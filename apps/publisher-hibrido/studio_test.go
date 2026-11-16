package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStudioArchiveEmbeddedAndValid(t *testing.T) {
	reader, err := zip.NewReader(bytes.NewReader(studioPluginZip), int64(len(studioPluginZip)))
	if err != nil {
		t.Fatalf("invalid embedded plugin archive: %v", err)
	}
	var manifest, skill bool
	for _, file := range reader.File {
		if file.Name == "plugin.json" {
			manifest = true
		}
		if file.Name == "skills/loopingbr-studio/SKILL.md" {
			skill = true
		}
	}
	if !manifest || !skill {
		t.Fatal("missing plugin metadata or Studio skill")
	}
}
func TestStudioPackageWriteIsIdempotentAndNeverOverwritesModifiedFile(t *testing.T) {
	dir := t.TempDir()
	p, err := writeStudioPackage(dir)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(original, studioPluginZip) {
		t.Fatal("written file not identical to embedded plugin")
	}
	again, err := writeStudioPackage(dir)
	if err != nil || again != p {
		t.Fatalf("expected stable package path: %v %s", err, again)
	}
	if err := os.WriteFile(p, []byte("personal-file-do-not-overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	alt, err := writeStudioPackage(dir)
	if err != nil || alt == p {
		t.Fatalf("expected alternative filename for modified file: %s %v", alt, err)
	}
	current, err := os.ReadFile(p)
	if err != nil || string(current) != "personal-file-do-not-overwrite" {
		t.Fatal("overwrote an existing user file")
	}
	b, err := os.ReadFile(alt)
	if err != nil || !bytes.Equal(b, studioPluginZip) {
		t.Fatal("invalid alternative archive")
	}
}
func TestStudioLinkValidation(t *testing.T) {
	good := []string{"https://chatgpt.com/plugins/plugins_6af6a820a65081919c0f15a3b00cbcc0", "https://chatgpt.com/plugins/plugin_abc123"}
	bad := []string{"http://chatgpt.com/plugins/plugin_abc", "https://evil.example/plugins/plugins_abc", "https://chatgpt.com.evil.com/plugins/plugins_abc", "https://chatgpt.com@evil.com/plugins/plugins_abc", "https://chatgpt.com/plugins/plugins_abc?redirect=https://evil.com", "file:///etc/passwd", "javascript:alert(1)", "https://chatgpt.com/plugins/../settings", "https://chatgpt.com/g/g-abc", "https://chatgpt.com/plugins/plugins_a#junk", ""}
	for _, s := range good {
		if _, err := validateStudioURL(s); err != nil {
			t.Fatalf("rejects good URL %s: %v", s, err)
		}
	}
	for _, s := range bad {
		if _, err := validateStudioURL(s); err == nil {
			t.Fatalf("accepts bad URL %s", s)
		}
	}
}
func TestStudioLinkSavedInSeparateVersionProfile(t *testing.T) {
	tmp := t.TempDir()
	// os.UserConfigDir uses XDG_CONFIG_HOME on Unix and AppData on Windows.
	// Point both to the isolated test directory; never write to a real user profile.
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("AppData", tmp)
	cfgFile := cfgPath()
	if filepath.Clean(filepath.Dir(filepath.Dir(cfgFile))) != filepath.Clean(tmp) {
		t.Fatalf("test config path escaped isolated temporary directory: %q", cfgFile)
	}
	app := &App{cfg: Config{}}
	body := `{"url":"https://chatgpt.com/plugins/plugins_abc123"}`
	w := httptest.NewRecorder()
	app.studioSaveLink(w, httptest.NewRequest(http.MethodPost, "/api/studio/save-link", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("save error: %d %s", w.Code, w.Body.String())
	}
	saved, err := os.ReadFile(cfgFile)
	if err != nil || !bytes.Contains(saved, []byte("plugins_abc123")) {
		t.Fatalf("saved config missing URL: %v", err)
	}
	var c Config
	if err := json.Unmarshal(saved, &c); err != nil || c.StudioPluginURL != "https://chatgpt.com/plugins/plugins_abc123" {
		t.Fatal("URL not correctly persisted")
	}
	w2 := httptest.NewRecorder()
	app.studioForgetLink(w2, httptest.NewRequest(http.MethodPost, "/api/studio/forget-link", strings.NewReader(`{}`)))
	if w2.Code != 200 {
		t.Fatalf("forget link: %d", w2.Code)
	}
	if app.cfg.StudioPluginURL != "" {
		t.Fatal("did not forget URL")
	}
	// Removing a local link does not modify any remote ChatGPT plugins.
	// Only the independent v0.7 profile is written by this handler.
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(cfgFile)), "LoopingBRPublisherDesktopV06", "config.json")); !os.IsNotExist(err) {
		t.Fatal("v0.6 profile must not be created/changed")
	}
}
