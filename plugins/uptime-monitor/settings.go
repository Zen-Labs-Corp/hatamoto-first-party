package main

import (
	"net/mail"
	"strings"
)

// settings are the owner's options with the defaults the host does not merge
// for us applied.
type settings struct {
	AlertEmails   []string `json:"alert_emails"`
	FailThreshold int      `json:"fail_threshold"`
	SSLWarnDays   []int    `json:"ssl_warn_days"`
	HeartbeatURL  string   `json:"heartbeat_url"`
	// InvalidEmails are the alert_emails entries dropped for not being plain
	// addresses; never part of the stored settings.
	InvalidEmails []string `json:"-"`
	// Fallback: the settings could not be read and these are the defaults, not
	// the owner's choices.
	Fallback bool `json:"-"`
}

const defaultFailThreshold = 2

var defaultSSLWarnDays = []int{21, 7, 1}

func defaultSettings() settings {
	return settings{FailThreshold: defaultFailThreshold, SSLWarnDays: append([]int(nil), defaultSSLWarnDays...)}
}

// loadSettings never fails: a host error or an out of range value gives the
// default, so a job keeps working with a broken settings store.
func loadSettings() settings {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		logf("warn", "uptime-monitor: settings unavailable, using defaults code=%s", hostErrCode(err))
		s = defaultSettings()
		s.Fallback = true
		return s
	}
	s.AlertEmails, s.InvalidEmails = splitEmails(s.AlertEmails)
	if s.FailThreshold < 1 || s.FailThreshold > 10 {
		s.FailThreshold = defaultFailThreshold
	}
	if s.SSLWarnDays == nil {
		s.SSLWarnDays = append([]int(nil), defaultSSLWarnDays...)
	}
	return s
}

// splitEmails separates the bare addresses mail.send accepts from the rest.
// The host does not enforce the settings format, and one bad entry would make
// every alert mail fail.
func splitEmails(all []string) (valid, invalid []string) {
	for _, e := range all {
		if a, err := mail.ParseAddress(e); err == nil && a.Address == e && !strings.ContainsAny(e, "\r\n") {
			valid = append(valid, e)
		} else {
			invalid = append(invalid, e)
		}
	}
	return valid, invalid
}
