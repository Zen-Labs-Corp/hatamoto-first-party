package main

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func withSettings(t *testing.T, raw string) settings {
	t.Helper()
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("host call %q", fn)
		}
		return json.Unmarshal([]byte(raw), out)
	}
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSettingsDefaults(t *testing.T) {
	s := withSettings(t, `{}`)
	if s.GraphVersion != "v26.0" || s.EventName != "Lead" || s.EmailField != "email" ||
		s.PhoneField != "phone" || s.NameField != "name" {
		t.Fatalf("defaults = %+v", s)
	}
	if s.Forms != nil || s.ConsentField != "" || s.TestEventCode != "" {
		t.Fatalf("optional = %+v", s)
	}
}

func TestSettingsTrimmed(t *testing.T) {
	s := withSettings(t, `{"pixel_id":" 12345 ","access_token":" tok ","graph_version":" v25.0 ","site_url":" https://example.com/ ",
		"event_name":" Lead ","forms":[" contact ","","  "," quote"],"email_field":" mail ","consent_field":" ok ","test_event_code":" TEST1 "}`)
	if s.PixelID != "12345" || s.AccessToken != "tok" || s.GraphVersion != "v25.0" || s.SiteURL != "https://example.com" ||
		s.EventName != "Lead" || s.EmailField != "mail" || s.ConsentField != "ok" || s.TestEventCode != "TEST1" {
		t.Fatalf("settings = %+v", s)
	}
	if !slices.Equal(s.Forms, []string{"contact", "quote"}) {
		t.Fatalf("forms = %q", s.Forms)
	}
}

func TestSettingsNullForms(t *testing.T) {
	s := withSettings(t, `{"forms":null}`)
	if len(s.Forms) != 0 || !s.allowsForm("anything") {
		t.Fatalf("forms = %v", s.Forms)
	}
}

func TestSettingsHostError(t *testing.T) {
	callHost = func(string, any, any) error { return errors.New("host down") }
	if _, err := loadSettings(); err == nil {
		t.Fatal("want the host error")
	}
}

func TestSettingsMissing(t *testing.T) {
	if got := withSettings(t, `{}`).missing(); !slices.Equal(got, []string{"pixel_id", "access_token"}) {
		t.Fatalf("missing = %v", got)
	}
	if got := withSettings(t, `{"pixel_id":"12345","access_token":"  "}`).missing(); !slices.Equal(got, []string{"access_token"}) {
		t.Fatalf("missing = %v", got)
	}
	if got := withSettings(t, `{"pixel_id":"12345","access_token":"t"}`).missing(); len(got) != 0 {
		t.Fatalf("missing = %v", got)
	}
}

func TestSettingsAllowsForm(t *testing.T) {
	s := withSettings(t, `{"forms":["contact"]}`)
	if !s.allowsForm("contact") || s.allowsForm("quote") {
		t.Fatal("allowsForm with a list")
	}
	if !withSettings(t, `{}`).allowsForm("quote") {
		t.Fatal("no list means all forms")
	}
}

func TestSettingsEndpoint(t *testing.T) {
	s := withSettings(t, `{"pixel_id":"12345","access_token":"secret-token"}`)
	const want = "https://graph.facebook.com/v26.0/12345/events"
	if got := s.endpoint(); got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}
