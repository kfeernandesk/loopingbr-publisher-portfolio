package main

import (
	"strings"
	"testing"
)

func TestWorkflowDoesNotReceivePermanentGoogleSecrets(t *testing.T) {
	for _, name := range []string{"assets/workflow.yml", "assets/publisher.py"} {
		raw, e := assets.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		s := string(raw)
		for _, secret := range []string{"GOOGLE_CLIENT_SECRET", "GOOGLE_REFRESH_TOKEN"} {
			if strings.Contains(s, secret) {
				t.Fatalf("%s contains secret name %s", name, secret)
			}
		}
	}
	wf, e := assets.ReadFile("assets/workflow.yml")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(wf), "id-token: write") {
		t.Fatal("GitHub OIDC permission missing")
	}
}
