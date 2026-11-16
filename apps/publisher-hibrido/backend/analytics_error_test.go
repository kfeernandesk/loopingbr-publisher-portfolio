package main

import (
	"strings"
	"testing"
)

func TestAnalyticsHTTPErrorReportsActionableStatuses(t *testing.T) {
	cases := []struct {
		code     int
		contains string
	}{{401, "expirou"}, {403, "API"}, {400, "consulta"}, {429, "limite"}, {502, "502"}}
	for _, tt := range cases {
		if got := analyticsHTTPError(tt.code).Error(); !strings.Contains(got, tt.contains) {
			t.Fatalf("code %d: %q", tt.code, got)
		}
	}
}
