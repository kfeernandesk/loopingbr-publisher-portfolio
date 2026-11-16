package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed assets/*
var assets embed.FS

//go:embed index.html
var page string

const githubBase = "https://api.github.com"
const githubOAuth = "https://github.com/login"
const googleOAuth = "https://accounts.google.com/o/oauth2/v2/auth"
const googleToken = "https://oauth2.googleapis.com/token"
const githubAPIVersion = "2022-11-28"

// IDs públicos dos apps oficiais do LoopingBR; fornecidos pelo desenvolvedor.
const builtInGitHubClientID = "Ov23liX6cVLuvwL8haOG"
const builtInGoogleClientID = "1071903015150-2i4ujdb2c5m34tp48mjis41mjlkdeori.apps.googleusercontent.com"

// O Client Secret Google Web fica exclusivamente no processo backend local.

var httpClient = &http.Client{Timeout: 35 * time.Second}
var drivePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,100}$`)
var repoPattern = regexp.MustCompile(`^loopingbr-desktop-[a-f0-9]{12}$`)

// Os tokens locais de GitHub e ChatGPT usam DPAPI no Windows.
// O Refresh Token Google permanece no backend local, nunca no repositório GitHub.
type Config struct {
	GitHubClientID    string `json:"github_client_id"`
	GoogleClientID    string `json:"google_client_id"`
	Repo              string `json:"repo"`
	Branch            string `json:"branch"`
	GHDPAPI           string `json:"github_dpapi,omitempty"`
	Ready             bool   `json:"ready"`
	SavedChannelID    string `json:"saved_channel_id"`
	SavedChannelName  string `json:"saved_channel_name"`
	GoogleChannelID   string `json:"google_channel_id,omitempty"`
	GoogleChannelName string `json:"google_channel_name,omitempty"`
	ChatGPTHostID     string `json:"chatgpt_host_id,omitempty"`
	ChatGPTClientID   string `json:"chatgpt_client_id,omitempty"`
	ChatGPTSubject    string `json:"chatgpt_subject,omitempty"`
	ChatGPTEmail      string `json:"chatgpt_email,omitempty"`
	ChatGPTDPAPI      string `json:"chatgpt_dpapi,omitempty"`
	StudioPluginURL   string `json:"studio_plugin_url,omitempty"`
}
type App struct {
	sync.Mutex
	cfg                Config
	ghToken            string
	ghLogin            string
	ghState            string
	ghDeviceCode       string
	ghCode             string
	ghVerifyURL        string
	googleState        string
	googleVerifier     string
	googleSecret       string
	googleAccess       string
	googleRefresh      string
	driveConnected     bool
	analyticsConnected bool
	googleExpiry       time.Time
	googleRefreshMu    sync.Mutex
	channelID          string
	channelName        string
	csrf               string
	address            string
	busy               bool
	lastRequest        string
	chatGPT            ChatGPTTokens
	chatGPTRefreshMu   sync.Mutex
	chatGPTPending     ChatGPTPending
	ghChecked          string
	googleChecked      string
	chatgptChecked     string
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func loadConfig() Config {
	var c Config
	if data, err := os.ReadFile(cfgPath()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	// Perfil independente. Nenhum Client Secret do Google é distribuído no executável.
	c.GitHubClientID = builtInGitHubClientID
	c.GoogleClientID = "Google Web OAuth via backend local"
	return c
}
func cfgPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "LoopingBRHybridV1014Desktop", "config.json")
}
func (a *App) saveConfig() error {
	a.Lock()
	c := a.cfg
	a.Unlock()
	p := cfgPath()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0600)
}
func apiErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
func writeJSON(w http.ResponseWriter, status int, obj any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(obj)
}
func readJSON(r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 60*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
func getJSON(urlStr, token string, out any) (int, error) {
	return reqJSON(http.MethodGet, urlStr, token, nil, out)
}
func reqJSON(method, urlStr, token string, body any, out any) (int, error) {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, urlStr, payload)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		var data struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &data)
		if data.Message == "" {
			data.Message = resp.Status
		}
		return resp.StatusCode, fmt.Errorf("API HTTP %d: %s", resp.StatusCode, data.Message)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
func postForm(endpoint string, v url.Values, out any) error {
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d ao autenticar; verifique a configuração OAuth", resp.StatusCode)
	}
	return json.Unmarshal(b, out)
}

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	_ = a.syncBackendStatus()
	a.Lock()
	defer a.Unlock()
	writeJSON(w, 200, map[string]any{
		"github_connected": a.ghToken != "", "github_login": a.ghLogin, "github_state": a.ghState,
		"github_code": a.ghCode, "github_verify_url": a.ghVerifyURL,
		"google_connected": a.googleRefresh != "", "drive_connected": a.driveConnected, "analytics_connected": a.analyticsConnected, "google_prepared": a.cfg.Ready, "google_state": a.googleState,
		"channel_id": func() string {
			if a.channelID != "" {
				return a.channelID
			}
			return a.cfg.SavedChannelID
		}(), "channel_name": func() string {
			if a.channelName != "" {
				return a.channelName
			}
			return a.cfg.SavedChannelName
		}(),
		"repo": a.cfg.Repo, "branch": a.cfg.Branch, "busy": a.busy, "last_request": a.lastRequest,
		"configured": a.cfg.GitHubClientID != "" && a.cfg.GoogleClientID != "" && a.googleSecret != "",
		"ready":      a.cfg.Ready, "google_auth_ready": a.googleRefresh != "",
		"gh_client_hint": a.cfg.GitHubClientID != "", "google_client_hint": a.cfg.GoogleClientID != "",
		"developer_build_ready": a.googleSecret != "",
		"chatgpt_connected":     a.chatGPT.AccessToken != "" || a.chatGPT.RefreshToken != "",
		"chatgpt_plan_enabled":  a.chatGPT.ScopesAllowPlan(),
		"studio_plugin_url":     a.cfg.StudioPluginURL,
		"chatgpt_email":         a.cfg.ChatGPTEmail,
		"github_check":          a.ghChecked, "google_check": a.googleChecked, "chatgpt_check": a.chatgptChecked,
	})
}
func (a *App) githubStart(w http.ResponseWriter, r *http.Request) {
	a.Lock()
	clientID := a.cfg.GitHubClientID
	a.Unlock()
	if clientID == "" {
		apiErr(w, 400, "Configure o Client ID do OAuth App do GitHub.")
		return
	}
	v := url.Values{"client_id": {clientID}, "scope": {"repo workflow read:user"}}
	var reply struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
		Error           string `json:"error"`
	}
	if err := postForm(githubOAuth+"/device/code", v, &reply); err != nil {
		apiErr(w, 502, err.Error())
		return
	}
	if reply.DeviceCode == "" {
		apiErr(w, 502, "O GitHub não retornou código. Habilite Device Flow no OAuth App.")
		return
	}
	a.Lock()
	a.ghState = "Aguardando autorização no GitHub"
	a.ghDeviceCode = reply.DeviceCode
	a.ghCode = reply.UserCode
	a.ghVerifyURL = reply.VerificationURI
	a.Unlock()
	if reply.Interval < 5 {
		reply.Interval = 5
	}
	if reply.ExpiresIn <= 0 {
		reply.ExpiresIn = 900
	}
	go a.pollGitHub(clientID, reply.DeviceCode, reply.Interval, time.Now().Add(time.Duration(reply.ExpiresIn)*time.Second))
	writeJSON(w, 200, map[string]string{"user_code": reply.UserCode, "verification_uri": reply.VerificationURI})
}
func (a *App) pollGitHub(clientID, deviceCode string, interval int, expires time.Time) {
	for time.Now().Before(expires) {
		time.Sleep(time.Duration(interval) * time.Second)
		a.Lock()
		valid := a.ghDeviceCode == deviceCode
		a.Unlock()
		if !valid {
			return
		}
		values := url.Values{"client_id": {clientID}, "device_code": {deviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
		var o struct {
			AccessToken      string `json:"access_token"`
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := postForm(githubOAuth+"/oauth/access_token", values, &o); err != nil {
			a.Lock()
			a.ghState = err.Error()
			a.Unlock()
			return
		}
		if o.AccessToken != "" {
			var me struct {
				Login string `json:"login"`
			}
			_, err := getJSON(githubBase+"/user", o.AccessToken, &me)
			if err != nil {
				a.Lock()
				a.ghState = "GitHub autorizado, mas não foi possível verificar a conta."
				a.Unlock()
				return
			}
			a.Lock()
			a.ghToken = o.AccessToken
			a.ghChecked = "verificado"
			a.ghLogin = me.Login
			a.ghState = "GitHub conectado"
			// Ao mudar de dono, não reutiliza repositório de outra conta.
			if a.cfg.Repo != "" && !strings.HasPrefix(strings.ToLower(a.cfg.Repo), strings.ToLower(me.Login)+"/") {
				a.cfg.Repo = ""
				a.cfg.Branch = ""
				a.cfg.Ready = false
			}
			a.ghDeviceCode = ""
			a.ghCode = ""
			a.ghVerifyURL = ""
			sealed, sealErr := protectSecret(o.AccessToken)
			if sealErr == nil {
				a.cfg.GHDPAPI = base64.StdEncoding.EncodeToString(sealed)
			}
			a.Unlock()
			_ = a.saveConfig()
			return
		}
		switch o.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		default:
			a.Lock()
			a.ghState = "Autorização GitHub não concluída: " + o.Error
			a.ghDeviceCode = ""
			a.Unlock()
			return
		}
	}
	a.Lock()
	a.ghState = "Código GitHub expirado. Inicie uma nova conexão."
	a.ghDeviceCode = ""
	a.Unlock()
}
func (a *App) googleStart(w http.ResponseWriter, r *http.Request) {
	var reply struct {
		URL string `json:"url"`
	}
	if err := backendCall("POST", "/api/local/start", map[string]any{}, &reply); err != nil {
		apiErr(w, 503, "Backend local: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"url": reply.URL})
}
func (a *App) driveStart(w http.ResponseWriter, r *http.Request) {
	var reply struct {
		URL string `json:"url"`
	}
	if err := backendCall("POST", "/api/local/drive/start", map[string]any{}, &reply); err != nil {
		apiErr(w, 503, "Backend Drive: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"url": reply.URL})
}
func (a *App) driveDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := backendCall("POST", "/api/local/drive/disconnect", map[string]any{}, nil); err != nil {
		apiErr(w, 503, "Backend Drive: "+err.Error())
		return
	}
	_ = a.syncBackendStatus()
	writeJSON(w, 200, map[string]string{"ok": "Envio ao Drive desconectado. O YouTube e o GitHub continuam autorizados."})
}
func (a *App) ghCall(method, resource string, body any, out any) (int, error) {
	a.Lock()
	token := a.ghToken
	a.Unlock()
	if token == "" {
		return 0, errors.New("Conecte o GitHub antes.")
	}
	code, err := reqJSON(method, githubBase+resource, token, body, out)
	if code == 401 || code == 403 {
		a.Lock()
		a.ghChecked = "erro: solicitação GitHub não autorizada (token, permissões ou limite da API)"
		a.Unlock()
	}
	return code, err
}
func (a *App) busyStart() bool {
	a.Lock()
	defer a.Unlock()
	if a.busy {
		return false
	}
	a.busy = true
	return true
}
func (a *App) busyStop() { a.Lock(); a.busy = false; a.Unlock() }

func (a *App) provision(w http.ResponseWriter, r *http.Request) {
	if !a.busyStart() {
		apiErr(w, 409, "Outra operação está sendo executada.")
		return
	}
	defer a.busyStop()
	a.Lock()
	repo := a.cfg.Repo
	branch := a.cfg.Branch
	ghLogin := a.ghLogin
	ghToken := a.ghToken
	backendConnected := a.googleRefresh != ""
	channel := a.channelID
	a.Unlock()
	if ghToken == "" || !backendConnected || channel == "" {
		apiErr(w, 400, "Inicie o backend e conecte GitHub, Google e YouTube antes de preparar o Publisher.")
		return
	}
	// Não altere Secrets/repositório com uma identidade de canal não verificada.
	if err := a.verifyGoogleChannel(); err != nil {
		apiErr(w, 403, "Não foi possível confirmar o canal Google/YouTube: "+err.Error())
		return
	}
	if repo == "" {
		raw := make([]byte, 6)
		_, _ = rand.Read(raw)
		name := fmt.Sprintf("loopingbr-desktop-%x", raw)
		var created struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
		}
		_, err := a.ghCall("POST", "/user/repos", map[string]any{"name": name, "description": "LoopingBR Publisher Desktop (repositório individual)", "private": true, "auto_init": true}, &created)
		if err != nil {
			apiErr(w, 502, "Não foi possível criar repositório privado: "+err.Error())
			return
		}
		if !created.Private || created.FullName == "" {
			apiErr(w, 502, "Repositório criado com propriedades inesperadas. Verifique sua conta antes de prosseguir.")
			return
		}
		repo = created.FullName
		branch = created.DefaultBranch
		if branch == "" {
			branch = "main"
		}
		a.Lock()
		a.cfg.Repo = repo
		a.cfg.Branch = branch
		a.Unlock()
		_ = a.saveConfig()
	}
	if err := validateOwnedRepo(repo, ghLogin); err != nil {
		apiErr(w, 403, err.Error())
		return
	}
	// Repo files are installed on a new private repo only; original repo never touched.
	if err := a.installEngine(repo, branch); err != nil {
		apiErr(w, 502, "Repositório criado, mas a instalação dos arquivos falhou. Tente 'Preparar' outra vez: "+err.Error())
		return
	}
	brokerURL, err := backendBrokerURL()
	if err != nil {
		apiErr(w, 503, "Configure o túnel HTTPS do backend: "+err.Error())
		return
	}
	secrets := map[string]string{"LOOPINGBR_BROKER_URL": brokerURL, "YOUTUBE_CHANNEL_ID": channel}
	if err := a.installSecrets(repo, secrets); err != nil {
		apiErr(w, 502, "Arquivos instalados, mas a configuração do Actions falhou. Tente novamente: "+err.Error())
		return
	}
	var repoInfo struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
	}
	if _, err := a.ghCall("GET", "/repos/"+repo, nil, &repoInfo); err != nil || repoInfo.ID < 1 || repoInfo.FullName != repo || !repoInfo.Private {
		apiErr(w, 502, "Não foi possível confirmar o ID e a privacidade do repositório de testes. Nenhum vínculo com backend foi criado.")
		return
	}
	if err := backendCall("POST", "/api/local/register", map[string]any{"repo": repo, "repo_id": repoInfo.ID, "branch": branch, "channel_id": channel}, nil); err != nil {
		apiErr(w, 502, "Repositório criado, mas não foi possível vincular ao backend seguro: "+err.Error())
		return
	}
	a.Lock()
	a.cfg.Ready = true
	a.cfg.SavedChannelID = channel
	a.cfg.SavedChannelName = a.channelName
	a.Unlock()
	_ = a.saveConfig()
	writeJSON(w, 200, map[string]string{"ok": "Repositório experimental criado. Workflow OIDC vinculado ao backend sem segredos permanentes do Google.", "repo": repo, "url": "https://github.com/" + repo + "/actions"})
}
func validateOwnedRepo(full, owner string) error {
	parts := strings.Split(full, "/")
	if len(parts) != 2 || !strings.EqualFold(parts[0], owner) || !repoPattern.MatchString(parts[1]) {
		return errors.New("Segurança: o programa só pode modificar seu próprio repositório privado loopingbr-desktop-*. O Publisher original está protegido.")
	}
	return nil
}
func (a *App) installEngine(repo, branch string) error {
	// Retry-safe: if publisher.py already exists, do not overwrite remote files.
	var check any
	status, err := a.ghCall("GET", "/repos/"+repo+"/contents/publisher.py", nil, &check)
	if status == 200 && err == nil {
		return nil
	}
	if status != 404 {
		return fmt.Errorf("não foi possível consultar o estado do repositório: %w", err)
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	_, err = a.ghCall("GET", "/repos/"+repo+"/git/ref/heads/"+url.PathEscape(branch), nil, &ref)
	if err != nil {
		return err
	}
	if ref.Object.SHA == "" {
		return errors.New("Branch principal sem commit.")
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	_, err = a.ghCall("GET", "/repos/"+repo+"/git/commits/"+ref.Object.SHA, nil, &commit)
	if err != nil {
		return err
	}
	files := map[string]string{}
	for _, entry := range []struct{ From, To string }{
		{"assets/publisher.py", "publisher.py"}, {"assets/workflow.yml", ".github/workflows/publicar.yml"},
		{"assets/requirements.txt", "requirements.txt"}, {"assets/about.md", "README.md"},
	} {
		content, err := assets.ReadFile(entry.From)
		if err != nil {
			return err
		}
		value := string(content)
		if entry.From == "assets/workflow.yml" && branch != "main" {
			value = strings.Replace(value, "branches: [main]", "branches: ["+branch+"]", 1)
		}
		files[entry.To] = value
	}
	items := make([]map[string]string, 0, len(files))
	for path, body := range files {
		items = append(items, map[string]string{"path": path, "mode": "100644", "type": "blob", "content": body})
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	_, err = a.ghCall("POST", "/repos/"+repo+"/git/trees", map[string]any{"base_tree": commit.Tree.SHA, "tree": items}, &tree)
	if err != nil {
		return err
	}
	var newCommit struct {
		SHA string `json:"sha"`
	}
	_, err = a.ghCall("POST", "/repos/"+repo+"/git/commits", map[string]any{"message": "Instalar motor independente LoopingBR Desktop", "tree": tree.SHA, "parents": []string{ref.Object.SHA}}, &newCommit)
	if err != nil {
		return err
	}
	_, err = a.ghCall("PATCH", "/repos/"+repo+"/git/refs/heads/"+url.PathEscape(branch), map[string]any{"sha": newCommit.SHA, "force": false}, nil)
	return err
}
func (a *App) installSecrets(repo string, credentials map[string]string) error {
	var key struct {
		Key   string `json:"key"`
		KeyID string `json:"key_id"`
	}
	_, err := a.ghCall("GET", "/repos/"+repo+"/actions/secrets/public-key", nil, &key)
	if err != nil {
		return err
	}
	publicKey, err := base64.StdEncoding.DecodeString(key.Key)
	if err != nil {
		return err
	}
	for _, name := range []string{"YOUTUBE_CHANNEL_ID", "LOOPINGBR_BROKER_URL"} {
		if credentials[name] == "" {
			return fmt.Errorf("configuração ausente: %s", name)
		}
		sealed, err := sealForGitHub([]byte(credentials[name]), publicKey)
		if err != nil {
			return err
		}
		_, err = a.ghCall("PUT", "/repos/"+repo+"/actions/secrets/"+name,
			map[string]string{"encrypted_value": base64.StdEncoding.EncodeToString(sealed), "key_id": key.KeyID}, nil)
		if err != nil {
			return fmt.Errorf("não foi possível gravar %s: %w", name, err)
		}
	}
	return nil
}
func normalizeDriveID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "http") {
		u, err := url.Parse(s)
		if err != nil {
			return "", err
		}
		if u.Scheme != "https" || (u.Hostname() != "drive.google.com" && u.Hostname() != "docs.google.com") {
			return "", errors.New("Use apenas URLs HTTPS oficiais do Google Drive.")
		}
		components := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(components) >= 3 && components[0] == "file" && components[1] == "d" {
			s = components[2]
		} else {
			s = u.Query().Get("id")
		}
	}
	if !drivePattern.MatchString(s) {
		return "", errors.New("ID do arquivo do Drive inválido.")
	}
	return s, nil
}

type Job struct {
	VideoDriveID     string   `json:"video_drive_id"`
	ThumbnailDriveID string   `json:"thumbnail_drive_id"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Tags             []string `json:"tags,omitempty"`
	Visibility       string   `json:"visibility"`
	MadeForKids      bool     `json:"made_for_kids"`
	Tipo             string   `json:"tipo"`
	PublishAt        string   `json:"publish_at"`
	AllowRepublish   bool     `json:"allow_republish"`
	ChannelID        string   `json:"channel_id"`
	Confirm          string   `json:"confirm"`
	Confirmed        bool     `json:"confirmed"`
}

func (j *Job) valid(expected string) error {
	if !j.Confirmed || j.Confirm != "CONFIRMO_PUBLICAR" {
		return errors.New("Aprovação explícita ausente.")
	}
	if j.ChannelID != expected || expected == "" {
		return errors.New("Canal do pedido não corresponde à autorização atual.")
	}
	var err error
	j.VideoDriveID, err = normalizeDriveID(j.VideoDriveID)
	if err != nil {
		return err
	}
	if j.ThumbnailDriveID != "" {
		j.ThumbnailDriveID, err = normalizeDriveID(j.ThumbnailDriveID)
		if err != nil {
			return err
		}
	}
	if len([]rune(j.Title)) < 1 || len([]rune(j.Title)) > 100 {
		return errors.New("Título precisa ter entre 1 e 100 caracteres.")
	}
	if len([]byte(j.Description)) > 5000 {
		return errors.New("Descrição excede 5.000 bytes.")
	}
	if len(j.Tags) > 25 {
		return errors.New("Use no máximo 25 tags.")
	}
	seen := map[string]bool{}
	totalTagBytes := 0
	for i, tag := range j.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || strings.Contains(tag, ",") || len([]rune(tag)) > 60 {
			return errors.New("Tag inválida. Separe por vírgula e use até 60 caracteres por tag.")
		}
		totalTagBytes += len([]byte(tag))
		if seen[strings.ToLower(tag)] {
			return errors.New("Tags duplicadas não são necessárias.")
		}
		seen[strings.ToLower(tag)] = true
		j.Tags[i] = tag
	}
	if totalTagBytes > 440 {
		return errors.New("As tags ultrapassam o limite recomendado.")
	}
	if j.Visibility != "unlisted" && j.Visibility != "private" && j.Visibility != "public" {
		return errors.New("Visibilidade inválida.")
	}
	if j.Tipo != "auto" && j.Tipo != "video" && j.Tipo != "shorts" {
		return errors.New("Tipo inválido.")
	}
	if j.PublishAt != "" {
		if j.Visibility != "public" {
			return errors.New("Agendamento do YouTube exige visibilidade pública.")
		}
		t, err := time.Parse(time.RFC3339, j.PublishAt)
		if err != nil {
			return errors.New("Agendamento deve conter data, hora e fuso ISO 8601.")
		}
		if t.Before(time.Now().Add(10 * time.Minute)) {
			return errors.New("Agendamento precisa de pelo menos 10 minutos de antecedência.")
		}
	}
	return nil
}
func (a *App) publish(w http.ResponseWriter, r *http.Request) {
	if !a.busyStart() {
		apiErr(w, 409, "Outra operação está em andamento.")
		return
	}
	defer a.busyStop()
	var req Job
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "Dados do pedido inválidos.")
		return
	}
	a.Lock()
	repo := a.cfg.Repo
	branch := a.cfg.Branch
	login := a.ghLogin
	chanID := a.channelID
	if chanID == "" && a.cfg.Ready {
		chanID = a.cfg.SavedChannelID
	}
	ready := a.cfg.Ready
	a.Unlock()
	if !ready {
		apiErr(w, 403, "Finalize primeiro a vinculação do GitHub Actions com o backend.")
		return
	}
	if err := a.verifyGoogleChannel(); err != nil {
		apiErr(w, 503, "Backend/Google indisponível: "+err.Error())
		return
	}
	a.Lock()
	ready = a.cfg.Ready
	a.Unlock()
	if !ready {
		apiErr(w, 403, "O repositório não está vinculado ao backend. Prepare-o novamente.")
		return
	}

	if err := validateOwnedRepo(repo, login); err != nil {
		apiErr(w, 403, err.Error())
		return
	}
	if err := req.valid(chanID); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	// Re-check older approved requests before the commit. Runner checks again.
	var files []struct {
		Path string `json:"path"`
	}
	_, err := a.ghCall("GET", "/repos/"+repo+"/contents/publicacoes/aprovadas", nil, &files)
	if err != nil && !strings.Contains(err.Error(), "HTTP 404") {
		apiErr(w, 502, "Não foi possível consultar publicações anteriores: "+err.Error())
		return
	}
	if !req.AllowRepublish {
		for _, f := range files {
			var o struct {
				Content  string `json:"content"`
				Encoding string `json:"encoding"`
			}
			_, err := a.ghCall("GET", "/repos/"+repo+"/contents/"+f.Path, nil, &o)
			if err != nil {
				apiErr(w, 502, "Não foi possível verificar pedidos anteriores.")
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(o.Content, "\n", ""))
			if err != nil {
				apiErr(w, 502, "Pedido anterior ilegível.")
				return
			}
			var old Job
			if json.Unmarshal(decoded, &old) != nil {
				apiErr(w, 502, "Pedido anterior inválido.")
				return
			}
			id, err := normalizeDriveID(old.VideoDriveID)
			if err == nil && id == req.VideoDriveID {
				apiErr(w, 409, "Já existe um pedido para esse arquivo do Drive. Se realmente deseja reenviar, marque 'Permitir republicação' depois de conferir o YouTube Studio.")
				return
			}
		}
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	name := fmt.Sprintf("%s_%x", stamp, raw)
	payload := map[string]any{"video_drive_id": req.VideoDriveID, "thumbnail_drive_id": req.ThumbnailDriveID, "title": req.Title,
		"description": req.Description, "tags": req.Tags, "visibility": req.Visibility, "made_for_kids": req.MadeForKids, "tipo": req.Tipo,
		"publish_at": req.PublishAt, "allow_republish": req.AllowRepublish, "channel_id": req.ChannelID, "confirm": "CONFIRMO_PUBLICAR"}
	encoded, _ := json.MarshalIndent(payload, "", "  ")
	path := "publicacoes/aprovadas/" + name + ".json"
	_, err = a.ghCall("PUT", "/repos/"+repo+"/contents/"+path,
		map[string]string{"message": "Publicação confirmada pelo usuário: " + name, "content": base64.StdEncoding.EncodeToString(encoded), "branch": branch}, nil)
	if err != nil {
		apiErr(w, 502, "Não foi possível enviar pedido ao GitHub: "+err.Error())
		return
	}
	a.Lock()
	a.lastRequest = name
	a.Unlock()
	writeJSON(w, 200, map[string]string{"ok": "Pedido enviado ao GitHub Actions; publicação ainda não confirmada pelo YouTube.", "request": name,
		"repo_url": "https://github.com/" + repo + "/actions", "result_path": "publicacoes/resultados/" + name + ".json"})
}

// A atualização de tags é opt-in e aceita apenas o motor exato da v1.0.8.
// Nunca sobrescreve arquivos do usuário nem modifica outros repositórios.
const legacyPublisherSHA256 = "193a90ad2667bff402b2cd4531b247bcebd4afa3a43b40ce78b674f249a5c292"

type publisherRemoteFile struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	SHA      string `json:"sha"`
}

func engineStateForData(remote, current []byte) string {
	if bytes.Equal(remote, current) {
		return "current"
	}
	h := fmt.Sprintf("%x", sha256.Sum256(remote))
	if h == legacyPublisherSHA256 {
		return "legacy"
	}
	return "unknown"
}
func (a *App) getEngineStatus() (string, string, error) {
	a.Lock()
	repo, branch, login, ready := a.cfg.Repo, a.cfg.Branch, a.ghLogin, a.cfg.Ready
	a.Unlock()
	if !ready {
		return "", "", errors.New("Prepare primeiro seu GitHub Actions.")
	}
	if err := validateOwnedRepo(repo, login); err != nil {
		return "", "", err
	}
	if branch == "" {
		return "", "", errors.New("Branch de publicação não configurada.")
	}
	var remote publisherRemoteFile
	if _, err := a.ghCall("GET", "/repos/"+repo+"/contents/publisher.py?ref="+url.QueryEscape(branch), nil, &remote); err != nil {
		return "", "", err
	}
	if remote.Encoding != "base64" || remote.SHA == "" {
		return "", "", errors.New("Arquivo remoto do motor não pôde ser validado.")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(remote.Content, "\n", ""))
	if err != nil {
		return "", "", err
	}
	current, err := assets.ReadFile("assets/publisher.py")
	if err != nil {
		return "", "", err
	}
	return engineStateForData(raw, current), remote.SHA, nil
}
func (a *App) engineStatus(w http.ResponseWriter, r *http.Request) {
	state, _, err := a.getEngineStatus()
	if err != nil {
		apiErr(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"state": state})
}
func (a *App) engineUpgrade(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirm string `json:"confirm"`
	}
	if readJSON(r, &input) != nil || input.Confirm != "ATUALIZAR_MOTOR_TAGS" {
		apiErr(w, 400, "Confirmação explícita ausente.")
		return
	}
	if !a.busyStart() {
		apiErr(w, 409, "Outra operação está em andamento.")
		return
	}
	defer a.busyStop()
	state, sha, err := a.getEngineStatus()
	if err != nil {
		apiErr(w, 503, err.Error())
		return
	}
	if state == "current" {
		writeJSON(w, 200, map[string]string{"ok": "Motor já atualizado."})
		return
	}
	if state != "legacy" {
		apiErr(w, 409, "O motor foi alterado ou é de outra versão. Nenhum arquivo foi modificado.")
		return
	}
	a.Lock()
	repo, branch := a.cfg.Repo, a.cfg.Branch
	a.Unlock()
	code, err := assets.ReadFile("assets/publisher.py")
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	_, err = a.ghCall("PUT", "/repos/"+repo+"/contents/publisher.py", map[string]string{"message": "Atualizar motor LoopingBR para tags (autorizado no Desktop)", "content": base64.StdEncoding.EncodeToString(code), "sha": sha, "branch": branch}, nil)
	if err != nil {
		apiErr(w, 502, "O GitHub recusou a atualização. Nada será repetido automaticamente: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "Motor atualizado. O recurso de tags está disponível."})
}

func (a *App) latestResult(w http.ResponseWriter, r *http.Request) {
	a.Lock()
	repo := a.cfg.Repo
	name := a.lastRequest
	login := a.ghLogin
	a.Unlock()
	if err := validateOwnedRepo(repo, login); err != nil {
		apiErr(w, 400, "Publisher não preparado.")
		return
	}
	if !regexp.MustCompile(`^[0-9T_]{15,19}[a-f0-9]{16}$`).MatchString(name) {
		apiErr(w, 404, "Nenhum pedido criado nesta sessão.")
		return
	}
	var data struct {
		Content string `json:"content"`
	}
	_, err := a.ghCall("GET", "/repos/"+repo+"/contents/publicacoes/resultados/"+name+".json", nil, &data)
	if err != nil {
		apiErr(w, 404, "Sem resultado registrado ainda. Confira GitHub Actions.")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(data.Content, "\n", ""))
	if err != nil {
		apiErr(w, 500, "Resultado ilegível.")
		return
	}
	var reply map[string]any
	if json.Unmarshal(raw, &reply) != nil {
		apiErr(w, 500, "Resultado inválido.")
		return
	}
	writeJSON(w, 200, reply)
}
func openBrowser(target string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
func (a *App) exitApp(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"ok": "Encerrando o LoopingBR Publisher"})
	go func() { time.Sleep(500 * time.Millisecond); os.Exit(0) }()
}
func main() {
	app := &App{cfg: loadConfig(), csrf: randToken(24)}
	app.restoreChatGPT()
	_ = app.syncBackendStatus()
	// Grava somente a nova versão: arquivos e credenciais antigos permanecem intactos.
	if err := app.saveConfig(); err != nil {
		log.Printf("Não foi possível salvar o perfil do laboratório híbrido: %v", err)
	}
	// Não importar conversas nem credenciais antigas automaticamente para o laboratório.
	if app.cfg.GHDPAPI != "" {
		if data, err := base64.StdEncoding.DecodeString(app.cfg.GHDPAPI); err == nil {
			if unsealed, err := unprotectSecret(data); err == nil {
				app.ghToken = string(unsealed)
				var me struct {
					Login string `json:"login"`
				}
				if _, err := getJSON(githubBase+"/user", app.ghToken, &me); err == nil && me.Login != "" {
					app.ghLogin = me.Login
					app.ghChecked = "verificado"
					app.ghState = "GitHub conectado"
				} else {
					// Falha de rede não apaga a credencial local.
					app.ghChecked = "erro: autorização GitHub não verificada; confira a conexão"
					app.ghState = "GitHub exige verificação"
				}
			}
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	app.address = listener.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' https://i.ytimg.com; connect-src 'self'; base-uri 'none'; form-action 'self'")
		html := strings.ReplaceAll(page, "__CSRF_PLACEHOLDER__", app.csrf)
		_, _ = io.WriteString(w, html)
	})
	mux.HandleFunc("/auth/callback", app.chatGPTCallback)
	endpoints := map[string]func(http.ResponseWriter, *http.Request){
		"/api/status": app.status, "/api/github/start": app.githubStart,
		"/api/google/start": app.googleStart, "/api/drive/connect": app.driveStart, "/api/drive/disconnect": app.driveDisconnect, "/api/provision": app.provision,
		"/api/engine/status": app.engineStatus, "/api/engine/upgrade": app.engineUpgrade, "/api/publish": app.publish, "/api/publications": app.publications, "/api/result": app.latestResult, "/api/exit": app.exitApp,
		"/api/analytics/start": app.analyticsStart, "/api/analytics/report": app.analyticsReport, "/api/analytics/interpret": app.analyticsInterpret, "/api/analytics/disconnect": app.analyticsDisconnect,
		"/api/chatgpt/start": app.chatGPTStart, "/api/chatgpt/models": app.chatGPTModels,
		"/api/chatgpt/chat": app.chatGPTChat, "/api/chatgpt/disconnect": app.chatGPTDisconnect,
		"/api/github/disconnect": app.githubDisconnect, "/api/google/disconnect": app.googleDisconnect,
		"/api/connections/check": app.checkConnections, "/api/youtube/research": app.youtubeResearchHandler,
		"/api/conversations": app.loadConversations, "/api/conversations/save": app.saveConversations,
		"/api/studio/prepare": app.studioPrepare, "/api/studio/reveal": app.studioReveal,
		"/api/studio/save-link": app.studioSaveLink, "/api/studio/forget-link": app.studioForgetLink,
		"/api/drive/uploads": app.recentDriveUploads, "/api/drive/quota": app.driveQuota, "/api/drive/progress": app.driveProgress, "/api/drive/upload": app.driveUpload,
	}
	for path, fn := range endpoints {
		fnLocal := fn
		pathLocal := path
		mux.HandleFunc(pathLocal, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" && r.Method != "POST" {
				apiErr(w, 405, "Método não permitido.")
				return
			}
			if r.Method == "POST" {
				origin := r.Header.Get("Origin")
				if origin != "" && origin != "http://"+app.address {
					apiErr(w, 403, "Origem não permitida.")
					return
				}
				if r.Header.Get("X-LoopingBR-CSRF") != app.csrf {
					apiErr(w, 403, "Solicitação sem autorização local.")
					return
				}
			}
			if r.Method == "GET" && pathLocal != "/api/status" && pathLocal != "/api/publications" && pathLocal != "/api/result" && pathLocal != "/api/chatgpt/models" && pathLocal != "/api/conversations" {
				apiErr(w, 405, "Método não permitido.")
				return
			}
			if r.Method == "POST" && (pathLocal == "/api/status" || pathLocal == "/api/publications" || pathLocal == "/api/result" || pathLocal == "/api/chatgpt/models" || pathLocal == "/api/conversations") {
				apiErr(w, 405, "Método não permitido.")
				return
			}
			fnLocal(w, r)
		})
	}
	if app.googleRefresh != "" {
		go func() {
			if err := app.verifyGoogleChannel(); err != nil {
				app.Lock()
				app.googleChecked = "erro: " + err.Error()
				app.Unlock()
			} else {
				app.Lock()
				app.googleChecked = "verificado"
				app.Unlock()
			}
		}()
	}
	address := "http://" + app.address
	fmt.Println("LoopingBR Publisher HYBRID LAB em", address)
	log.Println("Acesso restrito ao próprio computador. Feche o aplicativo para parar o painel.")
	go func() { time.Sleep(350 * time.Millisecond); _ = openBrowser(address) }()
	log.Fatal(http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != app.address {
			http.Error(w, "Host local inválido", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})))
}
