// Command event-tickets is a Hatamoto plugin. Free tickets, RSVP, paid tickets,
// attendees, check-in, seating and event emails for the event-calendar plugin.
//
// The logic lives in plain Go files and the //go:wasmexport lines in
// exports_wasip1.go, so everything but the exports builds and tests on the
// host. Host calls go through the callHost seam.
package main

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	legacySchemaSlug = "event"
	defaultNewSlug   = "calendar-event"

	defaultCapacity = 50
	defaultCurrency = "USD"
	defaultRetain   = 24
)

var (
	slugRe     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
	siteURLRe  = regexp.MustCompile(`^https?://[^\s/]+$`)
)

// now is the clock seam of tests.
var now = time.Now

// settings are the plugin's own settings, always usable: every field falls
// back to its default. An empty SchemaSlug is auto mode: the calendar event
// type and the older event type are both read.
type settings struct {
	Currency         string
	SenderName       string
	ConfirmationText string
	DefaultCapacity  int
	RetentionMonths  int
	SiteURL          string
	SchemaSlug       string
}

func defaultSettings() settings {
	return settings{Currency: defaultCurrency, DefaultCapacity: defaultCapacity, RetentionMonths: defaultRetain}
}

// slugs are the content types events are read from.
func (s settings) slugs() []string {
	if s.SchemaSlug != "" {
		return []string{s.SchemaSlug}
	}
	return []string{defaultNewSlug, legacySchemaSlug}
}

// accepts reports whether entities of the given content type are events.
func (s settings) accepts(slug string) bool { return slices.Contains(s.slugs(), slug) }

// loadSettings never fails: an unreadable or out-of-range setting yields its default.
func loadSettings() settings {
	s := defaultSettings()
	var raw struct {
		Currency         string `json:"currency"`
		SenderName       string `json:"sender_name"`
		ConfirmationText string `json:"confirmation_text"`
		DefaultCapacity  int    `json:"default_capacity"`
		RetentionMonths  int    `json:"retention_months"`
		SiteURL          string `json:"site_url"`
		SchemaSlug       string `json:"schema_slug"`
	}
	if err := callHost(sdk.FnSettingsGet, struct{}{}, &raw); err != nil {
		return s
	}
	if currencyRe.MatchString(raw.Currency) {
		s.Currency = raw.Currency
	}
	if n := len([]rune(strings.TrimSpace(raw.SenderName))); n <= 80 {
		s.SenderName = strings.TrimSpace(raw.SenderName)
	}
	if len([]rune(raw.ConfirmationText)) <= 2000 {
		s.ConfirmationText = strings.TrimSpace(raw.ConfirmationText)
	}
	if raw.DefaultCapacity >= 1 && raw.DefaultCapacity <= 100000 {
		s.DefaultCapacity = raw.DefaultCapacity
	}
	if raw.RetentionMonths >= 1 && raw.RetentionMonths <= 120 {
		s.RetentionMonths = raw.RetentionMonths
	}
	if u := strings.TrimSpace(raw.SiteURL); len(u) <= 200 && siteURLRe.MatchString(u) {
		s.SiteURL = u
	}
	if slug := strings.TrimSpace(raw.SchemaSlug); slugRe.MatchString(slug) {
		s.SchemaSlug = slug
	}
	return s
}
