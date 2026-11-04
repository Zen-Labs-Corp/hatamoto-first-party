package main

import (
	"strings"
	"time"
)

// now is replaced in tests.
var now = time.Now

const (
	defaultItems = 9
	maxItems     = 48
)

type settings struct {
	AppID      string
	AppSecret  string
	YouTubeKey string
	ChannelID  string
	PublicURL  string
	Items      int
}

// loadSettings never fails: an unreadable setting set is the same as an empty one.
func loadSettings() settings {
	var raw struct {
		AppID      string `json:"instagram_app_id"`
		AppSecret  string `json:"instagram_app_secret"`
		YouTubeKey string `json:"youtube_api_key"`
		ChannelID  string `json:"youtube_channel_id"`
		PublicURL  string `json:"public_url"`
		Items      int    `json:"items"`
	}
	if err := callHost("settings.get", struct{}{}, &raw); err != nil {
		return settings{Items: defaultItems}
	}
	s := settings{
		AppID:      strings.TrimSpace(raw.AppID),
		AppSecret:  strings.TrimSpace(raw.AppSecret),
		YouTubeKey: strings.TrimSpace(raw.YouTubeKey),
		ChannelID:  strings.TrimSpace(raw.ChannelID),
		PublicURL:  strings.TrimRight(strings.TrimSpace(raw.PublicURL), "/"),
		Items:      raw.Items,
	}
	switch {
	case s.Items < 1:
		s.Items = defaultItems
	case s.Items > maxItems:
		s.Items = maxItems
	}
	return s
}

// redirectURI is the one address used by both the authorize and the exchange step.
func (s settings) redirectURI() string {
	return s.PublicURL + "/p/social-feeds/oauth/callback"
}

// redact removes every given secret and the value of secret-bearing query
// parameters from s, so it is safe to log or store.
func redact(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	for _, key := range []string{"access_token", "client_secret", "key"} {
		s = redactParam(s, key)
	}
	return s
}

// redactParam blanks the value after "<key>=" up to the next separator.
func redactParam(s, key string) string {
	var b strings.Builder
	for {
		i := indexParam(s, key+"=")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		start := i + len(key) + 1
		b.WriteString(s[:start])
		b.WriteString("[redacted]")
		end := strings.IndexAny(s[start:], "&\"' \n\t)")
		if end < 0 {
			return b.String()
		}
		s = s[start+end:]
	}
}

// indexParam finds "<key>=" at the start of s or after a separator, so that
// "monkey=" does not match "key=".
func indexParam(s, needle string) int {
	from := 0
	for {
		i := strings.Index(s[from:], needle)
		if i < 0 {
			return -1
		}
		i += from
		if i == 0 || strings.ContainsRune("?&; \"'(\n\t", rune(s[i-1])) {
			return i
		}
		from = i + 1
	}
}
