package main

import "strings"

// settings are the owner's plugin settings (settings.get merges the defaults).
type settings struct {
	ClientID      string `json:"github_client_id"`
	ClientSecret  string `json:"github_client_secret"`
	ReviewEmail   string `json:"review_email"`
	PublicURL     string `json:"public_url"`
	TermsMarkdown string `json:"terms_markdown"`
	ListingKey    string `json:"listing_key"`
}

func loadSettings() (settings, error) {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return settings{}, err
	}
	s.PublicURL = strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	return s, nil
}

// cfg reads a manifest config value such as the GitHub endpoints.
func cfg(k string) string {
	v, _ := config(k)
	return v
}
