package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"testing"
)

func TestBlake2b192(t *testing.T) {
	got := blake2b192([]byte("abc"))
	// Python hashlib.blake2b(b"abc", digest_size=24).hexdigest()
	want := "56a17e38cc371a46b12c32f18e0c61de2a84e9c2555b114e"
	if fmt.Sprintf("%x", got) != want {
		t.Fatalf("BLAKE2b-192 mismatch: %x", got)
	}
}
func TestGitHubSealedBoxInteroperability(t *testing.T) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("GOOGLE_REFRESH_TOKEN_TEST_only_do_not_use")
	sealed, err := sealForGitHub(msg, key.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != len(msg)+48 {
		t.Fatalf("wrong sealed length: %d", len(sealed))
	}
	fmt.Printf("SODIUM_SECRET_KEY_B64=%s\n", base64.StdEncoding.EncodeToString(key.Bytes()))
	fmt.Printf("SODIUM_PUBLIC_KEY_B64=%s\n", base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()))
	fmt.Printf("SODIUM_CIPHERTEXT_B64=%s\n", base64.StdEncoding.EncodeToString(sealed))
}
func TestDriveAndRepoConstraints(t *testing.T) {
	if id, err := normalizeDriveID("https://drive.google.com/file/d/1u7DeCeq3Tu2On_yl5_8NyasUYgustcYn/view"); err != nil || id != "1u7DeCeq3Tu2On_yl5_8NyasUYgustcYn" {
		t.Fatalf("bad normalized id %s / %v", id, err)
	}
	if _, err := normalizeDriveID("https://evil.example/file/d/1u7DeCeq3Tu2On_yl5_8NyasUYgustcYn/view"); err == nil {
		t.Fatal("unsafe external Drive link accepted")
	}
	if err := validateOwnedRepo("kfeernandesk/Loopingbr_publisher", "kfeernandesk"); err == nil {
		t.Fatal("original publisher repo should not be writable")
	}
	if err := validateOwnedRepo("someone/loopingbr-desktop-aabbccddeeff", "kfeernandesk"); err == nil {
		t.Fatal("other user repo should be blocked")
	}
	if err := validateOwnedRepo("kfeernandesk/loopingbr-desktop-aabbccddeeff", "kfeernandesk"); err != nil {
		t.Fatal(err)
	}
}
