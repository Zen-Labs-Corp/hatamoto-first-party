package main

import (
	"net/url"
	"strings"
)

const (
	maxURLs         = 25
	strategyMobile  = "mobile"
	strategyDesktop = "desktop"
	strategyBoth    = "both"
)

type settings struct {
	APIKey    string   `json:"api_key"`
	URLs      []string `json:"urls"`
	Strategy  string   `json:"strategy"`
	PublicURL string   `json:"public_url"`
}

// loadSettings reads and normalizes the plugin settings.
func loadSettings() (settings, error) {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return settings{}, err
	}
	s.APIKey = strings.TrimSpace(s.APIKey)
	switch s.Strategy {
	case strategyMobile, strategyDesktop:
	default:
		s.Strategy = strategyBoth
	}
	s.PublicURL = cleanOrigin(s.PublicURL)
	s.URLs = cleanPaths(s.URLs, s.PublicURL)
	return s, nil
}

// cleanOrigin returns scheme://host[:port] of an http(s) address, or "" when
// the address is anything else (a path, query, fragment or credentials too).
func cleanOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// cleanPaths keeps the paths that start with "/", trimmed and deduplicated,
// at most maxURLs of them. A full URL on origin is reduced to its path.
func cleanPaths(in []string, origin string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if rest, ok := strings.CutPrefix(p, origin); ok && origin != "" && (rest == "" || rest[0] == '/' || rest[0] == '?') {
			p = "/" + strings.TrimPrefix(rest, "/")
		}
		if !strings.HasPrefix(p, "/") || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) == maxURLs {
			break
		}
	}
	return out
}

// strategies lists the devices to measure.
func (s settings) strategies() []string {
	if s.Strategy == strategyBoth {
		return []string{strategyMobile, strategyDesktop}
	}
	return []string{s.Strategy}
}
