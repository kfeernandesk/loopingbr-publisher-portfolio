package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var analyticsVideoRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{11}$`)

// A autorização do Analytics e separada. O refresh token nunca e enviado ao
// Desktop ou GitHub Actions. A API recebe somente o video ID da conta autorizada.
func (b *Backend) analyticsAccessToken() (string, error) {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	b.mu.Lock()
	if b.analyticsRefresh == "" || b.refresh == "" || !sameGoogleAccount(b.saved.AnalyticsAccountID, b.saved.YouTubeAccountID) || b.saved.AnalyticsChannelID == "" || b.saved.AnalyticsChannelID != b.saved.ChannelID {
		b.mu.Unlock()
		return "", errors.New("YouTube Analytics ainda não autorizado para este canal. Clique em Analisar para autorizar.")
	}
	if b.analyticsAccess != "" && time.Until(b.analyticsAccessUntil) > 2*time.Minute {
		token := b.analyticsAccess
		b.mu.Unlock()
		return token, nil
	}
	refresh := b.analyticsRefresh
	b.mu.Unlock()
	vals := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {b.webID}, "client_secret": {b.webSecret}}
	tok, err := exchangeGoogle(context.Background(), vals)
	if err != nil {
		return "", fmt.Errorf("Autorização Analytics expirada ou revogada: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// Não permitir aplicar um refresh antigo depois de uma troca de conta/canal.
	if b.analyticsRefresh != refresh || b.refresh == "" || b.saved.AnalyticsChannelID != b.saved.ChannelID || !sameGoogleAccount(b.saved.AnalyticsAccountID, b.saved.YouTubeAccountID) {
		return "", errors.New("O canal conectado mudou durante a consulta. Tente novamente.")
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refresh {
		protected, e := protectSecret(tok.RefreshToken)
		if e != nil {
			return "", e
		}
		old := b.saved.AnalyticsRefreshDPAPI
		b.saved.AnalyticsRefreshDPAPI = base64.StdEncoding.EncodeToString(protected)
		if e = b.saveLocked(); e != nil {
			b.saved.AnalyticsRefreshDPAPI = old
			return "", e
		}
		b.analyticsRefresh = tok.RefreshToken
	}
	if tok.ExpiresIn < 180 {
		tok.ExpiresIn = 3600
	}
	b.analyticsAccess = tok.AccessToken
	b.analyticsAccessUntil = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return b.analyticsAccess, nil
}

type analyticsRows struct {
	ColumnHeaders []struct {
		Name string `json:"name"`
	} `json:"columnHeaders"`
	Rows [][]any `json:"rows"`
}

// Indica o problema real sem expor respostas OAuth ou detalhes sensíveis do Google.
func analyticsHTTPError(code int) error {
	switch code {
	case http.StatusUnauthorized:
		return errors.New("A autorização do Analytics expirou. Remova apenas a permissão do Analytics e conecte novamente.")
	case http.StatusForbidden:
		return errors.New("A consulta ao YouTube Analytics foi negada (403). Confirme a mesma conta/canal e se a YouTube Analytics API está ativada no Google Cloud.")
	case http.StatusBadRequest:
		return errors.New("O YouTube Analytics rejeitou esta consulta (400). Confira se o vídeo possui métricas disponíveis e se a API está ativada.")
	case http.StatusTooManyRequests:
		return errors.New("O YouTube Analytics atingiu um limite temporário. Aguarde e tente novamente.")
	default:
		return fmt.Errorf("O YouTube Analytics não respondeu normalmente (HTTP %d). Tente mais tarde.", code)
	}
}

func queryAnalytics(ctx context.Context, token string, dimensions, metrics, videoID, startDate, endDate string) (analyticsRows, error) {
	params := url.Values{"ids": {"channel==MINE"}, "startDate": {startDate}, "endDate": {endDate}, "metrics": {metrics}, "filters": {"video==" + videoID}}
	if dimensions != "" {
		params.Set("dimensions", dimensions)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", "https://youtubeanalytics.googleapis.com/v2/reports?"+params.Encode(), nil)
	if e != nil {
		return analyticsRows{}, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e := client.Do(req)
	if e != nil {
		return analyticsRows{}, e
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if e != nil {
		return analyticsRows{}, e
	}
	if resp.StatusCode != 200 {
		return analyticsRows{}, analyticsHTTPError(resp.StatusCode)
	}
	var out analyticsRows
	if e = json.Unmarshal(raw, &out); e != nil {
		return out, errors.New("Dados inválidos recebidos da API do Analytics")
	}
	return out, nil
}

func rowNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	case string:
		f, e := strconv.ParseFloat(n, 64)
		return f, e == nil
	}
	return 0, false
}

func extractAnalyticsMetrics(data analyticsRows) map[string]float64 {
	out := map[string]float64{}
	if len(data.Rows) == 0 {
		return out
	}
	for i, h := range data.ColumnHeaders {
		if i < len(data.Rows[0]) {
			if f, ok := rowNumber(data.Rows[0][i]); ok {
				out[h.Name] = f
			}
		}
	}
	return out
}

type retentionPoint struct {
	Position            float64  `json:"position"`
	WatchRatio          float64  `json:"watch_ratio"`
	RelativePerformance *float64 `json:"relative_performance,omitempty"`
}

func extractRetention(data analyticsRows) []retentionPoint {
	posIndex, watchIndex, relIndex := -1, -1, -1
	for i, h := range data.ColumnHeaders {
		switch h.Name {
		case "elapsedVideoTimeRatio":
			posIndex = i
		case "audienceWatchRatio":
			watchIndex = i
		case "relativeRetentionPerformance":
			relIndex = i
		}
	}
	pts := []retentionPoint{}
	if posIndex < 0 || watchIndex < 0 {
		return pts
	}
	for _, row := range data.Rows {
		if posIndex >= len(row) || watchIndex >= len(row) {
			continue
		}
		pos, yes1 := rowNumber(row[posIndex])
		watch, yes2 := rowNumber(row[watchIndex])
		if !yes1 || !yes2 || pos < 0 || pos > 1.01 || watch < 0 || watch > 10 {
			continue
		}
		p := retentionPoint{Position: pos, WatchRatio: watch}
		if relIndex >= 0 && relIndex < len(row) {
			if rel, ok := rowNumber(row[relIndex]); ok {
				p.RelativePerformance = &rel
			}
		}
		pts = append(pts, p)
		if len(pts) >= 500 {
			break
		}
	}
	return pts
}

func (b *Backend) analyticsReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fail(w, 405, "Use GET")
		return
	}
	videoID := strings.TrimSpace(r.URL.Query().Get("video_id"))
	if !analyticsVideoRE.MatchString(videoID) {
		fail(w, 400, "ID do vídeo inválido")
		return
	}
	b.mu.Lock()
	channel := b.saved.ChannelID
	b.mu.Unlock()
	if channel == "" {
		fail(w, 403, "Conecte o canal Google/YouTube antes de analisar")
		return
	}
	token, e := b.analyticsAccessToken()
	if e != nil {
		fail(w, 403, e.Error())
		return
	}
	// Dados diários do Analytics podem demorar até 48-72 horas.
	end := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	start := "2005-01-01"
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	totals, e := queryAnalytics(ctx, token, "", "views,estimatedMinutesWatched,averageViewDuration,averageViewPercentage", videoID, start, end)
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	// A ausência de retenção não impede consultar as métricas gerais.
	retention, retentionErr := queryAnalytics(ctx, token, "elapsedVideoTimeRatio", "audienceWatchRatio,relativeRetentionPerformance", videoID, start, end)
	if retentionErr != nil {
		retention, retentionErr = queryAnalytics(ctx, token, "elapsedVideoTimeRatio", "audienceWatchRatio", videoID, start, end)
	}
	pts := []retentionPoint{}
	warning := ""
	if retentionErr != nil {
		warning = "Retenção não disponível para este vídeo no momento."
	} else {
		pts = extractRetention(retention)
		if len(pts) == 0 {
			warning = "O YouTube ainda não retornou dados de retenção para este vídeo."
		}
	}
	writeJSON(w, 200, map[string]any{"video_id": videoID, "channel_id": channel, "start_date": start, "end_date": end, "metrics": extractAnalyticsMetrics(totals), "retention": pts, "retention_warning": warning, "has_data": len(totals.Rows) > 0, "source": "youtube_analytics_api", "last_checked_at": time.Now().UTC().Format(time.RFC3339), "note": "Dados do YouTube podem ter atraso de 48h ou mais. Valores de retenção acima de 100% podem resultar de trechos reassistidos."})
}

func (b *Backend) disconnectAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "Use POST")
		return
	}
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	old, oldR, oldA, oldUntil := b.saved, b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil
	b.saved.AnalyticsRefreshDPAPI, b.saved.AnalyticsAccountID, b.saved.AnalyticsChannelID = "", "", ""
	b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil = "", "", time.Time{}
	if e := b.saveLocked(); e != nil {
		b.saved, b.analyticsRefresh, b.analyticsAccess, b.analyticsAccessUntil = old, oldR, oldA, oldUntil
		fail(w, 500, "Falha ao salvar desconexão do Analytics")
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "YouTube Analytics desconectado. As autorizações de publicação e Drive não foram alteradas."})
}
