package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const backendLocalURL = "http://127.0.0.1:8765"

var backendHTTP = &http.Client{Timeout: 80 * time.Second}

func backendKey() (string, error) {
	base, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	path := filepath.Join(base, "LoopingBRHybridV1014Backend", "admin.key")
	if env := os.Getenv("LOOPINGBR_HYBRID_PROFILE_DIR"); env != "" {
		path = filepath.Join(env, "admin.key")
	}
	data, e := os.ReadFile(path)
	if e != nil {
		return "", errors.New("inicie o backend local primeiro (admin.key ainda não existe)")
	}
	return strings.TrimSpace(string(data)), nil
}
func backendCall(method, path string, body any, out any) error {
	key, e := backendKey()
	if e != nil {
		return e
	}
	var buf io.Reader
	if body != nil {
		v, e := json.Marshal(body)
		if e != nil {
			return e
		}
		buf = bytes.NewReader(v)
	}
	req, e := http.NewRequest(method, backendLocalURL+path, buf)
	if e != nil {
		return e
	}
	req.Header.Set("X-LoopingBR-Admin-Key", key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, e := backendHTTP.Do(req)
	if e != nil {
		return errors.New("backend offline; execute INICIAR-BACKEND-LAB.bat antes do Desktop")
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 128*1024))
	if e != nil {
		return e
	}
	if res.StatusCode >= 300 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &msg)
		if msg.Error == "" {
			msg.Error = res.Status
		}
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, msg.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type backendStatus struct {
	Connected          bool   `json:"connected"`
	DriveConnected     bool   `json:"drive_connected"`
	AnalyticsConnected bool   `json:"analytics_connected"`
	DriveAccountID     string `json:"drive_account_id"`
	ChannelID          string `json:"channel_id"`
	ChannelName        string `json:"channel_name"`
	PublicURL          bool   `json:"public_url"`
	BrokerURL          string `json:"broker_url"`
	OAuthConfigured    bool   `json:"oauth_configured"`
	RepoRegistered     bool   `json:"repo_registered"`
	Repo               string `json:"repo"`
}

func (a *App) syncBackendStatus() error {
	var status backendStatus
	if e := backendCall("GET", "/api/local/status", nil, &status); e != nil {
		a.Lock()
		a.googleRefresh = ""
		a.analyticsConnected = false
		a.googleSecret = ""
		a.googleChecked = "erro: backend local offline"
		a.Unlock()
		return e
	}
	a.Lock()
	defer a.Unlock()
	a.driveConnected = status.DriveConnected && status.Connected
	a.analyticsConnected = status.AnalyticsConnected && status.Connected
	if status.Connected {
		a.googleRefresh = "VINCULO_NO_BACKEND"
		a.channelID = status.ChannelID
		a.channelName = status.ChannelName
	} else {
		a.googleRefresh = ""
		a.channelID = ""
		a.channelName = ""
	}
	if status.OAuthConfigured {
		a.googleSecret = "configurado-no-backend"
	} else {
		a.googleSecret = ""
	}
	if status.Connected && a.cfg.SavedChannelID != "" && a.cfg.SavedChannelID != status.ChannelID {
		a.cfg.Ready = false
	}
	if a.cfg.Repo != "" && (!status.RepoRegistered || status.Repo != a.cfg.Repo) {
		a.cfg.Ready = false
	}
	if status.Connected {
		a.googleChecked = "conexão disponível no backend"
	} else {
		a.googleChecked = "desconectado"
	}
	return nil
}
func (a *App) verifyGoogleChannel() error {
	if e := backendCall("GET", "/api/local/verify", nil, nil); e != nil {
		return e
	}
	return a.syncBackendStatus()
}

// Token efêmero somente para pesquisa local de títulos; o Refresh Token permanece no backend.
func (a *App) googleAccessToken() (string, error) {
	var reply struct {
		AccessToken string `json:"access_token"`
	}
	if e := backendCall("GET", "/api/local/access", nil, &reply); e != nil {
		return "", e
	}
	if reply.AccessToken == "" {
		return "", errors.New("backend não retornou token Google")
	}
	return reply.AccessToken, nil
}
func (a *App) driveAccessToken() (string, error) {
	var reply struct {
		AccessToken string `json:"access_token"`
	}
	if e := backendCall("GET", "/api/local/drive/access", nil, &reply); e != nil {
		return "", e
	}
	if reply.AccessToken == "" {
		return "", errors.New("Backend não retornou token Drive")
	}
	return reply.AccessToken, nil
}
func backendBrokerURL() (string, error) {
	var st backendStatus
	if e := backendCall("GET", "/api/local/status", nil, &st); e != nil {
		return "", e
	}
	if !st.PublicURL || !strings.HasPrefix(st.BrokerURL, "https://") {
		return "", errors.New("é necessário túnel HTTPS configurado e backend reiniciado")
	}
	return st.BrokerURL, nil
}
