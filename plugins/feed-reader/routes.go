package main

import (
	"encoding/json"
	"net/http"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// latestItems is how many items the overview lists.
const latestItems = 20

// statusPending is shown for a configured feed that has not been fetched yet.
const statusPending = "pending"

// statusInvalidURL is shown for a settings entry whose URL cannot be fetched.
const statusInvalidURL = "invalid URL"

func init() {
	guest.OnHTTP(handleRoute)
}

func handleRoute(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /overview":
		return overview()
	case "POST /fetch-now":
		results, grant := refreshReport(true)
		if results == nil {
			results = []feedResult{}
		}
		if grant != "" {
			return reply(http.StatusOK, map[string]any{"results": results, "needs_grant": grant})
		}
		return reply(http.StatusOK, map[string]any{"results": results})
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

func reply(status int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{Status: http.StatusInternalServerError, Headers: jsonHeader, Body: `{"error":"internal error"}`}, nil
	}
	return sdk.HTTPResponse{Status: status, Headers: jsonHeader, Body: string(b)}, nil
}

var jsonHeader = map[string]string{"Content-Type": "application/json"}

// internalError logs a fixed phrase and answers 500 without details.
func internalError(what string) (sdk.HTTPResponse, error) {
	logf("error", "feed-reader: %s", what)
	return reply(http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

// needsGrant answers 200 with the permission the owner has not granted yet,
// so the admin page can explain it instead of showing an error.
func needsGrant(permission string) (sdk.HTTPResponse, error) {
	return reply(http.StatusOK, map[string]any{
		"feeds": []overviewFeed{}, "latest": []overviewItem{}, "needs_grant": permission,
	})
}

type overviewFeed struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Group     int    `json:"group"`
	LastFetch string `json:"last_fetch"`
	Status    string `json:"status"`
	Items     int    `json:"items"`
}

type overviewItem struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Source    string `json:"source"`
	Published string `json:"published_at"`
	Excerpt   string `json:"excerpt"`
}

// overview lists the configured feeds with their stored state and the latest
// items. It only reads.
func overview() (sdk.HTTPResponse, error) {
	cfg, skipped, err := readFeeds()
	if err != nil {
		if c, ok := guest.CapabilityDenied(err); ok {
			return needsGrant(c)
		}
		return internalError("settings unavailable")
	}
	rows, err := feedRows()
	if err != nil {
		if c, ok := guest.CapabilityDenied(err); ok {
			return needsGrant(c)
		}
		return internalError("cannot read the feed state")
	}
	state := make(map[string]feedRow, len(rows))
	for _, r := range rows {
		state[r.Key] = r
	}
	feeds := make([]overviewFeed, 0, len(cfg))
	for _, f := range cfg {
		o := overviewFeed{Name: f.Name, URL: f.URL, Group: f.Group, Status: statusPending}
		if r, ok := state[f.Key]; ok {
			o.Items = r.ItemCount
			if !r.LastAttempt.IsZero() {
				o.LastFetch = r.LastAttempt.UTC().Format(time.RFC3339)
			}
			if r.Status != "" {
				o.Status = r.Status
			}
		}
		feeds = append(feeds, o)
	}
	for _, s := range skipped {
		name := s.Name
		if name == "" {
			name = s.URL
		}
		feeds = append(feeds, overviewFeed{Name: name, URL: s.URL, Status: statusInvalidURL})
	}
	items, err := itemsFor("items", latestItems)
	if err != nil {
		if c, ok := guest.CapabilityDenied(err); ok {
			return needsGrant(c)
		}
		return internalError("cannot read the items")
	}
	latest := make([]overviewItem, 0, len(items))
	for _, it := range items {
		o := overviewItem{Title: it.Title, URL: it.Link, Source: it.FeedName, Excerpt: it.Excerpt}
		if !it.Published.IsZero() {
			o.Published = it.Published.UTC().Format(time.RFC3339)
		}
		latest = append(latest, o)
	}
	return reply(http.StatusOK, map[string]any{"feeds": feeds, "latest": latest})
}
