package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Disconnecting locally never revokes OAuth at the provider and never deletes a repository.
// Re-provisioning is required before publishing after changing the Google channel.
func (a *App) githubDisconnect(w http.ResponseWriter, r *http.Request) {
	a.Lock()
	a.ghToken, a.ghLogin, a.ghCode, a.ghDeviceCode, a.ghVerifyURL = "", "", "", "", ""
	a.ghState = "GitHub desconectado localmente"
	a.cfg.GHDPAPI = ""
	a.ghChecked = ""
	a.Unlock()
	if err := a.saveConfig(); err != nil {
		apiErr(w, 500, "Não foi possível salvar a desconexão: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "GitHub desconectado somente neste computador. Repositórios existentes foram preservados."})
}

func (a *App) googleDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := backendCall("POST", "/api/local/disconnect", map[string]any{}, nil); err != nil {
		apiErr(w, 503, "Backend local: "+err.Error())
		return
	}
	a.Lock()
	a.googleRefresh = ""
	a.driveConnected = false
	a.googleAccess = ""
	a.channelID = ""
	a.channelName = ""
	a.googleChecked = ""
	a.cfg.Ready = false
	a.Unlock()
	if err := a.saveConfig(); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Google desconectado do laboratório. Nenhum repositório ou vídeo foi apagado."})
}
func (a *App) checkConnections(w http.ResponseWriter, r *http.Request) {
	a.Lock()
	gh := a.ghToken
	hasGoogle := a.googleRefresh != "" || a.googleAccess != ""
	hasChatGPT := a.chatGPT.AccessToken != "" || a.chatGPT.RefreshToken != ""
	a.Unlock()
	results := map[string]string{"github": "desconectado", "google": "desconectado", "drive": "desconectado", "chatgpt": "desconectado"}
	if gh != "" {
		var me struct {
			Login string `json:"login"`
		}
		if _, err := getJSON(githubBase+"/user", gh, &me); err != nil || me.Login == "" {
			results["github"] = "erro: não foi possível validar a autorização GitHub"
		} else {
			results["github"] = "verificado"
			a.Lock()
			a.ghLogin = me.Login
			a.Unlock()
		}
	}
	if hasGoogle {
		if err := a.verifyGoogleChannel(); err != nil {
			results["google"] = "erro: " + err.Error()
		} else {
			results["google"] = "verificado"
		}
	}
	// A autorização de upload do Drive é independente: uma falha aqui não invalida YouTube.
	a.Lock()
	driveLinked := a.driveConnected
	a.Unlock()
	if driveLinked {
		if err := backendCall("GET", "/api/local/drive/verify", nil, nil); err != nil {
			results["drive"] = "erro: " + err.Error()
		} else {
			results["drive"] = "verificado"
		}
	}
	if hasChatGPT {
		token, err := a.chatGPTAccess()
		if err != nil {
			results["chatgpt"] = "erro: autorização ChatGPT indisponível"
		} else if _, err := availableModels(token); err != nil {
			results["chatgpt"] = "erro: não foi possível acessar modelos do ChatGPT"
		} else {
			results["chatgpt"] = "verificado"
		}
	}
	a.Lock()
	a.ghChecked, a.googleChecked, a.chatgptChecked = results["github"], results["google"], results["chatgpt"]
	a.Unlock()
	writeJSON(w, 200, map[string]any{"results": results, "checked_at": time.Now().Format(time.RFC3339)})
}

type savedChat struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	History []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"history"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	GuideStep int            `json:"guide_step"`
	Subject   string         `json:"subject"`
	Draft     map[string]any `json:"draft"`
}

type savedChats struct {
	Tabs   []savedChat `json:"tabs"`
	Active string      `json:"active"`
}

func chatsPath() string { return filepath.Join(filepath.Dir(cfgPath()), "conversations.dat") }

func (a *App) migrateConversationHistory() {
	dest := chatsPath()
	if _, err := os.Stat(dest); err == nil || !os.IsNotExist(err) {
		return
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return
	}
	source := filepath.Join(base, "LoopingBRPublisherDesktopV06", "conversations.dat")
	content, err := os.ReadFile(source)
	if os.IsNotExist(err) {
		source = filepath.Join(base, "LoopingBRPublisherDesktopV05", "conversations.dat")
		content, err = os.ReadFile(source)
	}
	if err != nil || len(content) > 512*1024 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return
	}
	_ = os.WriteFile(dest, content, 0600)
}
func validateChats(chats savedChats) error {
	if len(chats.Tabs) > 12 {
		return errors.New("Limite de 12 conversas")
	}
	for _, t := range chats.Tabs {
		if len(t.ID) < 1 || len(t.ID) > 64 || len(t.Title) > 256 || len(t.Subject) > 640 || t.GuideStep < 0 || t.GuideStep > 20 || len(t.History) > 24 || len(t.Messages) > 65 {
			return errors.New("Dados da conversa inválidos")
		}
		for _, m := range t.History {
			if (m.Role != "user" && m.Role != "assistant") || len(m.Content) > 2800 {
				return errors.New("Histórico inválido")
			}
		}
		for _, m := range t.Messages {
			if (m.Role != "user" && m.Role != "assistant") || len(m.Content) > 5000 {
				return errors.New("Mensagem inválida")
			}
		}
		if len(t.Draft) > 18 {
			return errors.New("Rascunho inválido")
		}
	}
	return nil
}

func (a *App) saveConversations(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	defer r.Body.Close()
	var chats savedChats
	if err := json.NewDecoder(r.Body).Decode(&chats); err != nil {
		apiErr(w, 400, "Arquivo de conversa inválido")
		return
	}
	if err := validateChats(chats); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	plaintext, _ := json.Marshal(chats)
	enc, err := protectSecret(string(plaintext))
	if err != nil {
		apiErr(w, 500, "Falha ao proteger as conversas")
		return
	}
	p := chatsPath()
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		apiErr(w, 500, "Falha ao criar pasta local")
		return
	}
	tmp := p + ".tmp"
	if err = os.WriteFile(tmp, []byte(base64.StdEncoding.EncodeToString(enc)), 0600); err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		apiErr(w, 500, "Falha ao salvar histórico local")
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Histórico salvo neste computador"})
}

func (a *App) loadConversations(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(chatsPath())
	if os.IsNotExist(err) {
		writeJSON(w, 200, savedChats{Tabs: []savedChat{}})
		return
	}
	if err != nil {
		apiErr(w, 500, "Falha ao abrir histórico")
		return
	}
	enc, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		apiErr(w, 500, "Histórico local inválido")
		return
	}
	plaintext, err := unprotectSecret(enc)
	if err != nil {
		apiErr(w, 500, "Histórico protegido para outro usuário Windows ou arquivo danificado")
		return
	}
	var chats savedChats
	if err = json.Unmarshal(plaintext, &chats); err != nil || validateChats(chats) != nil {
		apiErr(w, 500, "Histórico local inválido")
		return
	}
	writeJSON(w, 200, chats)
}
