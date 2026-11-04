package main

import "strings"

const (
	defaultMinRating = 4
	defaultMaxItems  = 6
	maxItemsCap      = 48
)

// settings are the owner's plugin settings, clamped. They hold secrets: never
// log or return them.
type settings struct {
	GoogleKey   string
	GooglePlace string
	TPKey       string
	TPUnit      string
	MinRating   int
	MaxItems    int
}

// googleOn reports whether Google is set up: key and place ID both present.
func (s settings) googleOn() bool { return s.GoogleKey != "" && s.GooglePlace != "" }

// tpOn reports whether Trustpilot is set up: key and business unit ID both present.
func (s settings) tpOn() bool { return s.TPKey != "" && s.TPUnit != "" }

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	return min(max(v, lo), hi)
}

// loadSettings reads the settings; a host error returns the error and the
// defaults, so callers can fail open with nothing configured.
func loadSettings() (settings, error) {
	var raw struct {
		GoogleKey   string  `json:"google_api_key"`
		GooglePlace string  `json:"google_place_id"`
		TPKey       string  `json:"trustpilot_api_key"`
		TPUnit      string  `json:"trustpilot_business_unit_id"`
		MinRating   float64 `json:"min_rating"`
		MaxItems    float64 `json:"max_items"`
	}
	err := callHost("settings.get", struct{}{}, &raw)
	return settings{
		GoogleKey:   strings.TrimSpace(raw.GoogleKey),
		GooglePlace: strings.TrimSpace(raw.GooglePlace),
		TPKey:       strings.TrimSpace(raw.TPKey),
		TPUnit:      strings.TrimSpace(raw.TPUnit),
		MinRating:   clamp(int(raw.MinRating), 1, 5, defaultMinRating),
		MaxItems:    clamp(int(raw.MaxItems), 1, maxItemsCap, defaultMaxItems),
	}, err
}
