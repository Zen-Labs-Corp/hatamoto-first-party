package main

import "strings"

// settings are the owner's options, already clamped to their allowed range.
type settings struct {
	Exclude     []string `json:"exclude"`
	RecheckDays int      `json:"recheck_days"`
	TimeoutMS   int      `json:"timeout_ms"`
}

const (
	defaultRecheckDays = 7
	defaultTimeoutMS   = 5000
)

func defaultSettings() settings {
	return settings{RecheckDays: defaultRecheckDays, TimeoutMS: defaultTimeoutMS}
}

// loadSettings never fails: a host error or an out of range value gives the
// default, so a hook keeps working with a broken settings store.
func loadSettings() settings {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		logf("warn", "settings unavailable, using defaults: %v", err)
		return defaultSettings()
	}
	if s.RecheckDays < 1 || s.RecheckDays > 90 {
		s.RecheckDays = defaultRecheckDays
	}
	if s.TimeoutMS < 1000 || s.TimeoutMS > 10000 {
		s.TimeoutMS = defaultTimeoutMS
	}
	kept := s.Exclude[:0:0]
	for _, p := range s.Exclude {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	s.Exclude = kept
	return s
}
