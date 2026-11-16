package main

// Upload Drive do laboratório: o arquivo nunca é gravado em disco pelo Desktop.
// A sessão retomável só cria um arquivo visível quando TODOS os bytes são recebidos.
// Falhas NÃO iniciam publicação, nem fazem uma segunda tentativa automática.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	driveAPIBase                  = "https://www.googleapis.com"
	driveChunkSize                = 8 * 1024 * 1024
	maxVideoUploadBytes     int64 = 4 * 1024 * 1024 * 1024
	maxThumbnailUploadBytes int64 = 50 * 1000 * 1000
)

var driveHTTP = &http.Client{Timeout: 5 * time.Minute}

type driveProgress struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Confirmed int64  `json:"confirmed"`
	Total     int64  `json:"total"`
	State     string `json:"state"`
}

type driveUploadManager struct {
	sync.Mutex
	current *driveProgress
	running bool
}

var uploadManager driveUploadManager

func (m *driveUploadManager) begin(id, kind string, total int64) error {
	m.Lock()
	defer m.Unlock()
	if m.running {
		return errors.New("Outro envio está em andamento. Aguarde ou cancele antes de iniciar outro.")
	}
	m.current = &driveProgress{ID: id, Kind: kind, Total: total, State: "enviando"}
	m.running = true
	return nil
}
func (m *driveUploadManager) update(id string, confirmed int64) {
	m.Lock()
	defer m.Unlock()
	if m.current != nil && m.current.ID == id {
		m.current.Confirmed = confirmed
	}
}
func (m *driveUploadManager) finish(id, state string) {
	m.Lock()
	defer m.Unlock()
	if m.current != nil && m.current.ID == id {
		m.current.State = state
	}
	m.running = false
}
func (m *driveUploadManager) snapshot(id string) (driveProgress, bool) {
	m.Lock()
	defer m.Unlock()
	if m.current == nil || m.current.ID != id {
		return driveProgress{}, false
	}
	return *m.current, true
}

type driveFileInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MimeType    string `json:"mimeType"`
	Size        string `json:"size"`
	MD5         string `json:"md5Checksum"`
	WebViewLink string `json:"webViewLink"`
}

type driveUploadResult struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	Kind           string `json:"kind"`
	Link           string `json:"link"`
	HistorySaved   bool   `json:"history_saved"`
	HistoryWarning string `json:"history_warning,omitempty"`
}

func safeDriveName(s string) (string, error) {
	s = filepath.Base(strings.ReplaceAll(s, "\\", "/"))
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == ".." || len([]byte(s)) > 190 || strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("Nome de arquivo inválido")
	}
	return s, nil
}
func uploadFileSpec(kind, name string, n int64) (string, error) {
	if n <= 0 {
		return "", errors.New("Arquivo vazio não pode ser enviado")
	}
	switch kind {
	case "video":
		if n > maxVideoUploadBytes {
			return "", errors.New("Vídeo maior que 4 GB. O motor atual do Publisher aceita até 4 GB")
		}
		if !strings.EqualFold(filepath.Ext(name), ".mp4") {
			return "", errors.New("O publicador atual aceita vídeo MP4. Converta o arquivo antes de enviar")
		}
		return "video/mp4", nil
	case "thumbnail":
		if n > maxThumbnailUploadBytes {
			return "", errors.New("Thumbnail maior que 50 MB")
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".png":
			return "image/png", nil
		case ".jpg", ".jpeg":
			return "image/jpeg", nil
		default:
			return "", errors.New("A thumbnail deve ser PNG ou JPG")
		}
	default:
		return "", errors.New("Tipo de arquivo não reconhecido")
	}
}

func googleRequest(ctx context.Context, method, uri, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, uri, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req, nil
}

func readDriveError(res *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 16*1024))
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &parsed)
	message := parsed.Error.Message
	if res.StatusCode == 403 && (strings.Contains(strings.ToLower(message), "scope") || strings.Contains(strings.ToLower(message), "permission")) {
		return errors.New("Permissão insuficiente no Google Drive. Autorize novamente o Google com a permissão drive.file")
	}
	if message == "" {
		message = res.Status
	}
	return fmt.Errorf("Google Drive HTTP %d: %s", res.StatusCode, message)
}

// All external endpoints are passed in so tests can exercise real HTTP behavior without Google credentials.
func streamToDrive(ctx context.Context, input io.Reader, name, mimeType string, size int64, getToken func() (string, error), endpoint string, client *http.Client, onProgress func(int64)) (driveFileInfo, error) {
	if size <= 0 {
		return driveFileInfo{}, errors.New("Tamanho inválido")
	}
	token, err := getToken()
	if err != nil {
		return driveFileInfo{}, err
	}
	metadata, _ := json.Marshal(map[string]string{"name": name, "mimeType": mimeType})
	startURL := endpoint + "/upload/drive/v3/files?uploadType=resumable&fields=id,name,mimeType,size,md5Checksum,webViewLink"
	req, err := googleRequest(ctx, "POST", startURL, token, bytes.NewReader(metadata))
	if err != nil {
		return driveFileInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Type", mimeType)
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
	res, err := client.Do(req)
	if err != nil {
		return driveFileInfo{}, fmt.Errorf("Não foi possível iniciar o upload: %w", err)
	}
	if res.StatusCode != 200 && res.StatusCode != 201 {
		defer res.Body.Close()
		return driveFileInfo{}, readDriveError(res)
	}
	location := res.Header.Get("Location")
	_ = res.Body.Close()
	session, err := url.Parse(location)
	if err != nil || session.Scheme != "https" && session.Scheme != "http" || session.Host == "" || session.User != nil {
		return driveFileInfo{}, errors.New("Google não retornou uma sessão de upload válida")
	}
	// Never send an access token to a third-party host supplied in Location.
	canonical, err := url.Parse(endpoint)
	if err != nil {
		return driveFileInfo{}, err
	}
	if !strings.EqualFold(canonical.Host, session.Host) && !(canonical.Host == "www.googleapis.com" && session.Host == "upload.googleapis.com" && session.Scheme == "https") {
		return driveFileInfo{}, errors.New("Google retornou um endereço inesperado para a sessão")
	}
	hash := md5.New()
	offset := int64(0)
	buf := make([]byte, driveChunkSize)
	for offset < size {
		if err = ctx.Err(); err != nil {
			return driveFileInfo{}, errors.New("Envio cancelado; nenhuma publicação foi iniciada")
		}
		n := int64(len(buf))
		if left := size - offset; left < n {
			n = left
		}
		_, err = io.ReadFull(input, buf[:n])
		if err != nil {
			return driveFileInfo{}, errors.New("Leitura do arquivo interrompida; upload não concluído")
		}
		if offset+n == size {
			var extra [1]byte
			cnt, e := input.Read(extra[:])
			if cnt != 0 || e == nil {
				return driveFileInfo{}, errors.New("Tamanho do arquivo não corresponde ao informado; upload não concluído")
			}
			if e != io.EOF {
				return driveFileInfo{}, errors.New("Falha ao concluir leitura do arquivo")
			}
		}
		_, _ = hash.Write(buf[:n])
		token, err = getToken()
		if err != nil {
			return driveFileInfo{}, err
		}
		put, err := googleRequest(ctx, "PUT", location, token, bytes.NewReader(buf[:n]))
		if err != nil {
			return driveFileInfo{}, err
		}
		put.Header.Set("Content-Type", mimeType)
		put.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+n-1, size))
		put.ContentLength = n
		response, e := client.Do(put)
		if e != nil {
			return driveFileInfo{}, fmt.Errorf("Falha de rede no envio. Confira seu Drive antes de tentar novamente: %w", e)
		}
		if offset+n < size {
			if response.StatusCode != 308 {
				defer response.Body.Close()
				return driveFileInfo{}, readDriveError(response)
			}
			// Check the acknowledged byte position; never silently skip data.
			expected := fmt.Sprintf("bytes=0-%d", offset+n-1)
			got := response.Header.Get("Range")
			_ = response.Body.Close()
			if got != expected {
				return driveFileInfo{}, fmt.Errorf("Drive confirmou intervalo inesperado (%s); envio interrompido sem repetição automática", got)
			}
			offset += n
			if onProgress != nil {
				onProgress(offset)
			}
			continue
		}
		if response.StatusCode != 200 && response.StatusCode != 201 {
			defer response.Body.Close()
			return driveFileInfo{}, readDriveError(response)
		}
		var info driveFileInfo
		e = json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&info)
		_ = response.Body.Close()
		if e != nil || info.ID == "" {
			return driveFileInfo{}, errors.New("Google pode ter concluído o arquivo, mas não confirmou seu ID. Confira o Drive antes de reenviar")
		}
		// Confirm the server's committed file size and hash before filling the publication form.
		if info.Size == "" || info.MD5 == "" {
			token, e = getToken()
			if e != nil {
				return driveFileInfo{}, errors.New("Arquivo enviado, mas não foi possível verificá-lo. Confira o Drive antes de reenviar")
			}
			metaURL := endpoint + "/drive/v3/files/" + url.PathEscape(info.ID) + "?fields=id,name,mimeType,size,md5Checksum,webViewLink"
			m, e := googleRequest(ctx, "GET", metaURL, token, nil)
			if e != nil {
				return driveFileInfo{}, e
			}
			mr, e := client.Do(m)
			if e != nil {
				return driveFileInfo{}, errors.New("Arquivo enviado, mas a verificação falhou. Confira o Drive antes de reenviar")
			}
			if mr.StatusCode != 200 {
				_ = mr.Body.Close()
				return driveFileInfo{}, errors.New("Arquivo enviado, mas a consulta de verificação falhou. Confira o Drive antes de reenviar")
			}
			e = json.NewDecoder(io.LimitReader(mr.Body, 64*1024)).Decode(&info)
			_ = mr.Body.Close()
			if e != nil {
				return driveFileInfo{}, errors.New("Resposta de verificação inválida; confira o Drive antes de reenviar")
			}
		}
		finalSize, e := strconv.ParseInt(info.Size, 10, 64)
		if e != nil || finalSize != size || info.MD5 == "" || !strings.EqualFold(info.MD5, hex.EncodeToString(hash.Sum(nil))) {
			return driveFileInfo{}, errors.New("Google recebeu arquivo com tamanho ou integridade inesperados. Não foi usado na publicação. Confira o Drive")
		}
		if onProgress != nil {
			onProgress(size)
		}
		return info, nil
	}
	return driveFileInfo{}, errors.New("Arquivo não concluído")
}

func (a *App) driveQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiErr(w, 405, "Método inválido")
		return
	}
	token, e := a.googleAccessToken()
	if e != nil {
		apiErr(w, 503, e.Error())
		return
	}
	req, e := googleRequest(r.Context(), "GET", driveAPIBase+"/drive/v3/about?fields=storageQuota(limit,usage,usageInDrive,usageInDriveTrash)", token, nil)
	if e != nil {
		apiErr(w, 502, "Não foi possível consultar o armazenamento")
		return
	}
	res, e := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if e != nil {
		apiErr(w, 502, "Não foi possível consultar o armazenamento no Drive")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		apiErr(w, 502, readDriveError(res).Error())
		return
	}
	var quota struct {
		StorageQuota struct {
			Limit        string `json:"limit"`
			Usage        string `json:"usage"`
			UsageInDrive string `json:"usageInDrive"`
		} `json:"storageQuota"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(&quota); e != nil {
		apiErr(w, 502, "Resposta inválida de armazenamento")
		return
	}
	writeJSON(w, 200, map[string]any{"limit": quota.StorageQuota.Limit, "usage": quota.StorageQuota.Usage, "usage_in_drive": quota.StorageQuota.UsageInDrive})
}

func (a *App) driveProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiErr(w, 405, "Método inválido")
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if e := readJSON(r, &in); e != nil {
		apiErr(w, 400, "Consulta inválida")
		return
	}
	st, ok := uploadManager.snapshot(in.ID)
	if !ok {
		apiErr(w, 404, "Envio não encontrado ou finalizado nesta sessão")
		return
	}
	writeJSON(w, 200, st)
}

func (a *App) driveUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiErr(w, 405, "Método inválido")
		return
	}
	// Limit the entire request: no need for a local copy, and no unlimited buffering.
	r.Body = http.MaxBytesReader(w, r.Body, maxVideoUploadBytes+1024*1024)
	kind := r.Header.Get("X-LoopingBR-File-Kind")
	decodedName, e := url.QueryUnescape(r.Header.Get("X-LoopingBR-File-Name"))
	if e != nil {
		apiErr(w, 400, "Nome de arquivo inválido")
		return
	}
	name, e := safeDriveName(decodedName)
	if e != nil {
		apiErr(w, 400, e.Error())
		return
	}
	declaredSize, e := strconv.ParseInt(r.Header.Get("X-LoopingBR-File-Size"), 10, 64)
	if e != nil {
		apiErr(w, 400, "Tamanho não informado")
		return
	}
	fileMIME, e := uploadFileSpec(kind, name, declaredSize)
	if e != nil {
		apiErr(w, 400, e.Error())
		return
	}
	uploadID := r.Header.Get("X-LoopingBR-Upload-ID")
	if len(uploadID) < 24 || len(uploadID) > 100 {
		apiErr(w, 400, "Identificador de envio inválido")
		return
	}
	if e = uploadManager.begin(uploadID, kind, declaredSize); e != nil {
		apiErr(w, 409, e.Error())
		return
	}
	completed := false
	defer func() {
		if completed {
			uploadManager.finish(uploadID, "concluído")
		} else {
			uploadManager.finish(uploadID, "falhou ou foi cancelado")
		}
	}()
	var start backendStatus
	if e := backendCall("GET", "/api/local/status", nil, &start); e != nil || !start.Connected || !start.DriveConnected || start.DriveAccountID == "" {
		apiErr(w, 403, "Não foi possível confirmar a conta Google que receberá o arquivo")
		return
	}
	token, e := a.driveAccessToken()
	if e != nil {
		apiErr(w, 403, "Autorize o envio ao Drive na etapa 3: "+e.Error())
		return
	}
	_ = token // Short-lived tokens are always obtained fresh for each chunk by streamToDrive.
	// Raw request body is precisely the file bytes; no multipart parser or staging folder required.
	// JS sends file directly, with its exact byte length provided in headers.
	// Verify basic file signatures before opening a Drive upload session.
	rawFile := bufio.NewReaderSize(r.Body, 4096)
	head, err := rawFile.Peek(12)
	if err != nil {
		apiErr(w, 400, "Arquivo pequeno demais ou interrompido antes do envio")
		return
	}
	valid := false
	switch fileMIME {
	case "video/mp4":
		valid = len(head) >= 8 && string(head[4:8]) == "ftyp"
	case "image/png":
		valid = bytes.HasPrefix(head, []byte{137, 80, 78, 71, 13, 10, 26, 10})
	case "image/jpeg":
		valid = bytes.HasPrefix(head, []byte{255, 216, 255})
	}
	if !valid {
		apiErr(w, 400, "Conteúdo não corresponde ao formato selecionado (MP4, PNG ou JPG)")
		return
	}
	info, e := streamToDrive(r.Context(), rawFile, name, fileMIME, declaredSize, a.driveAccessToken, driveAPIBase, driveHTTP, func(n int64) { uploadManager.update(uploadID, n) })
	if e != nil {
		apiErr(w, 502, e.Error())
		return
	}
	if !drivePattern.MatchString(info.ID) {
		apiErr(w, 502, "Google retornou um identificador de arquivo inesperado")
		return
	}
	// Se o usuário trocar a conta no meio do upload, não selecionar nem registrar
	// automaticamente um arquivo cuja origem ficou incerta.
	var end backendStatus
	if e := backendCall("GET", "/api/local/status", nil, &end); e != nil || !end.Connected || !end.DriveConnected || end.DriveAccountID != start.DriveAccountID {
		apiErr(w, 409, "A conta Google mudou ou ficou indisponível durante o upload. Confira o Drive antes de tentar novamente; o ID não foi selecionado.")
		return
	}
	completed = true
	link := info.WebViewLink
	if link == "" {
		link = "https://drive.google.com/file/d/" + info.ID + "/view"
	}
	result := driveUploadResult{ID: info.ID, Name: name, Size: declaredSize, Kind: kind, Link: link}
	// O histórico só recebe arquivos já confirmados em tamanho e MD5 pelo Drive.
	// Falha na gravação do histórico não desfaz o arquivo enviado e nunca causa retry.
	if err := backendCall("POST", "/api/local/drive/uploads/record", map[string]any{"id": info.ID, "name": name, "kind": kind, "size": declaredSize}, nil); err != nil {
		result.HistoryWarning = "Upload confirmado, mas o histórico não pôde ser atualizado. Use o ID exibido: " + info.ID
	} else {
		result.HistorySaved = true
	}
	writeJSON(w, 201, result)
}

// O histórico é armazenado pelo backend e sempre filtrado pela conta Google atual.
func (a *App) recentDriveUploads(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		apiErr(w, 405, "Método inválido")
		return
	}
	var data struct {
		Uploads []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Kind       string `json:"kind"`
			Size       int64  `json:"size"`
			UploadedAt string `json:"uploaded_at"`
		} `json:"uploads"`
	}
	if err := backendCall("GET", "/api/local/drive/uploads", nil, &data); err != nil {
		apiErr(w, 503, "Histórico do Drive indisponível: "+err.Error())
		return
	}
	writeJSON(w, 200, data)
}
