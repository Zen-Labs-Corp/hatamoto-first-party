package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
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
	if got.SchemaSlug != "" || got.DateFormat != "short" {
		t.Fatalf("settings = %+v", got)
	}
}

func TestSettingsMapAndContact(t *testing.T) {
	fakeSettings(t, `{"show_map":true,"geocode_contact":" `+strings.Repeat("a", 300)+` "}`, nil)
	got := loadSettings()
	if !got.ShowMap || len(got.GeocodeContact) != maxContactBytes {
		t.Fatalf("settings = %+v", got)
	}
	if defaultSettings().ShowMap {
		t.Error("map on by default")
	}
}

func TestSettingsSlugsAndAccepts(t *testing.T) {
	auto := settings{}
	if !auto.accepts("calendar-event") || !auto.accepts("event") || auto.accepts("post") || auto.accepts("") {
		t.Errorf("auto slugs = %v", auto.slugs())
	}
	one := settings{SchemaSlug: "gig"}
	if !one.accepts("gig") || one.accepts("event") || len(one.slugs()) != 1 {
		t.Errorf("explicit slugs = %v", one.slugs())
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

func TestManifestTimeoutsFitCoreBudget(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Limits struct {
			HookTimeouts map[string]int `json:"hook_timeouts_ms"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	// core cancels the whole builder data round trip at 500 ms
	if v := m.Limits.HookTimeouts["builder.data"]; v <= 0 || v > 500 {
		t.Errorf("builder.data timeout = %d ms", v)
	}
}
