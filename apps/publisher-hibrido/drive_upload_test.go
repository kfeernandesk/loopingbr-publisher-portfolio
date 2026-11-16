package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestDriveSpecRejectsVideoUnsupportedByExistingPublisher(t *testing.T) {
	for _, test := range []struct {
		kind, name string
		size       int64
		wantOK     bool
	}{
		{"video", "movie.mp4", 234, true}, {"video", "movie.mov", 234, false},
		{"thumbnail", "art.png", 234, true}, {"thumbnail", "art.jpeg", 234, true},
		{"thumbnail", "art.gif", 234, false}, {"video", "zero.mp4", 0, false},
		{"video", "limite-exato.mp4", maxVideoUploadBytes, true},
		{"video", "oversized.mp4", maxVideoUploadBytes + 1, false},
	} {
		_, err := uploadFileSpec(test.kind, test.name, test.size)
		if (err == nil) != test.wantOK {
			t.Fatalf("kind=%s name=%s size=%d err=%v", test.kind, test.name, test.size, err)
		}
	}
}

func TestDriveResumableOnlyConfirmsCompleteVerifiedUpload(t *testing.T) {
	// >8MiB forces a 308 checkpoint followed by a final confirmation.
	data := bytes.Repeat([]byte("A"), driveChunkSize+300)
	expectedHash := md5.Sum(data)
	var got []byte
	starts, puts, reads := 0, 0, 0
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/upload/drive/v3/files"):
			starts++
			if r.Header.Get("X-Upload-Content-Length") != strconv.Itoa(len(data)) {
				t.Error("wrong size header")
			}
			w.Header().Set("Location", origin+"/session")
			w.WriteHeader(200)
		case r.Method == "PUT" && r.URL.Path == "/session":
			puts++
			if r.Header.Get("Authorization") != "Bearer testtoken" {
				t.Error("token missing")
			}
			body, _ := io.ReadAll(r.Body)
			rangeHeader := fmt.Sprintf("bytes %d-%d/%d", len(got), len(got)+len(body)-1, len(data))
			if r.Header.Get("Content-Range") != rangeHeader {
				t.Errorf("bad range: %v vs %v", r.Header.Get("Content-Range"), rangeHeader)
			}
			got = append(got, body...)
			if len(got) < len(data) {
				w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(got)-1))
				w.WriteHeader(308)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(driveFileInfo{ID: "drive-file-id-0123456", Size: strconv.Itoa(len(got)), MD5: hex.EncodeToString(expectedHash[:]), Name: "test.mp4"})
		default:
			reads++
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	origin = server.URL
	var counts []int64
	f, e := streamToDrive(context.Background(), bytes.NewReader(data), "test.mp4", "video/mp4", int64(len(data)), func() (string, error) { return "testtoken", nil }, server.URL, server.Client(), func(n int64) { counts = append(counts, n) })
	if e != nil {
		t.Fatal(e)
	}
	if f.ID != "drive-file-id-0123456" || !bytes.Equal(got, data) || starts != 1 || puts != 2 || reads != 0 {
		t.Fatalf("wrong confirmed file or extra calls: %+v starts=%d puts=%d reads=%d", f, starts, puts, reads)
	}
	if len(counts) != 2 || counts[len(counts)-1] != int64(len(data)) {
		t.Fatalf("progress only after Drive checkpoint: %v", counts)
	}
}
func TestDriveTruncatedFileNeverSendsFinalChunk(t *testing.T) {
	var origin string
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Location", origin+"/session")
			return
		}
		if r.Method == "PUT" {
			puts++
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	origin = server.URL
	_, e := streamToDrive(context.Background(), bytes.NewReader([]byte("abc")), "x.png", "image/png", 4, func() (string, error) { return "tok", nil }, server.URL, server.Client(), nil)
	if e == nil || puts != 0 {
		t.Fatalf("truncated input was committed; err=%v puts=%d", e, puts)
	}
}
func TestDriveRejectsWrongHashWithoutUsingID(t *testing.T) {
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Location", origin+"/session")
			return
		}
		if r.Method == "PUT" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(driveFileInfo{ID: "drive-file-id-0123456", Size: "8", MD5: "bad"})
			return
		}
	}))
	defer server.Close()
	origin = server.URL
	_, e := streamToDrive(context.Background(), bytes.NewReader([]byte("12345678")), "x.png", "image/png", 8, func() (string, error) { return "tok", nil }, server.URL, server.Client(), nil)
	if e == nil || !strings.Contains(e.Error(), "integridade") {
		t.Fatalf("wrong hash passed: %v", e)
	}
}
func TestProgressRejectsParallelJobsAndIsolatesIDs(t *testing.T) {
	var m driveUploadManager
	if e := m.begin("a", "video", 10); e != nil {
		t.Fatal(e)
	}
	if e := m.begin("b", "thumbnail", 10); e == nil {
		t.Fatal("concurrent upload allowed")
	}
	m.update("b", 8)
	p, ok := m.snapshot("a")
	if !ok || p.Confirmed != 0 {
		t.Fatal("wrong progress or wrong id")
	}
	m.finish("a", "falhou")
	if e := m.begin("b", "thumbnail", 10); e != nil {
		t.Fatal(e)
	}
}
