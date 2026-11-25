package main

import (
	"encoding/json"
	"errors"
	"testing"
)

// fakeSettings makes settings.get answer raw, or fail with err.
func fakeSettings(t *testing.T, raw string, err error) {
	t.Helper()
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(raw), out)
	}
}

func TestLoadSettingsDefaults(t *testing.T) {
	want := settings{Currency: "USD", DefaultCapacity: 50, RetentionMonths: 24}
	for name, c := range map[string]struct {
		raw string
		err error
	}{
		"empty":     {raw: `{}`},
		"host down": {err: errors.New("down")},
	} {
		fakeSettings(t, c.raw, c.err)
		if got := loadSettings(); got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
}

func TestLoadSettingsOutOfRange(t *testing.T) {
	fakeSettings(t, `{"currency":"usd","default_capacity":0,"retention_months":999,"site_url":"ftp://x","schema_slug":"Bad Slug"}`, nil)
	if got, want := loadSettings(), defaultSettings(); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	fakeSettings(t, `{"currency":"EUR","sender_name":" Club ","default_capacity":10,"retention_months":6,"site_url":"https://example.org","schema_slug":"gig","confirmation_text":"Door at 7"}`, nil)
	want := settings{Currency: "EUR", SenderName: "Club", ConfirmationText: "Door at 7", DefaultCapacity: 10, RetentionMonths: 6, SiteURL: "https://example.org", SchemaSlug: "gig"}
	if got := loadSettings(); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

func TestAccepts(t *testing.T) {
	s := defaultSettings()
	if !s.accepts("calendar-event") || !s.accepts("event") || s.accepts("page") {
		t.Error("auto mode must accept calendar-event and event only")
	}
	s.SchemaSlug = "gig"
	if !s.accepts("gig") || s.accepts("calendar-event") {
		t.Error("a named type replaces the defaults")
	}
}
