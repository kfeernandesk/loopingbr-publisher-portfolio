package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func getScopeURL(t *testing.T, b *Backend, drive bool) (string, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/local/start", nil)
	res := httptest.NewRecorder()
	if drive {
		b.startDrive(res, req)
	} else {
		b.startGoogle(res, req)
	}
	if res.Code != 200 {
		t.Fatalf("start authorization failed: %d %s", res.Code, res.Body.String())
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "accounts.google.com" {
		t.Fatalf("unexpected authorization host: %q", u.Host)
	}
	return u.Query().Get("scope"), u.Query().Get("state")
}

func TestOAuthSeparatedScopesAvoid400Combination(t *testing.T) {
	b := &Backend{webID: "test.web.apps.googleusercontent.com", webSecret: "local-test-secret"}
	ytScope, ytState := getScopeURL(t, b, false)
	if ytScope != youtubeScopes || strings.Contains(ytScope, "drive.file") || !strings.Contains(ytScope, "youtube.upload") {
		t.Fatalf("YouTube scopes changed: %q", ytScope)
	}
	b.refresh = "youtube-token-for-test"
	b.saved.YouTubeAccountID = "123456789"
	driveScope, driveState := getScopeURL(t, b, true)
	if driveScope != uploadScopes || strings.Contains(driveScope, "youtube") {
		t.Fatalf("Drive consent requested YouTube scopes: %q", driveScope)
	}
	if driveState == ytState {
		t.Fatal("OAuth state was reused")
	}
	if b.stateFlow != "drive" {
		t.Fatalf("wrong flow state: %q", b.stateFlow)
	}
}

func TestDriveAuthorizationRequiresYouTube(t *testing.T) {
	b := &Backend{webID: "test.web.apps.googleusercontent.com", webSecret: "test-secret"}
	w := httptest.NewRecorder()
	b.startDrive(w, httptest.NewRequest("POST", "/api/local/drive/start", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("drive authorized before YouTube: %d", w.Code)
	}
}

func TestDistinctGoogleAccountsCannotUpload(t *testing.T) {
	if sameGoogleAccount("", "") || sameGoogleAccount("first", "") || sameGoogleAccount("first", "second") || !sameGoogleAccount("first", "first") {
		t.Fatal("incorrect Google account identity validation")
	}
	b := &Backend{refresh: "youtube", driveRefresh: "drive"}
	b.saved.YouTubeAccountID = "one"
	b.saved.DriveAccountID = "two"
	if _, err := b.driveAccessToken(); err == nil {
		t.Fatal("Drive token from another account was accepted")
	}
	if status := b.status(); status["drive_connected"] == true {
		t.Fatal("Drive mismatched account shown as connected")
	}
}

func TestDriveAccessRequiresAdminKey(t *testing.T) {
	b := &Backend{apiKey: strings.Repeat("x", 48)}
	w := httptest.NewRecorder()
	b.serve().ServeHTTP(w, httptest.NewRequest("GET", "/api/local/drive/access", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized API not blocked: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/local/drive/access", nil)
	r.Header.Set("X-LoopingBR-Admin-Key", b.apiKey)
	w = httptest.NewRecorder()
	b.serve().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("unconfigured Drive returned token")
	}
}

func TestBackendProfileDoesNotImportV10(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	t.Setenv("LOOPINGBR_HYBRID_PROFILE_DIR", "")
	prior := filepath.Join(dir, "LoopingBRHybridV101Backend")
	if err := os.MkdirAll(prior, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prior, "state.json"), []byte(`{"channel_id":"old-channel","repo":{"id":1}}`), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := newBackend()
	if err != nil {
		t.Fatal(err)
	}
	if b.saved.ChannelID != "" || b.saved.Repo.ID != 0 || !strings.Contains(b.dir, "LoopingBRHybridV1014Backend") {
		t.Fatal("Imported previous lab profile")
	}
}
