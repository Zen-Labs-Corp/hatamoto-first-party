package main

import (
	"regexp"
	"strings"
)

var (
	graphVersionRe = regexp.MustCompile(`^v[0-9]{2}\.[0-9]$`)
	pixelIDRe      = regexp.MustCompile(`^[0-9]{5,20}$`)
)

// settings is the plugin's configuration as the host returns it.
type settings struct {
	PixelID       string   `json:"pixel_id"`
	AccessToken   string   `json:"access_token"`
	GraphVersion  string   `json:"graph_version"`
	SiteURL       string   `json:"site_url"`
	EventName     string   `json:"event_name"`
	Forms         []string `json:"forms"`
	EmailField    string   `json:"email_field"`
	PhoneField    string   `json:"phone_field"`
	NameField     string   `json:"name_field"`
	ConsentField  string   `json:"consent_field"`
	TestEventCode string   `json:"test_event_code"`
}

// loadSettings reads the settings, trims them and fills the defaults the host
// may not have merged.
func loadSettings() (settings, error) {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return settings{}, err
	}
	s.PixelID = strings.TrimSpace(s.PixelID)
	s.AccessToken = strings.TrimSpace(s.AccessToken)
	s.GraphVersion = orDefault(s.GraphVersion, "v26.0")
	s.SiteURL = strings.TrimRight(strings.TrimSpace(s.SiteURL), "/")
	// A malformed value would end up in the request URL; treat it as not set.
	if !graphVersionRe.MatchString(s.GraphVersion) {
		s.GraphVersion = ""
	}
	if !pixelIDRe.MatchString(s.PixelID) {
		s.PixelID = ""
	}
	s.EventName = orDefault(s.EventName, "Lead")
	s.EmailField = orDefault(s.EmailField, "email")
	s.PhoneField = orDefault(s.PhoneField, "phone")
	s.NameField = orDefault(s.NameField, "name")
	s.ConsentField = strings.TrimSpace(s.ConsentField)
	s.TestEventCode = strings.TrimSpace(s.TestEventCode)
	var forms []string
	for _, f := range s.Forms {
		if f = strings.TrimSpace(f); f != "" {
			forms = append(forms, f)
		}
	}
	s.Forms = forms
	return s, nil
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

// missing names the required settings that are empty.
func (s settings) missing() []string {
	var m []string
	if s.PixelID == "" {
		m = append(m, "pixel_id")
	}
	if s.AccessToken == "" {
		m = append(m, "access_token")
	}
	if s.GraphVersion == "" {
		m = append(m, "graph_version")
	}
	return m
}

// allowsForm reports whether events are sent for the form; no list means all.
func (s settings) allowsForm(slug string) bool {
	if len(s.Forms) == 0 {
		return true
	}
	for _, f := range s.Forms {
		if f == slug {
			return true
		}
	}
	return false
}

// endpoint is the Conversions API URL. The token is not part of it.
func (s settings) endpoint() string {
	return "https://graph.facebook.com/" + s.GraphVersion + "/" + s.PixelID + "/events"
}
