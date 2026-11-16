package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type historyTransport func(*http.Request) (*http.Response, error)

func (fn historyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func githubTestResponse(code int, value any) *http.Response {
	payload, _ := json.Marshal(value)
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(strings.NewReader(string(payload))), Header: make(http.Header)}
}
func encodedGithubRecord(value any) any {
	b, _ := json.Marshal(value)
	return map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(b)}
}

func TestPublicationHistoryReadOnlyAndScoped(t *testing.T) {
	repo := "tester/loopingbr-desktop-aabbccddeeff"
	latest := "20261113T191010_aaaaaaaaaaaaaaaa.json"
	old := "20261112T181010_bbbbbbbbbbbbbbbb.json"
	other := "20261111T181010_cccccccccccccccc.json"
	paths := map[string]any{}
	dir := "/repos/" + repo + "/contents/publicacoes/"
	paths[dir+"aprovadas"] = []map[string]string{
		{"name": old, "path": "publicacoes/aprovadas/" + old, "type": "file"},
		{"name": latest, "path": "publicacoes/aprovadas/" + latest, "type": "file"},
		{"name": other, "path": "publicacoes/aprovadas/" + other, "type": "file"},
		{"name": ".gitkeep", "path": "publicacoes/aprovadas/.gitkeep", "type": "file"},
	}
	paths[dir+"resultados"] = []map[string]string{{"name": latest, "path": "publicacoes/resultados/" + latest, "type": "file"}}
	paths[dir+"aprovadas/"+latest] = encodedGithubRecord(Job{Title: "Bloodborne", VideoDriveID: "1abcde234567890ABC", ChannelID: "channelA", Visibility: "unlisted", Tipo: "video"})
	paths[dir+"aprovadas/"+old] = encodedGithubRecord(Job{Title: "Metro 2033", VideoDriveID: "1abcde234567890DEF", ChannelID: "channelA", Visibility: "private", Tipo: "video"})
	paths[dir+"aprovadas/"+other] = encodedGithubRecord(Job{Title: "Vídeo de outra conta", VideoDriveID: "1abcde234567890GHI", ChannelID: "channelB", Visibility: "public", Tipo: "video"})
	paths[dir+"resultados/"+latest] = encodedGithubRecord(map[string]string{"status": "enviado", "youtube_video_id": "abc123XYZ_1"})
	count := 0
	prev := httpClient
	httpClient = &http.Client{Transport: historyTransport(func(req *http.Request) (*http.Response, error) {
		count++
		if req.Method != http.MethodGet {
			t.Errorf("Histórico tentou alterar GitHub: %s", req.Method)
		}
		if req.Host != "api.github.com" {
			t.Errorf("API não autorizada: %s", req.Host)
		}
		if value, ok := paths[req.URL.Path]; ok {
			return githubTestResponse(200, value), nil
		}
		return githubTestResponse(404, map[string]string{"message": "Not Found"}), nil
	})}
	defer func() { httpClient = prev }()
	app := &App{cfg: Config{Repo: repo, SavedChannelID: "channelA"}, ghToken: "fake-test", ghLogin: "tester", channelID: "channelA"}
	req := httptest.NewRequest("GET", "/api/publications?limit=12&page=1", nil)
	w := httptest.NewRecorder()
	app.publications(w, req)
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var res PublicationsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("Registros de outros canais devem ser excluídos, recebeu %d: %+v", len(res.Items), res.Items)
	}
	if res.Items[0].Title != "Bloodborne" || res.Items[0].State != "uploaded" || res.Items[0].YouTubeURL != "https://www.youtube.com/watch?v=abc123XYZ_1" {
		t.Fatalf("Upload real incorreto: %+v", res.Items[0])
	}
	if res.Items[1].State != "pending" || res.Items[1].YouTubeURL != "" {
		t.Fatalf("Pedido pendente exibido incorretamente: %+v", res.Items[1])
	}
	if count != 6 {
		t.Fatalf("Número inesperado de leituras: %d", count)
	}
}

func TestHistoryPreventsChangedChannelAndInvalidPage(t *testing.T) {
	app := &App{cfg: Config{Repo: "tester/loopingbr-desktop-aabbccddeeff", SavedChannelID: "channelA"}, ghToken: "fake", ghLogin: "tester", channelID: "channelB"}
	rec := httptest.NewRecorder()
	app.publications(rec, httptest.NewRequest("GET", "/api/publications", nil))
	if rec.Code != 403 {
		t.Fatalf("Canal trocado deveria ser bloqueado; HTTP %d", rec.Code)
	}
	app.channelID = "channelA"
	rec = httptest.NewRecorder()
	app.publications(rec, httptest.NewRequest("GET", "/api/publications?page=-1", nil))
	if rec.Code != 400 {
		t.Fatalf("Página inválida deveria ser recusada; HTTP %d", rec.Code)
	}
}

func TestHistoryNeverClaimsAmbiguousUploadSucceeded(t *testing.T) {
	for _, state := range []string{"interrompido_verificar_studio", "reservado_nao_reexecutar", "", "enviado", "agendado", "unknown"} {
		key, _, _ := classifyPublication(state, "")
		if key == "uploaded" || key == "scheduled" {
			t.Fatalf("Estado incerto tratado como sucesso: %s", state)
		}
	}
	if publicationFileRE.MatchString("../senha.json") {
		t.Fatal("Arquivo fora do diretório foi aceito")
	}
	if publicationKey("repo", "request") != "repo/request" {
		t.Fatal("Chave instável")
	}
}
