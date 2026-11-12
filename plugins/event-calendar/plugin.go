// Command event-calendar is a Hatamoto plugin. Events with dates, venue and
// ticket link: upcoming and past lists for the page builder, an iCal feed and
// Event structured data.
//
// The logic lives in plain Go files and the //go:wasmexport lines in
// exports_wasip1.go, so everything but the exports builds and tests on the
// host. Host calls go through the callHost seam.
package main

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // zone names work without a filesystem
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

const (
	legacySchemaSlug  = "event"
	defaultNewSlug    = "calendar-event"
	defaultTimeZone   = "UTC"
	defaultDateFormat = "short"
	maxContactBytes   = 200
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// settings are the plugin's own settings, always usable: every field falls
// back to its default. An empty SchemaSlug is auto mode: the calendar event
// type and the older event type are both read.
type settings struct {
	SchemaSlug     string
	TimeZone       string
	DateFormat     string
	ShowMap        bool
	GeocodeContact string
}

func defaultSettings() settings {
	return settings{TimeZone: defaultTimeZone, DateFormat: defaultDateFormat}
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

// loadSettings never fails: an unreadable or invalid setting yields its default.
func loadSettings() settings {
	s := defaultSettings()
	var raw struct {
		SchemaSlug string `json:"schema_slug"`
		TimeZone   string `json:"time_zone"`
		DateFormat string `json:"date_format"`
		ShowMap    bool   `json:"show_map"`
		Contact    string `json:"geocode_contact"`
	}
	if err := callHost(sdk.FnSettingsGet, struct{}{}, &raw); err != nil {
		return s
	}
	if slug := strings.TrimSpace(raw.SchemaSlug); slugRe.MatchString(slug) {
		s.SchemaSlug = slug
	}
	if tz := strings.TrimSpace(raw.TimeZone); tz != "" {
		s.TimeZone = tz
	}
	s.ShowMap = raw.ShowMap
	s.GeocodeContact = capBytes(strings.TrimSpace(raw.Contact), maxContactBytes)
	switch raw.DateFormat {
	case "short", "us", "iso":
		s.DateFormat = raw.DateFormat
	}
	return s
}

// capBytes shortens s to at most n bytes on a rune boundary.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

var (
	locMu    sync.Mutex
	locCache = map[string]*time.Location{}
)

// location is the configured zone, UTC when the name is unknown. Loading a
// zone parses the embedded database, so each name is loaded once.
func (s settings) location() *time.Location {
	locMu.Lock()
	defer locMu.Unlock()
	if loc, ok := locCache[s.TimeZone]; ok {
		return loc
	}
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	locCache[s.TimeZone] = loc
	return loc
}
