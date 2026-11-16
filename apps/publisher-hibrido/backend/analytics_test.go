package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAnalyticsAuthorizationOnlyViaExplicitStart(t *testing.T) {
	b := &Backend{webID: "test.apps.googleusercontent.com", webSecret: "test-secret", refresh: "youtube-refresh", saved: Saved{YouTubeAccountID: "account-1", ChannelID: "UC123456789"}, apiKey: "dummy-with-more-than-thirty-two-characters"}
	rr := httptest.NewRecorder()
	b.serve().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/local/status", nil))
	if rr.Code != 403 {
		t.Fatalf("status must require admin key: %d", rr.Code)
	}
	// Exibir status não inicia OAuth nem cria estado de consentimento.
	_ = b.status()
	if b.stateFlow != "" || b.state != "" {
		t.Fatal("status must not initiate analytics authorization")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/local/analytics/start", strings.NewReader("{}"))
	req.Header.Set("X-LoopingBR-Admin-Key", b.apiKey)
	rr = httptest.NewRecorder()
	b.serve().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("click-triggered start must work: %d: %s", rr.Code, rr.Body.String())
	}
	var auth struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(auth.URL)
	if err != nil {
		t.Fatal(err)
	}
	scopes := parsed.Query().Get("scope")
	if !strings.Contains(scopes, "yt-analytics.readonly") || !strings.Contains(scopes, "youtube.readonly") || strings.Contains(scopes, "drive.file") || strings.Contains(scopes, "youtube.upload") {
		t.Fatalf("Unexpected analytics scopes: %s", scopes)
	}
	if b.stateFlow != "analytics" {
		t.Fatalf("state flow %q", b.stateFlow)
	}
}
func TestAnalyticsRequiresGoogleChannel(t *testing.T) {
	b := &Backend{webID: "test.apps.googleusercontent.com", webSecret: "secret"}
	rr := httptest.NewRecorder()
	b.startAnalytics(rr, httptest.NewRequest(http.MethodPost, "/api/local/analytics/start", nil))
	if rr.Code != 409 {
		t.Fatalf("should refuse authorization before connecting YouTube: %d", rr.Code)
	}
}
func TestReportWithoutPermissionNeverCallsYouTube(t *testing.T) {
	b := &Backend{saved: Saved{ChannelID: "UCtest"}}
	rr := httptest.NewRecorder()
	b.analyticsReport(rr, httptest.NewRequest(http.MethodGet, "/api/local/analytics/report?video_id=abCD1234xyz", nil))
	if rr.Code != 403 {
		t.Fatalf("expected 403 for ungranted Analytics: %d", rr.Code)
	}
	if b.analyticsRefresh != "" {
		t.Fatal("report must not grant OAuth")
	}
}
func TestRetentionParsingNoFabricatedValues(t *testing.T) {
	a := analyticsRows{Rows: [][]any{{0.02, 0.99, 0.88}, {0.52, 0.33, 0.53}}}
	a.ColumnHeaders = make([]struct {
		Name string `json:"name"`
	}, 3)
	a.ColumnHeaders[0].Name = "elapsedVideoTimeRatio"
	a.ColumnHeaders[1].Name = "audienceWatchRatio"
	a.ColumnHeaders[2].Name = "relativeRetentionPerformance"
	points := extractRetention(a)
	if len(points) != 2 || points[1].WatchRatio != 0.33 || *points[1].RelativePerformance != 0.53 {
		t.Fatalf("bad retention parse: %#v", points)
	}
	if got := extractAnalyticsMetrics(analyticsRows{}); len(got) != 0 {
		t.Fatal("no data must not create fake numbers")
	}
}
