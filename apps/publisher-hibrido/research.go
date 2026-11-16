package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type videoEvidence struct {
	Title     string `json:"title"`
	Channel   string `json:"channel"`
	Published string `json:"published_at"`
	Views     int64  `json:"views"`
	Link      string `json:"url"`
}

type youtubeResearch struct {
	Topic     string          `json:"topic"`
	Videos    []videoEvidence `json:"videos"`
	CheckedAt string          `json:"checked_at"`
	Caveat    string          `json:"caveat"`
}

func (a *App) researchYouTube(topic string) (youtubeResearch, error) {
	topic = strings.TrimSpace(topic)
	if len(topic) < 3 || len(topic) > 140 {
		return youtubeResearch{}, errors.New("Informe um assunto entre 3 e 140 caracteres")
	}
	token, err := a.googleAccessToken()
	if err != nil {
		return youtubeResearch{}, fmt.Errorf("Google/YouTube indisponível: %w", err)
	}
	query := url.Values{"part": {"snippet"}, "type": {"video"}, "q": {topic}, "maxResults": {"8"}, "relevanceLanguage": {"pt"}, "order": {"relevance"}, "safeSearch": {"moderate"}}
	req, _ := http.NewRequest("GET", "https://www.googleapis.com/youtube/v3/search?"+query.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return youtubeResearch{}, errors.New("Não foi possível pesquisar no YouTube: falha de conexão")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 401 {
			a.Lock()
			a.googleChecked = "erro: a sessão Google expirou; reconecte"
			a.Unlock()
		}
		return youtubeResearch{}, fmt.Errorf("O YouTube não autorizou a pesquisa (HTTP %d). Confira a conexão Google, as APIs e a cota diária", resp.StatusCode)
	}
	var sr struct {
		Items []struct {
			ID struct {
				VideoID string `json:"videoId"`
			} `json:"id"`
			Snippet struct {
				Title        string `json:"title"`
				ChannelTitle string `json:"channelTitle"`
				PublishedAt  string `json:"publishedAt"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256*1024)).Decode(&sr); err != nil {
		return youtubeResearch{}, errors.New("Resposta incompleta da pesquisa YouTube")
	}
	data := youtubeResearch{Topic: topic, CheckedAt: time.Now().UTC().Format(time.RFC3339), Caveat: "Amostra pública pequena, não representa toda a plataforma. Visualizações não revelam CTR, retenção nem garantem resultados futuros."}
	if len(sr.Items) == 0 {
		return data, nil
	}
	ids := make([]string, 0, len(sr.Items))
	entries := map[string]videoEvidence{}
	for _, item := range sr.Items {
		id := item.ID.VideoID
		if !drivePattern.MatchString(id) || len(id) > 25 {
			continue
		}
		ids = append(ids, id)
		entries[id] = videoEvidence{Title: item.Snippet.Title, Channel: item.Snippet.ChannelTitle, Published: item.Snippet.PublishedAt, Link: "https://www.youtube.com/watch?v=" + id}
	}
	if len(ids) == 0 {
		return data, nil
	}
	q := url.Values{"part": {"statistics"}, "id": {strings.Join(ids, ",")}}
	r2, _ := http.NewRequest("GET", "https://www.googleapis.com/youtube/v3/videos?"+q.Encode(), nil)
	r2.Header.Set("Authorization", "Bearer "+token)
	resp2, err := httpClient.Do(r2)
	if err == nil && resp2.StatusCode == 200 {
		var stats struct {
			Items []struct {
				ID         string `json:"id"`
				Statistics struct {
					ViewCount string `json:"viewCount"`
				} `json:"statistics"`
			} `json:"items"`
		}
		_ = json.NewDecoder(io.LimitReader(resp2.Body, 256*1024)).Decode(&stats)
		for _, s := range stats.Items {
			entry, ok := entries[s.ID]
			if ok {
				entry.Views, _ = strconv.ParseInt(s.Statistics.ViewCount, 10, 64)
				entries[s.ID] = entry
			}
		}
	}
	if resp2 != nil {
		_ = resp2.Body.Close()
	}
	for _, id := range ids {
		data.Videos = append(data.Videos, entries[id])
	}
	return data, nil
}

func (a *App) youtubeResearchHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Topic string `json:"topic"`
	}
	if err := readJSON(r, &request); err != nil {
		apiErr(w, 400, "Pesquisa inválida")
		return
	}
	evidence, err := a.researchYouTube(request.Topic)
	if err != nil {
		apiErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, evidence)
}
