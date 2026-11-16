// LoopingBR Hybrid Lab: backend local para OAuth Web e broker GitHub Actions OIDC.
// APENAS EXPERIMENTAL. Nunca usa credenciais do Publisher original.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	backendAddr  = "127.0.0.1:8765"
	redirectURI  = "http://127.0.0.1:8765/oauth/google/callback"
	oauthStart   = "https://accounts.google.com/o/oauth2/v2/auth"
	oauthToken   = "https://oauth2.googleapis.com/token"
	channelAPI   = "https://www.googleapis.com/youtube/v3/channels?part=snippet&mine=true"
	oidcAudience = "loopingbr-hybrid-lab-v1"
)

var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/loopingbr-desktop-[a-f0-9]{12}$`)
var branchPattern = regexp.MustCompile(`^[a-zA-Z0-9._/-]{1,80}$`)
var client = &http.Client{Timeout: 25 * time.Second}

type RepoInfo struct {
	FullName  string `json:"full_name"`
	ID        int64  `json:"id"`
	Branch    string `json:"branch"`
	ChannelID string `json:"channel_id"`
}
type Saved struct {
	RefreshDPAPI          string         `json:"refresh_dpapi"`
	DriveRefreshDPAPI     string         `json:"drive_refresh_dpapi,omitempty"`
	AnalyticsRefreshDPAPI string         `json:"analytics_refresh_dpapi,omitempty"`
	AnalyticsAccountID    string         `json:"analytics_account_id,omitempty"`
	AnalyticsChannelID    string         `json:"analytics_channel_id,omitempty"`
	YouTubeAccountID      string         `json:"youtube_account_id,omitempty"`
	DriveAccountID        string         `json:"drive_account_id,omitempty"`
	ChannelID             string         `json:"channel_id"`
	ChannelName           string         `json:"channel_name"`
	Repo                  RepoInfo       `json:"repo"`
	RecentUploads         []UploadRecord `json:"recent_uploads,omitempty"`
}
type Backend struct {
	mu                                  sync.Mutex
	refreshMu                           sync.Mutex
	webID, webSecret, publicURL, apiKey string
	saved                               Saved
	refresh                             string
	access                              string
	accessUntil                         time.Time
	driveRefresh, driveAccess           string
	driveAccessUntil                    time.Time
	analyticsRefresh, analyticsAccess   string
	analyticsAccessUntil                time.Time
	state, verifier, stateFlow          string
	stateUntil                          time.Time
	dir                                 string
}

func secureRandom() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func profileDir() string {
	if v := os.Getenv("LOOPINGBR_HYBRID_PROFILE_DIR"); v != "" {
		return v
	}
	d, e := os.UserConfigDir()
	if e != nil {
		log.Fatal(e)
	}
	return filepath.Join(d, "LoopingBRHybridV1014Backend")
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
func readJSON(r *http.Request, dest any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 8*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(dest)
}
func atomicWrite(file string, content []byte) error {
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, content, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
func (b *Backend) saveLocked() error {
	bs, e := json.MarshalIndent(b.saved, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(b.dir, "state.json"), bs)
}
func (b *Backend) status() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return map[string]any{"backend_online": true, "connected": b.refresh != "", "analytics_connected": b.analyticsRefresh != "" && b.refresh != "" && b.saved.AnalyticsAccountID == b.saved.YouTubeAccountID && b.saved.AnalyticsChannelID == b.saved.ChannelID, "drive_connected": b.driveRefresh != "" && b.saved.YouTubeAccountID != "" && b.saved.DriveAccountID == b.saved.YouTubeAccountID, "drive_account_id": func() string {
		if b.refresh != "" && b.driveRefresh != "" && sameGoogleAccount(b.saved.YouTubeAccountID, b.saved.DriveAccountID) {
			return b.saved.DriveAccountID
		}
		return ""
	}(), "channel_id": b.saved.ChannelID, "channel_name": b.saved.ChannelName, "repo": b.saved.Repo.FullName, "repo_registered": b.saved.Repo.ID > 0, "public_url": b.publicURL != "", "broker_url": b.publicURL, "oauth_configured": b.webID != "" && b.webSecret != ""}
}
func newBackend() (*Backend, error) {
	dir := profileDir()
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	b := &Backend{dir: dir, webID: strings.TrimSpace(os.Getenv("LOOPINGBR_WEB_CLIENT_ID")), webSecret: strings.TrimSpace(os.Getenv("LOOPINGBR_WEB_CLIENT_SECRET")), publicURL: strings.TrimSuffix(strings.TrimSpace(os.Getenv("LOOPINGBR_PUBLIC_URL")), "/")}
	if b.publicURL != "" {
		u, e := url.Parse(b.publicURL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return nil, errors.New("LOOPINGBR_PUBLIC_URL precisa ser uma URL HTTPS sem caminho, query ou credenciais")
		}
	}
	keyFile := filepath.Join(dir, "admin.key")
	if k, e := os.ReadFile(keyFile); e == nil {
		b.apiKey = strings.TrimSpace(string(k))
	} else if os.IsNotExist(e) {
		b.apiKey = secureRandom()
		if e = atomicWrite(keyFile, []byte(b.apiKey)); e != nil {
			return nil, e
		}
	} else {
		return nil, e
	}
	if len(b.apiKey) < 32 {
		return nil, errors.New("chave de administração local inválida")
	}
	saved, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e == nil {
		if e = json.Unmarshal(saved, &b.saved); e != nil {
			return nil, e
		}
		if b.saved.RefreshDPAPI != "" {
			encrypted, e := base64.StdEncoding.DecodeString(b.saved.RefreshDPAPI)
			if e != nil {
				return nil, e
			}
			plain, e := unprotectSecret(encrypted)
			if e != nil {
				return nil, fmt.Errorf("não foi possível restaurar autorização YouTube DPAPI: %w", e)
			}
			b.refresh = string(plain)
		}
		if b.saved.AnalyticsRefreshDPAPI != "" {
			encrypted, err := base64.StdEncoding.DecodeString(b.saved.AnalyticsRefreshDPAPI)
			if err != nil {
				return nil, err
			}
			plain, err := unprotectSecret(encrypted)
			if err != nil {
				return nil, fmt.Errorf("falha ao restaurar token do Analytics: %w", err)
			}
			b.analyticsRefresh = string(plain)
		}
		if b.saved.DriveRefreshDPAPI != "" {
			encrypted, e := base64.StdEncoding.DecodeString(b.saved.DriveRefreshDPAPI)
			if e != nil {
				return nil, e
			}
			plain, e := unprotectSecret(encrypted)
			if e != nil {
				return nil, fmt.Errorf("não foi possível restaurar autorização Drive DPAPI: %w", e)
			}
			b.driveRefresh = string(plain)
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return b, nil
}
func (b *Backend) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("X-LoopingBR-Admin-Key")
		if len(k) != len(b.apiKey) || subtle.ConstantTimeCompare([]byte(k), []byte(b.apiKey)) != 1 {
			fail(w, 403, "Acesso local não autorizado")
			return
		}
		if r.Method != "POST" && r.Method != "GET" {
			fail(w, 405, "Método inválido")
			return
		}
		next(w, r)
	}
}

// O Google rejeita youtube.upload + drive.file na MESMA solicitação OAuth.
// Preservamos o fluxo v0.9 para YouTube/leitura do Drive; o upload pede
// uma segunda autorização exclusiva drive.file, vinculada à mesma conta Google.
const youtubeScopes = "https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly"
const uploadScopes = "https://www.googleapis.com/auth/drive.file"

// Autorização separada e somente sob demanda, iniciada ao clicar em "Analisar".
// youtube.readonly + drive.readonly confirmam canal e identidade da mesma conta
// antes de aceitar qualquer token Analytics. Nada segue para o runner do GitHub.
const analyticsScopes = "https://www.googleapis.com/auth/yt-analytics.readonly https://www.googleapis.com/auth/youtube.readonly https://www.googleapis.com/auth/drive.readonly"

func (b *Backend) startGoogle(w http.ResponseWriter, r *http.Request) { b.startOAuth(w, r, "youtube") }
func (b *Backend) startDrive(w http.ResponseWriter, r *http.Request)  { b.startOAuth(w, r, "drive") }
func (b *Backend) startAnalytics(w http.ResponseWriter, r *http.Request) {
	b.startOAuth(w, r, "analytics")
}
func (b *Backend) startOAuth(w http.ResponseWriter, r *http.Request, flow string) {
	if r.Method != "POST" {
		fail(w, 405, "Use POST")
		return
	}
	if b.webID == "" || b.webSecret == "" {
		fail(w, 503, "Configure OAuth Web no backend")
		return
	}
	if flow != "youtube" && flow != "drive" && flow != "analytics" {
		fail(w, 400, "Tipo de autorização inválido")
		return
	}
	b.mu.Lock()
	if (flow == "drive" && (b.refresh == "" || b.saved.YouTubeAccountID == "")) || (flow == "analytics" && (b.refresh == "" || b.saved.YouTubeAccountID == "" || b.saved.ChannelID == "")) {
		b.mu.Unlock()
		fail(w, 409, "Primeiro autorize Google/YouTube na etapa 1")
		return
	}
	b.mu.Unlock()
	s := secureRandom()
	v := secureRandom() + secureRandom()
	sha := sha256.Sum256([]byte(v))
	b.mu.Lock()
	b.state, b.verifier, b.stateFlow = s, v, flow
	b.stateUntil = time.Now().Add(8 * time.Minute)
	b.mu.Unlock()
	scopes := youtubeScopes
	if flow == "drive" {
		scopes = uploadScopes
	} else if flow == "analytics" {
		scopes = analyticsScopes
	}
	q := url.Values{"client_id": {b.webID}, "response_type": {"code"}, "redirect_uri": {redirectURI}, "scope": {scopes}, "access_type": {"offline"}, "prompt": {"consent select_account"}, "state": {s}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sha[:])}}
	writeJSON(w, 200, map[string]string{"url": oauthStart + "?" + q.Encode()})
}

type googleReply struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func exchangeGoogle(ctx context.Context, values url.Values) (googleReply, error) {
	req, e := http.NewRequestWithContext(ctx, "POST", oauthToken, strings.NewReader(values.Encode()))
	if e != nil {
		return googleReply{}, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := client.Do(req)
	if e != nil {
		return googleReply{}, e
	}
	defer resp.Body.Close()
	var out googleReply
	if e = json.NewDecoder(io.LimitReader(resp.Body, 32*1024)).Decode(&out); e != nil {
		return out, errors.New("Resposta inválida do Google")
	}
	if resp.StatusCode != 200 || out.AccessToken == "" {
		return out, fmt.Errorf("Google OAuth HTTP %d: %s", resp.StatusCode, out.Error)
	}
	return out, nil
}
func channelForToken(ctx context.Context, token string) (string, string, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", channelAPI, nil)
	if e != nil {
		return "", "", e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e := client.Do(req)
	if e != nil {
		return "", "", e
	}
	defer resp.Body.Close()
	var got struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 96*1024)).Decode(&got); e != nil {
		return "", "", e
	}
	if resp.StatusCode != 200 || len(got.Items) != 1 || got.Items[0].ID == "" {
		return "", "", fmt.Errorf("YouTube HTTP %d: canal não identificado de forma única", resp.StatusCode)
	}
	return got.Items[0].ID, got.Items[0].Snippet.Title, nil
}

// Identificação da conta Google real, independente de selecionar um canal de marca.
// Tanto drive.readonly quanto drive.file permitem about.get (user.permissionId).
func googleDriveAccount(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/drive/v3/about?fields=user(permissionId)", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var v struct {
		User struct {
			PermissionID string `json:"permissionId"`
		} `json:"user"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(&v); err != nil {
		return "", errors.New("Resposta inválida ao verificar a conta Google Drive")
	}
	if res.StatusCode != 200 || v.User.PermissionID == "" {
		return "", fmt.Errorf("Não foi possível confirmar a identidade da conta Google (Drive HTTP %d)", res.StatusCode)
	}
	return v.User.PermissionID, nil
}
func sameGoogleAccount(youtubeID, driveID string) bool {
	return youtubeID != "" && driveID != "" && youtubeID == driveID
}

func (b *Backend) callbackGoogle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fail(w, 405, "Método inválido")
		return
	}
	state, code := r.URL.Query().Get("state"), r.URL.Query().Get("code")
	b.mu.Lock()
	valid := state != "" && state == b.state && time.Now().Before(b.stateUntil)
	v, flow := b.verifier, b.stateFlow
	b.state, b.verifier, b.stateFlow = "", "", ""
	b.mu.Unlock()
	if !valid || code == "" || (flow != "youtube" && flow != "drive" && flow != "analytics") {
		http.Error(w, "Autorização não concluída ou estado expirado. Reinicie a conexão no Desktop.", 400)
		return
	}
	vals := url.Values{"code": {code}, "client_id": {b.webID}, "client_secret": {b.webSecret}, "grant_type": {"authorization_code"}, "redirect_uri": {redirectURI}, "code_verifier": {v}}
	tok, err := exchangeGoogle(r.Context(), vals)
	if err != nil {
		http.Error(w, "Falha na troca do código OAuth: "+err.Error(), 502)
		return
	}
	if tok.RefreshToken == "" {
		http.Error(w, "Google não retornou Refresh Token. Tente autorizar novamente.", 502)
		return
	}
	accountID, err := googleDriveAccount(r.Context(), tok.AccessToken)
	if err != nil {
		http.Error(w, "Não foi possível identificar a conta Google: "+err.Error(), 502)
		return
	}
	protected, err := protectSecret(tok.RefreshToken)
	if err != nil {
		http.Error(w, "Não foi possível proteger o token no Windows.", 500)
		return
	}
	var channel, channelName string
	if flow == "youtube" || flow == "analytics" {
		channel, channelName, err = channelForToken(r.Context(), tok.AccessToken)
		if err != nil {
			http.Error(w, "Falha ao identificar canal YouTube: "+err.Error(), 502)
			return
		}
	}
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if flow == "analytics" {
		if b.refresh == "" || !sameGoogleAccount(b.saved.YouTubeAccountID, accountID) || b.saved.ChannelID != channel {
			http.Error(w, "Conta ou canal diferente do YouTube conectado. Autorize a mesma conta e canal.", http.StatusForbidden)
			return
		}
		before, oldRefresh, oldAccess, oldUntil := b.saved, b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil
		b.saved.AnalyticsRefreshDPAPI = base64.StdEncoding.EncodeToString(protected)
		b.saved.AnalyticsAccountID, b.saved.AnalyticsChannelID = accountID, channel
		b.analyticsRefresh, b.analyticsAccess = tok.RefreshToken, tok.AccessToken
		if tok.ExpiresIn < 180 {
			tok.ExpiresIn = 3600
		}
		b.analyticsAccessUntil = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		if err := b.saveLocked(); err != nil {
			b.saved, b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil = before, oldRefresh, oldAccess, oldUntil
			http.Error(w, "Falha ao salvar autorização Analytics no Windows.", http.StatusInternalServerError)
			return
		}
	} else if flow == "drive" {
		if b.refresh == "" || !sameGoogleAccount(b.saved.YouTubeAccountID, accountID) {
			http.Error(w, "Conta Google diferente da conectada no YouTube. Selecione a MESMA conta Google e tente novamente.", 403)
			return
		}
		oldSaved, oldDrive, oldAccess, oldUntil := b.saved, b.driveRefresh, b.driveAccess, b.driveAccessUntil
		b.saved.DriveRefreshDPAPI = base64.StdEncoding.EncodeToString(protected)
		b.saved.DriveAccountID = accountID
		b.driveRefresh, b.driveAccess = tok.RefreshToken, tok.AccessToken
		if tok.ExpiresIn < 180 {
			tok.ExpiresIn = 3600
		}
		b.driveAccessUntil = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		if err = b.saveLocked(); err != nil {
			b.saved, b.driveRefresh, b.driveAccess, b.driveAccessUntil = oldSaved, oldDrive, oldAccess, oldUntil
			http.Error(w, "Falha ao salvar autorização do Drive no Windows.", 500)
			return
		}
	} else {
		oldSaved, oldRefresh, oldAccess, oldUntil := b.saved, b.refresh, b.access, b.accessUntil
		oldDrive, oldDriveAccess, oldDriveUntil := b.driveRefresh, b.driveAccess, b.driveAccessUntil
		oldAnalytics, oldAnalyticsAccess, oldAnalyticsUntil := b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil
		if (b.saved.ChannelID != "" && b.saved.ChannelID != channel) || (b.saved.YouTubeAccountID != "" && b.saved.YouTubeAccountID != accountID) {
			// Conta/canal trocado: nenhuma credencial de Drive ou repo antiga deve ser reutilizada.
			b.saved.Repo = RepoInfo{}
			b.saved.DriveRefreshDPAPI, b.saved.DriveAccountID = "", ""
			b.driveRefresh, b.driveAccess, b.driveAccessUntil = "", "", time.Time{}
			b.saved.AnalyticsRefreshDPAPI, b.saved.AnalyticsAccountID, b.saved.AnalyticsChannelID = "", "", ""
			b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil = "", "", time.Time{}
		}
		b.saved.ChannelID, b.saved.ChannelName, b.saved.YouTubeAccountID = channel, channelName, accountID
		b.saved.RefreshDPAPI = base64.StdEncoding.EncodeToString(protected)
		b.refresh, b.access = tok.RefreshToken, tok.AccessToken
		if tok.ExpiresIn < 180 {
			tok.ExpiresIn = 3600
		}
		b.accessUntil = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		if err = b.saveLocked(); err != nil {
			b.saved, b.refresh, b.access, b.accessUntil = oldSaved, oldRefresh, oldAccess, oldUntil
			b.driveRefresh, b.driveAccess, b.driveAccessUntil = oldDrive, oldDriveAccess, oldDriveUntil
			b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil = oldAnalytics, oldAnalyticsAccess, oldAnalyticsUntil
			http.Error(w, "Falha ao salvar autorização no Windows.", 500)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	nonce := secureRandom()
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-"+nonce+"'")
	label := "Google / YouTube"
	if flow == "drive" {
		label = "Envio ao Google Drive"
	} else if flow == "analytics" {
		label = "YouTube Analytics (somente leitura)"
	}
	// Apenas callback de SUCESSO tenta fechar a aba; navegadores podem bloquear window.close().
	// As páginas de ERRO continuam abertas, com o motivo retornado ao usuário.
	_, _ = io.WriteString(w, "<!doctype html><html lang=pt-br><meta charset=utf-8><title>LoopingBR — Conectado</title><body style='font:16px Segoe UI,Arial,sans-serif;background:#101827;color:#e8f0ff;padding:42px'><main style='max-width:580px;margin:12vh auto;padding:30px;border:1px solid #405171;border-radius:16px;background:#1c273b'><h2>"+label+" autorizado com sucesso.</h2><p>A autorização foi salva. Esta aba tentará fechar automaticamente.</p><p id='close-note' style='color:#b6c6e4;font-size:13px'>Se o navegador impedir, você pode fechar esta aba manualmente e voltar ao LoopingBR.</p></main><script nonce='"+nonce+"'>setTimeout(function(){try{window.close()}catch(e){}},1200)</script></body></html>")
}
func (b *Backend) accessToken() (string, error) {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	b.mu.Lock()
	if b.access != "" && time.Until(b.accessUntil) > 2*time.Minute {
		a := b.access
		b.mu.Unlock()
		return a, nil
	}
	refresh := b.refresh
	b.mu.Unlock()
	if refresh == "" {
		return "", errors.New("Google não conectado")
	}
	vals := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {b.webID}, "client_secret": {b.webSecret}}
	t, e := exchangeGoogle(context.Background(), vals)
	if e != nil {
		return "", e
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if t.RefreshToken != "" && t.RefreshToken != b.refresh {
		p, e := protectSecret(t.RefreshToken)
		if e != nil {
			return "", e
		}
		before := b.saved.RefreshDPAPI
		b.saved.RefreshDPAPI = base64.StdEncoding.EncodeToString(p)
		if e = b.saveLocked(); e != nil {
			b.saved.RefreshDPAPI = before
			return "", e
		}
		b.refresh = t.RefreshToken
	}
	b.access = t.AccessToken
	if t.ExpiresIn < 180 {
		t.ExpiresIn = 3600
	}
	b.accessUntil = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	return b.access, nil
}

// A autorização drive.file é separada e nunca é enviada ao runner do GitHub.
func (b *Backend) driveAccessToken() (string, error) {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	b.mu.Lock()
	if !sameGoogleAccount(b.saved.YouTubeAccountID, b.saved.DriveAccountID) || b.driveRefresh == "" || b.refresh == "" {
		b.mu.Unlock()
		return "", errors.New("Autorize o envio ao Drive usando a mesma conta Google conectada ao YouTube")
	}
	if b.driveAccess != "" && time.Until(b.driveAccessUntil) > 2*time.Minute {
		a := b.driveAccess
		b.mu.Unlock()
		return a, nil
	}
	refresh := b.driveRefresh
	b.mu.Unlock()
	vals := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {b.webID}, "client_secret": {b.webSecret}}
	t, err := exchangeGoogle(context.Background(), vals)
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if t.RefreshToken != "" && t.RefreshToken != b.driveRefresh {
		p, err := protectSecret(t.RefreshToken)
		if err != nil {
			return "", err
		}
		old := b.saved.DriveRefreshDPAPI
		b.saved.DriveRefreshDPAPI = base64.StdEncoding.EncodeToString(p)
		if err = b.saveLocked(); err != nil {
			b.saved.DriveRefreshDPAPI = old
			return "", err
		}
		b.driveRefresh = t.RefreshToken
	}
	b.driveAccess = t.AccessToken
	if t.ExpiresIn < 180 {
		t.ExpiresIn = 3600
	}
	b.driveAccessUntil = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	return b.driveAccess, nil
}
func (b *Backend) driveVerify(w http.ResponseWriter, r *http.Request) {
	t, err := b.driveAccessToken()
	if err != nil {
		fail(w, 403, err.Error())
		return
	}
	id, err := googleDriveAccount(r.Context(), t)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	b.mu.Lock()
	ok := sameGoogleAccount(b.saved.YouTubeAccountID, id)
	b.mu.Unlock()
	if !ok {
		fail(w, 403, "O Drive pertence a outra conta Google. Reconecte com a mesma conta do YouTube")
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Drive verificado"})
}
func (b *Backend) localDriveAccess(w http.ResponseWriter, r *http.Request) {
	t, err := b.driveAccessToken()
	if err != nil {
		fail(w, 403, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": t, "expires_in": 3000})
}
func (b *Backend) disconnectDrive(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "Use POST")
		return
	}
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	old, oldRefresh, oldAccess, oldUntil := b.saved, b.driveRefresh, b.driveAccess, b.driveAccessUntil
	b.saved.DriveRefreshDPAPI, b.saved.DriveAccountID = "", ""
	b.driveRefresh, b.driveAccess, b.driveAccessUntil = "", "", time.Time{}
	if err := b.saveLocked(); err != nil {
		b.saved, b.driveRefresh, b.driveAccess, b.driveAccessUntil = old, oldRefresh, oldAccess, oldUntil
		fail(w, 500, "Falha ao salvar desconexão Drive")
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Drive desconectado; YouTube e GitHub continuam como antes."})
}

func (b *Backend) verify(w http.ResponseWriter, r *http.Request) {
	t, e := b.accessToken()
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	id, _, e := channelForToken(r.Context(), t)
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	b.mu.Lock()
	same := id == b.saved.ChannelID
	b.mu.Unlock()
	if !same {
		fail(w, 403, "Canal Google diferente do autorizado")
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "verificado"})
}

// Endpoint exclusivamente local/admin para o módulo de pesquisa do Desktop (access token efêmero).
func (b *Backend) localAccess(w http.ResponseWriter, r *http.Request) {
	t, e := b.accessToken()
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": t, "expires_in": 3000})
}
func (b *Backend) disconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "Use POST")
		return
	}
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	b.mu.Lock()
	b.refresh = ""
	b.access = ""
	b.accessUntil = time.Time{}
	b.saved = Saved{RecentUploads: b.saved.RecentUploads}
	b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil = "", "", time.Time{}
	b.driveRefresh, b.driveAccess, b.driveAccessUntil = "", "", time.Time{}
	e := b.saveLocked()
	b.mu.Unlock()
	if e != nil {
		fail(w, 500, "Erro ao salvar desconexão")
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Google desconectado do laboratório. Repositórios remotos não foram alterados."})
}
func (b *Backend) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "Use POST")
		return
	}
	var input struct {
		Repo      string `json:"repo"`
		RepoID    int64  `json:"repo_id"`
		Branch    string `json:"branch"`
		ChannelID string `json:"channel_id"`
	}
	if e := readJSON(r, &input); e != nil {
		fail(w, 400, "Registro inválido")
		return
	}
	if !repoNamePattern.MatchString(input.Repo) || input.RepoID <= 0 || !branchPattern.MatchString(input.Branch) {
		fail(w, 400, "Repositório de teste inválido")
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refresh == "" || input.ChannelID == "" || input.ChannelID != b.saved.ChannelID {
		fail(w, 403, "A conta Google atual não corresponde ao canal")
		return
	}
	if b.publicURL == "" {
		fail(w, 400, "Inicie o backend com LOOPINGBR_PUBLIC_URL para o GitHub Actions")
		return
	}
	if b.saved.Repo.ID != 0 && (b.saved.Repo.ID != input.RepoID || b.saved.Repo.FullName != input.Repo) {
		fail(w, 409, "Este laboratório já está vinculado a outro repositório. Não substitua automaticamente.")
		return
	}
	b.saved.Repo = RepoInfo{FullName: input.Repo, ID: input.RepoID, Branch: input.Branch, ChannelID: input.ChannelID}
	if e := b.saveLocked(); e != nil {
		fail(w, 500, "Falha ao salvar vínculo")
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Repositório experimental vinculado com sucesso"})
}
func (b *Backend) serve() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("/oauth/google/callback", b.callbackGoogle)
	mux.HandleFunc("/api/local/status", b.admin(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			fail(w, 405, "Use GET")
			return
		}
		writeJSON(w, 200, b.status())
	}))
	mux.HandleFunc("/api/local/start", b.admin(b.startGoogle))
	mux.HandleFunc("/api/local/drive/start", b.admin(b.startDrive))
	mux.HandleFunc("/api/local/analytics/start", b.admin(b.startAnalytics))
	mux.HandleFunc("/api/local/analytics/report", b.admin(b.analyticsReport))
	mux.HandleFunc("/api/local/analytics/disconnect", b.admin(b.disconnectAnalytics))
	mux.HandleFunc("/api/local/drive/access", b.admin(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			fail(w, 405, "Use GET")
			return
		}
		b.localDriveAccess(w, r)
	}))
	mux.HandleFunc("/api/local/drive/verify", b.admin(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			fail(w, 405, "Use GET")
			return
		}
		b.driveVerify(w, r)
	}))
	mux.HandleFunc("/api/local/drive/disconnect", b.admin(b.disconnectDrive))
	mux.HandleFunc("/api/local/drive/uploads", b.admin(b.listRecentUploads))
	mux.HandleFunc("/api/local/drive/uploads/record", b.admin(b.recordRecentUpload))
	mux.HandleFunc("/api/local/verify", b.admin(b.verify))
	mux.HandleFunc("/api/local/access", b.admin(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			fail(w, 405, "Use GET")
			return
		}
		b.localAccess(w, r)
	}))
	mux.HandleFunc("/api/local/disconnect", b.admin(b.disconnect))
	mux.HandleFunc("/api/local/register", b.admin(b.register))
	mux.HandleFunc("/api/runner/token", b.runnerToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func main() {
	b, e := newBackend()
	if e != nil {
		log.Fatal(e)
	}
	l, e := netListen()
	if e != nil {
		log.Fatal(e)
	}
	log.Println("LoopingBR Hybrid Lab backend iniciado em http://" + backendAddr)
	log.Println("OAuth Web Google callback: " + redirectURI)
	if b.publicURL == "" {
		log.Println("AVISO: sem túnel HTTPS: o GitHub Actions ainda não poderá solicitar tokens")
	}
	log.Fatal(http.Serve(l, b.serve()))
}
