package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// fakeSettings answers settings.get with raw, or fails with hostErr.
func fakeSettings(t *testing.T, raw string, hostErr error) {
	t.Helper()
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		if hostErr != nil {
			return hostErr
		}
		return json.Unmarshal([]byte(raw), out)
	}
}

func TestSettingsDefaults(t *testing.T) {
	fakeSettings(t, `{}`, nil)
	if got, want := loadSettings(), defaultSettings(); got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}

func TestSettingsValues(t *testing.T) {
	fakeSettings(t, `{"schema_slug":" gig ","time_zone":"Europe/Berlin","date_format":"iso"}`, nil)
	got := loadSettings()
	if got.SchemaSlug != "gig" || got.TimeZone != "Europe/Berlin" || got.DateFormat != "iso" {
		t.Fatalf("settings = %+v", got)
	}
}

func TestSettingsBadSlugAndFormat(t *testing.T) {
	fakeSettings(t, `{"schema_slug":"Bad Slug!","date_format":"weird"}`, nil)
	got := loadSettings()
	if got.SchemaSlug != "event" || got.DateFormat != "short" {
		t.Fatalf("settings = %+v", got)
	}
}

func TestSettingsHostErrorGivesDefaults(t *testing.T) {
	fakeSettings(t, "", errors.New("host down"))
	if got, want := loadSettings(), defaultSettings(); got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}

func TestLocationFallsBackToUTC(t *testing.T) {
	if loc := (settings{TimeZone: "Not/AZone"}).location(); loc.String() != "UTC" {
		t.Fatalf("bad zone: %v", loc)
	}
	if loc := (settings{TimeZone: "Europe/Berlin"}).location(); loc.String() != "Europe/Berlin" {
		t.Fatalf("good zone: %v", loc)
	}
}

func TestLocationIsCached(t *testing.T) {
	s := settings{TimeZone: "Europe/London"}
	a, b := s.location(), s.location()
	if a != b {
		t.Error("location loaded twice for one zone name")
	}
	if a.String() != "Europe/London" {
		t.Errorf("location = %s", a)
	}
	if bad := (settings{TimeZone: "No/Such_Zone"}); bad.location() != time.UTC {
		t.Error("unknown zone is not UTC")
	}
}
