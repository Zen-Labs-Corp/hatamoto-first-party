package main

import "strings"

const (
	maxLinks         = 50
	defaultRetention = 24
	maxRetention     = 120
)

// link is one entry of the links setting.
type link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Kind  string `json:"kind"`
}

type settings struct {
	SecretKey       string `json:"secret_key"`
	Links           []link `json:"links"`
	Notify          *bool  `json:"notify"`
	NotifyEmail     string `json:"notify_email"`
	RetentionMonths int    `json:"retention_months"`
}

// notifyOn reports whether the owner email is wanted; unset means on.
func (s settings) notifyOn() bool { return s.Notify == nil || *s.Notify }

// loadSettings reads the settings through the host and normalises them.
func loadSettings() (settings, error) {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return settings{}, err
	}
	s.SecretKey = strings.TrimSpace(s.SecretKey)
	s.NotifyEmail = strings.TrimSpace(s.NotifyEmail)
	if s.RetentionMonths < 1 || s.RetentionMonths > maxRetention {
		s.RetentionMonths = defaultRetention
	}
	if len(s.Links) > maxLinks {
		s.Links = s.Links[:maxLinks]
	}
	for i := range s.Links {
		if s.Links[i].Kind != "donation" {
			s.Links[i].Kind = "payment"
		}
	}
	return s, nil
}
