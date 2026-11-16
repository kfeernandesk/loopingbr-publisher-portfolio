package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHostAndScopes(t *testing.T) {
	h := uuidHost()
	if !strings.HasPrefix(h, "urn:uuid:") || len(h) != 45 {
		t.Fatalf("host ID invalido: %q", h)
	}
	if !(ChatGPTTokens{Scope: "openid chatgpt.tokens.use.direct"}).ScopesAllowPlan() {
		t.Fatal("escopo valido nao reconhecido")
	}
	if (ChatGPTTokens{Scope: "openid email"}).ScopesAllowPlan() {
		t.Fatal("escopo de identidade nao deve permitir inferencia")
	}
}
func TestChatGPTStartUsesLocalPKCE(t *testing.T) {
	a := &App{cfg: Config{}, address: "127.0.0.1:9988"}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/chatgpt/start", strings.NewReader(`{"new_account":true}`))
	a.chatGPTStart(w, r)
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var v struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(w.Body.Bytes(), &v) != nil {
		t.Fatal("JSON invalido")
	}
	u, err := url.Parse(v.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "auth.openai.com" || q.Get("client_id") != "dynamic_agent_client" || q.Get("redirect_uri") != "http://127.0.0.1:9988/auth/callback" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("parametros OAuth invalidos: %v", q)
	}
	if q.Get("ext_agent_host_id") != a.cfg.ChatGPTHostID || a.cfg.ChatGPTHostID == "" {
		t.Fatal("host incorreto")
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(q.Get("code_challenge")); err != nil || len(decoded) != 32 {
		t.Fatal("challenge invalido")
	}
}
func TestChatGPTCallbackRejectsState(t *testing.T) {
	a := &App{chatGPTPending: ChatGPTPending{State: "expected"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/auth/callback?state=wrong&code=abc", nil)
	a.chatGPTCallback(w, r)
	if w.Code != 400 {
		t.Fatalf("esperado 400, obtido %d", w.Code)
	}
}

func TestModelListSupportsLargeResponses(t *testing.T) {
	// Crosses the former 512 KiB limit without exceeding the safe 16 MiB cap.
	extra := strings.Repeat("a", 600*1024)
	raw, _ := json.Marshal(map[string]any{
		"models":   []map[string]string{{"slug": "gpt-test", "display_name": "Test Model", "visibility": "list"}},
		"metadata": extra,
	})
	models, err := decodeModelList(strings.NewReader(string(raw)))
	if err != nil || len(models) != 1 || models[0].Slug != "gpt-test" {
		t.Fatalf("falha ao decodificar resposta longa: %#v, %v", models, err)
	}
}
func TestModelListPublicAPIShape(t *testing.T) {
	models, err := decodeModelList(strings.NewReader(`{"object":"list","data":[{"id":"gpt-example","object":"model"}]}`))
	if err != nil || len(models) != 1 || models[0].Slug != "gpt-example" {
		t.Fatalf("falha na forma documentada: %#v, %v", models, err)
	}
}
func TestModelListTruncatedResponse(t *testing.T) {
	_, err := decodeModelList(strings.NewReader(`{"models":[{"slug":"gpt-example"}`))
	if err == nil || !strings.Contains(err.Error(), "incompleta") {
		t.Fatalf("falha no diagnostico de resposta truncada: %v", err)
	}
}
