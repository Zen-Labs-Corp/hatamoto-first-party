package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

const (
	maxFeeds        = 20
	defaultMaxItems = 50
	maxItemsCap     = 200
	maxGroup        = 7
)

// feedCfg is one configured feed after validation. Key is the stable id the
// store uses, so editing a name or group keeps the feed's items.
type feedCfg struct {
	Key      string
	Name     string
	URL      string
	MaxItems int
	Group    int // 0 = no group
}

// loadFeeds reads and cleans the feeds setting. Invalid entries are skipped
// and a missing or null list is empty, so a bad setting never breaks a run.
func loadFeeds() ([]feedCfg, error) {
	var s struct {
		Feeds []struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			MaxItems int    `json:"max_items"`
			Group    int    `json:"group"`
		} `json:"feeds"`
	}
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return nil, err
	}
	var out []feedCfg
	seen := map[string]bool{}
	for _, f := range s.Feeds {
		u := strings.TrimSpace(f.URL)
		if !validFeedURL(u) || seen[u] {
			continue
		}
		seen[u] = true
		if f.MaxItems < 1 {
			f.MaxItems = defaultMaxItems
		}
		f.MaxItems = min(f.MaxItems, maxItemsCap)
		if f.Group < 1 || f.Group > maxGroup {
			f.Group = 0
		}
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = u
		}
		sum := sha256.Sum256([]byte(u))
		out = append(out, feedCfg{Key: hex.EncodeToString(sum[:]), Name: clipRunes(name, 80), URL: u, MaxItems: f.MaxItems, Group: f.Group})
		if len(out) == maxFeeds {
			break
		}
	}
	return out, nil
}

// validFeedURL accepts absolute http and https URLs with a host and no
// credentials.
func validFeedURL(raw string) bool {
	if raw == "" || len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// clipRunes cuts s to at most n runes.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
