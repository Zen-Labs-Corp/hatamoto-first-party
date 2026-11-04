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
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // zone names work without a filesystem

	"hatamoto.dev/sdk"
)

const (
	defaultSchemaSlug = "event"
	defaultTimeZone   = "UTC"
	defaultDateFormat = "short"
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// settings are the plugin's own settings, always usable: every field falls
// back to its default.
type settings struct {
	SchemaSlug string
	TimeZone   string
	DateFormat string
}

func defaultSettings() settings {
	return settings{SchemaSlug: defaultSchemaSlug, TimeZone: defaultTimeZone, DateFormat: defaultDateFormat}
}

// loadSettings never fails: an unreadable or invalid setting yields its default.
func loadSettings() settings {
	s := defaultSettings()
	var raw struct {
		SchemaSlug string `json:"schema_slug"`
		TimeZone   string `json:"time_zone"`
		DateFormat string `json:"date_format"`
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
	switch raw.DateFormat {
	case "short", "us", "iso":
		s.DateFormat = raw.DateFormat
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
