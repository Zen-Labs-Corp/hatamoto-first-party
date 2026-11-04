package main

import "strings"

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
	s.URLs = cleanPaths(s.URLs)
	s.PublicURL = strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	if !strings.HasPrefix(s.PublicURL, "https://") && !strings.HasPrefix(s.PublicURL, "http://") {
		s.PublicURL = ""
	}
	return s, nil
}

// cleanPaths keeps the paths that start with "/", trimmed and deduplicated,
// at most maxURLs of them.
func cleanPaths(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
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
