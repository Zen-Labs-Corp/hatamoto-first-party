package parity

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

type apiItem struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Text      string    `json:"text"`
	By        string    `json:"by"`
	Points    int       `json:"points"`
	Comments  int       `json:"comments"`
	CreatedAt time.Time `json:"created_at"`
}

type apiUser struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Karma     int       `json:"karma"`
	About     string    `json:"about"`
}

type apiFeed struct {
	Items   []apiItem `json:"items"`
	Page    int       `json:"page"`
	HasMore bool      `json:"has_more"`
}

const maxName = 32 // bytes of a username

func toItem(s pubStory) apiItem {
	return apiItem{ID: strconv.FormatInt(s.ItemNo, 10), Type: s.Kind, Title: s.Title, URL: s.URL, Text: s.Body, By: s.By,
		Points: s.Points, Comments: s.CommentCount, CreatedAt: s.CreatedAt}
}

// jsonOK is a 200 answer that caches for 30 seconds.
func jsonOK(v any) sdk.HTTPResponse {
	body, err := json.Marshal(v)
	if err != nil {
		return core.JSONError(500, "internal")
	}
	return sdk.HTTPResponse{Status: 200, Body: string(body), Headers: map[string]string{
		"Content-Type": "application/json; charset=utf-8", "Cache-Control": "public, max-age=30",
	}}
}

// handleAPIFeed is GET /api/feed/{name}?p=N.
func handleAPIFeed(q *core.Request) (sdk.HTTPResponse, error) {
	page := parsePage(firstQuery(q, "p"))
	rows, more, ok, err := loadPub(q.PathValue("name"), page)
	if !ok {
		return core.JSONError(404, "not found"), nil
	}
	if err != nil {
		return core.JSONError(503, "unavailable"), nil
	}
	out := apiFeed{Items: make([]apiItem, 0, len(rows)), Page: page, HasMore: more}
	for _, s := range rows {
		out.Items = append(out.Items, toItem(s))
	}
	return jsonOK(out), nil
}

// handleAPIItem is GET /api/item/{id}: a live story by its item number.
func handleAPIItem(q *core.Request) (sdk.HTTPResponse, error) {
	no, ok := parseItemNo(q.PathValue("id"))
	if !ok {
		return core.JSONError(404, "not found"), nil
	}
	var s pubStory
	if err := core.QueryJSON(sqlPubItem, &s, no); err != nil {
		if errors.Is(err, core.ErrNoRows) {
			return core.JSONError(404, "not found"), nil
		}
		return core.JSONError(503, "unavailable"), nil
	}
	return jsonOK(toItem(s)), nil
}

// handleAPIUser is GET /api/user/{name}: the public profile, nothing else.
func handleAPIUser(q *core.Request) (sdk.HTTPResponse, error) {
	name := q.PathValue("name")
	if name == "" || len(name) > maxName {
		return core.JSONError(404, "not found"), nil
	}
	u, err := core.UserByName(name)
	if err != nil {
		return core.JSONError(503, "unavailable"), nil
	}
	if u == nil {
		return core.JSONError(404, "not found"), nil
	}
	return jsonOK(apiUser{Name: u.Username, CreatedAt: u.CreatedAt, Karma: u.Karma, About: u.About}), nil
}
