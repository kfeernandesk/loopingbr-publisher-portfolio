package main

// Historico de publicacoes (somente leitura). Nenhum runner, pedido ou
// arquivo remoto e modificado; o GitHub Actions existente e a fonte da verdade.
import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var publicationFileRE = regexp.MustCompile(`^([0-9]{8}T[0-9]{6}_[a-f0-9]{16})\.json$`)
var youtubeVideoIDRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{11}$`)

type publicationFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

type PublicationRecord struct {
	RequestID        string `json:"request_id"`
	CreatedAt        string `json:"created_at"`
	Title            string `json:"title"`
	Description      string `json:"description,omitempty"`
	Visibility       string `json:"visibility"`
	Tipo             string `json:"tipo"`
	PublishAt        string `json:"publish_at,omitempty"`
	VideoDriveID     string `json:"video_drive_id,omitempty"`
	ThumbnailDriveID string `json:"thumbnail_drive_id,omitempty"`
	State            string `json:"state"`
	StateLabel       string `json:"state_label"`
	Note             string `json:"note,omitempty"`
	YouTubeVideoID   string `json:"youtube_video_id,omitempty"`
	YouTubeURL       string `json:"youtube_url,omitempty"`
	ThumbnailOK      *bool  `json:"thumbnail_ok,omitempty"`
}

type PublicationsResponse struct {
	Items     []PublicationRecord `json:"items"`
	Page      int                 `json:"page"`
	Limit     int                 `json:"limit"`
	HasMore   bool                `json:"has_more"`
	Repo      string              `json:"repo"`
	Scope     string              `json:"scope"`
	FetchedAt string              `json:"fetched_at"`
}

func requestDate(id string) string {
	t, err := time.Parse("20060102T150405", strings.Split(id, "_")[0])
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func classifyPublication(status string, videoID string) (string, string, string) {
	switch status {
	case "enviado":
		if youtubeVideoIDRE.MatchString(videoID) {
			return "uploaded", "Enviado ao YouTube", "O YouTube recebeu o upload; visibilidade e processamento dependem do YouTube."
		}
		return "uncertain", "Verificar no YouTube Studio", "O resultado diz enviado, mas não contém um ID de vídeo válido. Não repita o envio sem conferir."
	case "agendado":
		if youtubeVideoIDRE.MatchString(videoID) {
			return "scheduled", "Agendado no YouTube", "Agendamento registrado; não significa que o vídeo já esteja público."
		}
		return "uncertain", "Verificar agendamento", "O resultado não contém um ID de vídeo válido."
	case "interrompido_verificar_studio":
		return "uncertain", "Interrompido — conferir Studio", "Um upload pode ter sido concluído antes da falha. Não repita automaticamente."
	case "reservado_nao_reexecutar":
		return "reserved", "Execução reservada", "O Actions registrou o pedido; ainda não há confirmação do YouTube. Não repita a execução."
	case "":
		return "pending", "Aguardando resultado", "O pedido existe no GitHub, mas nenhum resultado foi registrado. Confira o GitHub Actions antes de repetir."
	default:
		return "uncertain", "Resultado não reconhecido", "Confira o resultado no GitHub Actions e no YouTube Studio."
	}
}

func (a *App) listPublicationFiles(repo, dir string) ([]publicationFile, error) {
	var files []publicationFile
	status, err := a.ghCall("GET", "/repos/"+repo+"/contents/publicacoes/"+dir, nil, &files)
	if status == http.StatusNotFound {
		return []publicationFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(files) > 1000 {
		return nil, errors.New("Histórico excede o limite desta versão; não é seguro exibir um conjunto incompleto")
	}
	var valid []publicationFile
	for _, f := range files {
		if f.Type == "file" && publicationFileRE.MatchString(f.Name) && f.Path == "publicacoes/"+dir+"/"+f.Name {
			valid = append(valid, f)
		}
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Name > valid[j].Name })
	return valid, nil
}

func (a *App) readPublicationFile(repo, dir, name string, out any) error {
	if !publicationFileRE.MatchString(name) {
		return errors.New("Nome de registro inválido")
	}
	var file struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	_, err := a.ghCall("GET", "/repos/"+repo+"/contents/publicacoes/"+dir+"/"+url.PathEscape(name), nil, &file)
	if err != nil {
		return err
	}
	if file.Encoding != "base64" {
		return errors.New("Formato do registro não reconhecido")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return err
	}
	if len(raw) > 24000 {
		return errors.New("Registro maior que o previsto")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}

func (a *App) publicationFromGitHub(repo string, request publicationFile, results map[string]bool, expectedChannel string) (PublicationRecord, bool) {
	id := strings.TrimSuffix(request.Name, ".json")
	p := PublicationRecord{RequestID: id, CreatedAt: requestDate(id), Title: "Registro " + id, State: "unavailable", StateLabel: "Dados indisponíveis", Note: "Não foi possível ler o pedido; consulte o GitHub Actions."}
	var j Job
	if err := a.readPublicationFile(repo, "aprovadas", request.Name, &j); err != nil {
		return p, true // nunca fingir sucesso quando o registro esta ilegivel
	}
	if j.ChannelID != expectedChannel {
		return PublicationRecord{}, false
	}
	p.Title = j.Title
	p.Description = j.Description
	p.VideoDriveID = j.VideoDriveID
	p.ThumbnailDriveID = j.ThumbnailDriveID
	p.Visibility = j.Visibility
	p.Tipo = j.Tipo
	p.PublishAt = j.PublishAt
	p.State, p.StateLabel, p.Note = classifyPublication("", "")
	if !results[request.Name] {
		return p, true
	}
	var result struct {
		Status         string `json:"status"`
		YouTubeVideoID string `json:"youtube_video_id"`
		ThumbnailOK    *bool  `json:"thumbnail_ok"`
	}
	if err := a.readPublicationFile(repo, "resultados", request.Name, &result); err != nil {
		p.State = "unavailable"
		p.StateLabel = "Resultado indisponível"
		p.Note = "Existe um resultado no GitHub, mas não foi possível lê-lo. Confira antes de repetir o envio."
		return p, true
	}
	p.State, p.StateLabel, p.Note = classifyPublication(result.Status, result.YouTubeVideoID)
	if (p.State == "uploaded" || p.State == "scheduled") && youtubeVideoIDRE.MatchString(result.YouTubeVideoID) {
		p.YouTubeVideoID = result.YouTubeVideoID
		p.YouTubeURL = "https://www.youtube.com/watch?v=" + result.YouTubeVideoID
		p.ThumbnailOK = result.ThumbnailOK
	}
	return p, true
}

func (a *App) publications(w http.ResponseWriter, r *http.Request) {
	a.Lock()
	repo, login, channel := a.cfg.Repo, a.ghLogin, a.cfg.SavedChannelID
	currentChannel := a.channelID
	a.Unlock()
	if err := validateOwnedRepo(repo, login); err != nil || channel == "" {
		apiErr(w, http.StatusForbidden, "Conecte o GitHub e prepare seu Publisher privado para consultar o histórico.")
		return
	}
	if currentChannel != "" && currentChannel != channel {
		apiErr(w, http.StatusForbidden, "O canal atual não corresponde ao repositório deste histórico.")
		return
	}
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			apiErr(w, 400, "Página inválida.")
			return
		}
		page = n
	}
	limit := 12
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 20 {
			apiErr(w, 400, "Limite inválido (1 a 20).")
			return
		}
		limit = n
	}
	approved, err := a.listPublicationFiles(repo, "aprovadas")
	if err != nil {
		apiErr(w, 502, "Falha ao consultar pedidos no GitHub: "+err.Error())
		return
	}
	resultFiles, err := a.listPublicationFiles(repo, "resultados")
	if err != nil {
		apiErr(w, 502, "Falha ao consultar resultados no GitHub: "+err.Error())
		return
	}
	results := make(map[string]bool, len(resultFiles))
	for _, f := range resultFiles {
		results[f.Name] = true
	}
	offset := (page - 1) * limit
	response := PublicationsResponse{Items: []PublicationRecord{}, Page: page, Limit: limit, Repo: repo, Scope: "only_loopingbr", FetchedAt: time.Now().UTC().Format(time.RFC3339)}
	if offset >= len(approved) {
		writeJSON(w, 200, response)
		return
	}
	end := offset + limit
	if end > len(approved) {
		end = len(approved)
	}
	response.HasMore = end < len(approved)
	selected := approved[offset:end]
	records := make([]PublicationRecord, len(selected))
	include := make([]bool, len(selected))
	jobs := make(chan int)
	var wg sync.WaitGroup
	// No maximo quatro consultas concorrentes para evitar sobrecarregar a API.
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				records[i], include[i] = a.publicationFromGitHub(repo, selected[i], results, channel)
			}
		}()
	}
	for i := range selected {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for i, record := range records {
		if include[i] {
			response.Items = append(response.Items, record)
		}
	}
	writeJSON(w, 200, response)
}

// Auxiliar puro para testes: o mesmo ID de pedido identifica uma unica publicacao.
func publicationKey(repo, requestID string) string { return fmt.Sprintf("%s/%s", repo, requestID) }
