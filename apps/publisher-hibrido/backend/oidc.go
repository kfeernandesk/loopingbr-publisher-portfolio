package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A identidade assinada do GitHub Actions identifica repositório, workflow e branch.
// OIDC NÃO é segredo de longa duração. Não aceitar tokens sem verificar assinatura.
var githubJwksURL = "https://token.actions.githubusercontent.com/.well-known/jwks"

type githubClaims struct {
	Iss                  string          `json:"iss"`
	Sub                  string          `json:"sub"`
	Aud                  json.RawMessage `json:"aud"`
	Exp                  int64           `json:"exp"`
	Nbf                  int64           `json:"nbf"`
	Iat                  int64           `json:"iat"`
	Repository           string          `json:"repository"`
	RepositoryID         string          `json:"repository_id"`
	RepositoryVisibility string          `json:"repository_visibility"`
	Ref                  string          `json:"ref"`
	EventName            string          `json:"event_name"`
	WorkflowRef          string          `json:"workflow_ref"`
}

func audienceMatches(raw json.RawMessage, expected string) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s == expected
	}
	var a []string
	if json.Unmarshal(raw, &a) == nil {
		for _, v := range a {
			if v == expected {
				return true
			}
		}
	}
	return false
}
func validateClaims(c githubClaims, repo RepoInfo) error {
	now := time.Now().Unix()
	if c.Iss != "https://token.actions.githubusercontent.com" || !audienceMatches(c.Aud, oidcAudience) {
		return errors.New("emissor ou audiência OIDC inválidos")
	}
	if c.Exp < now || c.Exp > now+600 || c.Iat < now-600 || c.Iat > now+60 || c.Nbf > now+60 {
		return errors.New("token OIDC expirado ou fora do intervalo aceitável")
	}
	if c.Repository != repo.FullName || c.RepositoryID != strconv.FormatInt(repo.ID, 10) || c.RepositoryVisibility != "private" {
		return errors.New("repositório GitHub não corresponde ao repositório autorizado")
	}
	if c.Ref != "refs/heads/"+repo.Branch || c.EventName != "push" || c.WorkflowRef != repo.FullName+"/.github/workflows/publicar.yml@refs/heads/"+repo.Branch {
		return errors.New("workflow, evento ou branch não autorizados")
	}
	return nil
}
func signedClaims(idToken string) (githubClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 || len(idToken) > 16000 {
		return githubClaims{}, errors.New("token OIDC inválido")
	}
	hb, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return githubClaims{}, e
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if json.Unmarshal(hb, &hdr) != nil || hdr.Alg != "RS256" || hdr.Kid == "" {
		return githubClaims{}, errors.New("algoritmo OIDC não permitido")
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return githubClaims{}, e
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return githubClaims{}, e
	}
	resp, e := client.Get(githubJwksURL)
	if e != nil {
		return githubClaims{}, errors.New("não foi possível consultar chaves públicas do GitHub")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return githubClaims{}, fmt.Errorf("chaves GitHub indisponíveis HTTP %d", resp.StatusCode)
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&jwks); e != nil {
		return githubClaims{}, errors.New("JWKS inválido")
	}
	var matched bool
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	for _, key := range jwks.Keys {
		if key.Kid != hdr.Kid || key.Kty != "RSA" || key.N == "" || key.E == "" {
			continue
		}
		if key.Alg != "" && key.Alg != "RS256" {
			continue
		}
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		nBytes, e := base64.RawURLEncoding.DecodeString(key.N)
		if e != nil {
			continue
		}
		eBytes, e := base64.RawURLEncoding.DecodeString(key.E)
		if e != nil {
			continue
		}
		exponent := 0
		for _, c := range eBytes {
			exponent = exponent*256 + int(c)
		}
		if exponent < 3 || exponent > 1<<20 {
			continue
		}
		pub := rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}
		if pub.N.BitLen() < 2048 {
			continue
		}
		if rsa.VerifyPKCS1v15(&pub, crypto.SHA256, hash[:], sig) == nil {
			matched = true
			break
		}
	}
	if !matched {
		return githubClaims{}, errors.New("assinatura OIDC inválida")
	}
	var c githubClaims
	if json.Unmarshal(payload, &c) != nil {
		return c, errors.New("claims OIDC inválidas")
	}
	return c, nil
}
func (b *Backend) runnerToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "Use POST")
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		fail(w, 401, "Identidade OIDC GitHub ausente")
		return
	}
	var input struct {
		ChannelID string `json:"channel_id"`
	}
	if readJSON(r, &input) != nil {
		fail(w, 400, "Solicitação inválida")
		return
	}
	b.mu.Lock()
	registered := b.saved.Repo
	connected := b.refresh != ""
	b.mu.Unlock()
	if !connected || registered.ID <= 0 || input.ChannelID != registered.ChannelID {
		fail(w, 403, "Repositório ou canal não registrado neste backend")
		return
	}
	claims, e := signedClaims(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")))
	if e != nil {
		fail(w, 403, e.Error())
		return
	}
	if e = validateClaims(claims, registered); e != nil {
		fail(w, 403, e.Error())
		return
	}
	access, e := b.accessToken()
	if e != nil {
		fail(w, 502, "Falha ao renovar autorização Google: "+e.Error())
		return
	}
	b.mu.Lock()
	ttl := int(time.Until(b.accessUntil).Seconds()) - 90
	b.mu.Unlock()
	if ttl <= 0 {
		fail(w, 503, "A validade do token Google é insuficiente; tente novamente")
		return
	}
	if ttl > 3000 {
		ttl = 3000
	}
	writeJSON(w, 200, map[string]any{"access_token": access, "expires_in": ttl, "channel_id": registered.ChannelID, "token_type": "Bearer"})
}
