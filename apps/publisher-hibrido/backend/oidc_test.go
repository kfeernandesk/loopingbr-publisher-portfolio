package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOIDCRejectsInconsistentClaims(t *testing.T) {
	repo := RepoInfo{FullName: "demo/loopingbr-desktop-aabbccddeeff", ID: 12345, Branch: "main", ChannelID: "UC123"}
	now := time.Now().Unix()
	c := githubClaims{Iss: "https://token.actions.githubusercontent.com", Aud: json.RawMessage(`"` + oidcAudience + `"`), Iat: now, Nbf: now - 1, Exp: now + 300, Repository: repo.FullName, RepositoryID: "12345", RepositoryVisibility: "private", Ref: "refs/heads/main", EventName: "push", WorkflowRef: repo.FullName + "/.github/workflows/publicar.yml@refs/heads/main"}
	if e := validateClaims(c, repo); e != nil {
		t.Fatal(e)
	}
	c.RepositoryID = "999"
	if validateClaims(c, repo) == nil {
		t.Fatal("different repository ID accepted")
	}
	c.RepositoryID = "12345"
	c.Aud = json.RawMessage(`"wrong"`)
	if validateClaims(c, repo) == nil {
		t.Fatal("wrong audience accepted")
	}
	c.Aud = json.RawMessage(`"` + oidcAudience + `"`)
	c.WorkflowRef = "other/repo/.github/workflows/publicar.yml@refs/heads/main"
	if validateClaims(c, repo) == nil {
		t.Fatal("foreign workflow accepted")
	}
	c.WorkflowRef = repo.FullName + "/.github/workflows/publicar.yml@refs/heads/main"
	c.EventName = "pull_request"
	if validateClaims(c, repo) == nil {
		t.Fatal("PR event accepted")
	}
}
func TestOIDCValidatesRS256Signature(t *testing.T) {
	priv, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	old := githubJwksURL
	defer func() { githubJwksURL = old }()
	exponent := big.NewInt(int64(priv.PublicKey.E)).Bytes()
	keyObj := map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "lab-key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(exponent)}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(keyObj) }))
	defer server.Close()
	githubJwksURL = server.URL
	repo := RepoInfo{FullName: "demo/loopingbr-desktop-aabbccddeeff", ID: 6789, Branch: "main"}
	now := time.Now().Unix()
	claims := githubClaims{Iss: "https://token.actions.githubusercontent.com", Aud: json.RawMessage(`"` + oidcAudience + `"`), Iat: now, Nbf: now, Exp: now + 300, Repository: repo.FullName, RepositoryID: strconv.FormatInt(repo.ID, 10), RepositoryVisibility: "private", Ref: "refs/heads/main", EventName: "push", WorkflowRef: repo.FullName + "/.github/workflows/publicar.yml@refs/heads/main"}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "lab-key"})
	payload, _ := json.Marshal(claims)
	head := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	h := sha256.Sum256([]byte(head))
	sig, e := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
	if e != nil {
		t.Fatal(e)
	}
	jwt := head + "." + base64.RawURLEncoding.EncodeToString(sig)
	got, e := signedClaims(jwt)
	if e != nil || validateClaims(got, repo) != nil {
		t.Fatalf("valid signed token rejected: %v", e)
	}
	forged := jwt[:len(jwt)-3] + strings.Repeat("A", 3)
	if _, e = signedClaims(forged); e == nil {
		t.Fatal("modified signature accepted")
	}
}
func TestAdminHeaderCannotBeSkipped(t *testing.T) {
	b := &Backend{apiKey: strings.Repeat("K", 43)}
	handler := b.admin(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"ok": "yes"}) })
	r := httptest.NewRequest("GET", "/api/local/status", nil)
	w := httptest.NewRecorder()
	handler(w, r)
	if w.Code != 403 {
		t.Fatal("Admin endpoint leaked")
	}
}
