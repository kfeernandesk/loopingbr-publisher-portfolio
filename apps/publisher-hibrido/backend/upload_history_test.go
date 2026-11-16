package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func historyRequest(b *Backend, method, path string, input any) *httptest.ResponseRecorder {
	var data []byte
	if input != nil {
		data, _ = json.Marshal(input)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("X-LoopingBR-Admin-Key", b.apiKey)
	w := httptest.NewRecorder()
	b.serve().ServeHTTP(w, r)
	return w
}
func TestHistoryOnlyConfirmedAndAccountScoped(t *testing.T) {
	d := t.TempDir()
	b := &Backend{dir: d, apiKey: strings.Repeat("x", 48), refresh: "YT", driveRefresh: "DRIVE"}
	b.saved.YouTubeAccountID = "A"
	b.saved.DriveAccountID = "A"
	for i := 0; i < 9; i++ {
		name := "thumbnail.png"
		rec := map[string]any{"id": "TESTUPLOADID__" + string(rune('A'+i)), "name": name, "kind": "thumbnail", "size": int64(220)}
		w := historyRequest(b, "POST", "/api/local/drive/uploads/record", rec)
		if w.Code != 201 {
			t.Fatalf("record %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	w := historyRequest(b, "GET", "/api/local/drive/uploads", nil)
	if w.Code != 200 {
		t.Fatalf("list %d", w.Code)
	}
	var v struct {
		Uploads []UploadRecord `json:"uploads"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if len(v.Uploads) != 6 || v.Uploads[0].ID != "TESTUPLOADID__I" {
		t.Fatalf("recent six %+v", v.Uploads)
	}
	for _, entry := range v.Uploads {
		if entry.AccountID != "" {
			t.Fatal("leaked account ID")
		}
	}
	saved, err := os.ReadFile(filepath.Join(d, "state.json"))
	if err != nil || !bytes.Contains(saved, []byte("TESTUPLOADID__I")) {
		t.Fatal("not persisted")
	}
	b.saved.YouTubeAccountID = "B"
	b.saved.DriveAccountID = "B"
	w = historyRequest(b, "GET", "/api/local/drive/uploads", nil)
	if w.Code != 200 {
		t.Fatalf("list B %d", w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if len(v.Uploads) != 0 {
		t.Fatal("other account uploads leaked")
	}
	b.driveRefresh = ""
	w = historyRequest(b, "GET", "/api/local/drive/uploads", nil)
	if w.Code != 403 {
		t.Fatalf("disconnected access: %d", w.Code)
	}
}
func TestHistoryRejectInvalidAndUnauthenticated(t *testing.T) {
	b := &Backend{apiKey: strings.Repeat("x", 48), refresh: "YT", driveRefresh: "DRIVE", dir: t.TempDir()}
	b.saved.YouTubeAccountID = "A"
	b.saved.DriveAccountID = "A"
	w := httptest.NewRecorder()
	b.serve().ServeHTTP(w, httptest.NewRequest("GET", "/api/local/drive/uploads", nil))
	if w.Code != 403 {
		t.Fatal("missing key accepted")
	}
	w = historyRequest(b, "POST", "/api/local/drive/uploads/record", map[string]any{"id": "bogus", "name": "bad.mp4", "kind": "video", "size": 3})
	if w.Code != 400 {
		t.Fatal("bad ID accepted")
	}
	w = historyRequest(b, "GET", "/api/local/drive/uploads", nil)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("bogus")) {
		t.Fatal("stored invalid upload")
	}
	_ = http.MethodPost
}

func TestRecentUploadsSurviveLocalGoogleDisconnect(t *testing.T) {
	b := &Backend{dir: t.TempDir(), apiKey: strings.Repeat("z", 48), refresh: "YT", driveRefresh: "DRIVE"}
	b.saved.YouTubeAccountID = "ACCOUNT"
	b.saved.DriveAccountID = "ACCOUNT"
	v := map[string]any{"id": "TESTFILEID_123456789012345", "name": "video.mp4", "kind": "video", "size": int64(1234)}
	if w := historyRequest(b, "POST", "/api/local/drive/uploads/record", v); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if w := historyRequest(b, "POST", "/api/local/disconnect", map[string]any{}); w.Code != 200 {
		t.Fatal("disconnect failed", w.Code)
	}
	if w := historyRequest(b, "GET", "/api/local/drive/uploads", nil); w.Code != 403 {
		t.Fatal("history visible after disconnect")
	}
	// Simulamos apenas o estado de uma nova autorização da mesma conta.
	b.refresh = "YT2"
	b.driveRefresh = "DRIVE2"
	b.saved.YouTubeAccountID = "ACCOUNT"
	b.saved.DriveAccountID = "ACCOUNT"
	w := historyRequest(b, "GET", "/api/local/drive/uploads", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "TESTFILEID_123456789012345") {
		t.Fatal("history lost on reconnect", w.Code, w.Body.String())
	}
}
