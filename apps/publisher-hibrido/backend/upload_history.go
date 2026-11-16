package main

// Apenas registros confirmados pelo Google Drive; não armazenamos bytes de mídia
// nem tokens aqui. API interna autenticada por admin key. Em uma futura versão
// Web/mobile, o acesso deve ser substituído por autenticação por usuário.
import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var uploadDriveIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,100}$`)

type UploadRecord struct {
	AccountID  string `json:"account_id"` // escopo da conta, nunca enviado ao cliente
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Size       int64  `json:"size"`
	UploadedAt string `json:"uploaded_at"`
}

// Retorna o identificador da conta Google somente para operações autenticadas.
func (b *Backend) currentDriveAccountLocked() (string, error) {
	if b.refresh == "" || b.driveRefresh == "" || !sameGoogleAccount(b.saved.YouTubeAccountID, b.saved.DriveAccountID) {
		return "", errors.New("Autorize o YouTube e o Drive com a mesma conta antes de consultar o histórico")
	}
	return b.saved.DriveAccountID, nil
}

func (b *Backend) listRecentUploads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "Use GET")
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	account, e := b.currentDriveAccountLocked()
	if e != nil {
		fail(w, 403, e.Error())
		return
	}
	out := make([]UploadRecord, 0, 6)
	for _, entry := range b.saved.RecentUploads {
		if entry.AccountID == account && len(out) < 6 {
			entry.AccountID = ""
			out = append(out, entry)
		}
	}
	writeJSON(w, 200, map[string]any{"uploads": out})
}

func (b *Backend) recordRecentUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "Use POST")
		return
	}
	var in struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
		Size int64  `json:"size"`
	}
	if e := readJSON(r, &in); e != nil {
		fail(w, 400, "Registro de envio inválido")
		return
	}
	if !uploadDriveIDPattern.MatchString(in.ID) || len(in.Name) < 1 || len(in.Name) > 190 || strings.ContainsAny(in.Name, "\r\n\x00") ||
		(in.Kind != "video" && in.Kind != "thumbnail") || in.Size <= 0 ||
		(in.Kind == "video" && in.Size > 2*1024*1024*1024) ||
		(in.Kind == "thumbnail" && in.Size > 50*1000*1000) {
		fail(w, 400, "Arquivo confirmado possui metadados inválidos")
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	account, e := b.currentDriveAccountLocked()
	if e != nil {
		fail(w, 403, e.Error())
		return
	}
	entry := UploadRecord{AccountID: account, ID: in.ID, Name: in.Name, Kind: in.Kind, Size: in.Size, UploadedAt: time.Now().UTC().Format(time.RFC3339)}
	prior := b.saved.RecentUploads
	next := make([]UploadRecord, 0, 31)
	next = append(next, entry)
	// Deduplicação pelo ID e retenção máxima de seis por conta.
	perAccount := map[string]int{account: 1}
	for _, v := range prior {
		if v.ID == entry.ID && v.AccountID == account {
			continue
		}
		if v.AccountID == "" || perAccount[v.AccountID] >= 6 {
			continue
		}
		perAccount[v.AccountID]++
		next = append(next, v)
		if len(next) >= 30 {
			break
		}
	}
	b.saved.RecentUploads = next
	if e = b.saveLocked(); e != nil {
		b.saved.RecentUploads = prior
		fail(w, 500, "Falha ao salvar o histórico local")
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true})
}

// Compile-time exercise: use json raw format in exported JSON as stable REST contract.
var _ = json.Valid
