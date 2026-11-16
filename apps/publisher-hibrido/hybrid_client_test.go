package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHybridProfileDoesNotImportPreviousGoogleCredential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AppData", os.Getenv("XDG_CONFIG_HOME"))
	prev := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "LoopingBRPublisherDesktopV07")
	if e := os.MkdirAll(prev, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(prev, "config.json"), []byte(`{"repo":"real/loopingbr-desktop-aabbccddeeff","google_dpapi":"secret","ready":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	c := loadConfig()
	if c.Repo != "" || c.Ready {
		t.Fatalf("lab imported production credentials: %+v", c)
	}
	if !strings.Contains(cfgPath(), "LoopingBRHybridV1014Desktop") {
		t.Fatal("wrong profile")
	}
}
