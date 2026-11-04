package main

import (
	"sort"
	"strings"
	"time"
)

type entity struct {
	Path        string `json:"path"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
}

const (
	listLimit = 50
	listPages = 2
)

// listPublished returns the first pages of published entities. The host lists
// by creation date, so callers sort by published_at themselves.
func listPublished() ([]entity, error) {
	var all []entity
	cursor := ""
	for range listPages {
		args := map[string]any{"status": "published", "limit": listLimit}
		if cursor != "" {
			args["cursor"] = cursor
		}
		var res struct {
			Items      []entity `json:"items"`
			NextCursor string   `json:"next_cursor"`
		}
		if err := callHost("entities.list", args, &res); err != nil {
			return all, err
		}
		all = append(all, res.Items...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return all, nil
}

// pickTargets is the configured list, else "/" and the newest published paths.
func pickTargets(s settings, ents []entity) []string {
	if len(s.URLs) > 0 {
		return s.URLs
	}
	sorted := append([]entity(nil), ents...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return publishedTime(sorted[i]).After(publishedTime(sorted[j]))
	})
	out := []string{"/"}
	seen := map[string]bool{"/": true}
	for _, e := range sorted {
		if len(out) == maxURLs {
			break
		}
		if !strings.HasPrefix(e.Path, "/") || seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		out = append(out, e.Path)
	}
	return out
}

func publishedTime(e entity) time.Time {
	t, _ := time.Parse(time.RFC3339, e.PublishedAt)
	return t
}

// siteOrigin is the public_url setting, else the origin core put in an entity
// url (the path is stripped). ok is false when neither exists.
func siteOrigin(s settings, ents []entity) (string, bool) {
	if s.PublicURL != "" {
		return s.PublicURL, true
	}
	for _, e := range ents {
		if e.Path == "" || e.URL == e.Path || !strings.HasSuffix(e.URL, e.Path) {
			continue
		}
		o := strings.TrimRight(strings.TrimSuffix(e.URL, e.Path), "/")
		if strings.HasPrefix(o, "https://") || strings.HasPrefix(o, "http://") {
			return o, true
		}
	}
	return "", false
}

// resolveTargets returns the pages to track and the site origin. It only asks
// the host for entities when the settings do not already answer both.
func resolveTargets(s settings) (targets []string, origin string, ok bool, err error) {
	var ents []entity
	if len(s.URLs) == 0 || s.PublicURL == "" {
		if ents, err = listPublished(); err != nil && len(ents) == 0 {
			return nil, "", false, err
		}
		err = nil
	}
	origin, ok = siteOrigin(s, ents)
	return pickTargets(s, ents), origin, ok, nil
}
