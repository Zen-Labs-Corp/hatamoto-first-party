package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const mediaEndpoint = "https://graph.instagram.com/me/media"

// apiError reduces an API error answer to its message.
func apiError(status int, body string) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var flat struct {
		Message string `json:"error_message"`
	}
	msg := ""
	if json.Unmarshal([]byte(body), &e) == nil {
		msg = strings.TrimSpace(e.Error.Message)
	}
	if msg == "" && json.Unmarshal([]byte(body), &flat) == nil {
		msg = strings.TrimSpace(flat.Message)
	}
	if msg == "" {
		msg = "no details"
	}
	return errors.New(redact("HTTP " + strconv.Itoa(status) + ": " + msg))
}

// fetchInstagram returns the latest media of the connected account.
func fetchInstagram(tok *igToken) ([]item, error) {
	q := url.Values{}
	q.Set("fields", "id,caption,media_type,media_url,thumbnail_url,permalink,timestamp")
	q.Set("limit", "50")
	q.Set("access_token", tok.AccessToken)
	res, err := fetch("GET", mediaEndpoint+"?"+q.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	if !ok2xx(res.Status) {
		return nil, apiError(res.Status, res.Body)
	}
	var doc struct {
		Data []struct {
			ID           string `json:"id"`
			Caption      string `json:"caption"`
			MediaType    string `json:"media_type"`
			MediaURL     string `json:"media_url"`
			ThumbnailURL string `json:"thumbnail_url"`
			Permalink    string `json:"permalink"`
			Timestamp    string `json:"timestamp"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Body), &doc); err != nil {
		return nil, errors.New("Instagram answered with something unreadable")
	}
	out := make([]item, 0, len(doc.Data))
	for _, m := range doc.Data {
		if m.ID == "" {
			continue
		}
		image := m.MediaURL
		if strings.EqualFold(m.MediaType, "VIDEO") && m.ThumbnailURL != "" {
			image = m.ThumbnailURL
		}
		out = append(out, item{
			ID:          m.ID,
			Position:    len(out),
			Title:       shorten(m.Caption, 120),
			Excerpt:     strings.TrimSpace(m.Caption),
			URL:         m.Permalink,
			Image:       image,
			Type:        strings.ToLower(m.MediaType),
			PublishedAt: normalizeTime(m.Timestamp),
		})
	}
	return out, nil
}
