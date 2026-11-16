package main

import (
	"strings"
	"testing"
)

func TestEngineStateIsConservative(t *testing.T) {
	original := []byte("publisher current")
	if state := engineStateForData(original, original); state != "current" {
		t.Fatalf("same motor: %q", state)
	}
	if state := engineStateForData([]byte("personalized motor"), original); state != "unknown" {
		t.Fatalf("custom motor must not be overwritten: %q", state)
	}
}
func TestPublicationTagsValidation(t *testing.T) {
	base := func() Job {
		return Job{VideoDriveID: "abcdefghijklmnopq123", Title: "Título teste", Description: "Descrição", Visibility: "unlisted", Tipo: "video", ChannelID: "UCtest", Confirm: "CONFIRMO_PUBLICAR", Confirmed: true}
	}
	valid := base()
	valid.Tags = []string{"Bloodborne", "Soulslike", "Gameplay"}
	if err := valid.valid("UCtest"); err != nil {
		t.Fatalf("tags validas: %v", err)
	}
	repeated := base()
	repeated.Tags = []string{"Soulslike", "soulslike"}
	if err := repeated.valid("UCtest"); err == nil {
		t.Fatal("tags duplicadas deveriam ser bloqueadas")
	}
	comma := base()
	comma.Tags = []string{"gameplay,canal"}
	if err := comma.valid("UCtest"); err == nil {
		t.Fatal("virgula interna deveria ser bloqueada")
	}
	oversized := base()
	oversized.Tags = []string{strings.Repeat("a", 61)}
	if err := oversized.valid("UCtest"); err == nil {
		t.Fatal("tag grande deveria ser bloqueada")
	}
}
