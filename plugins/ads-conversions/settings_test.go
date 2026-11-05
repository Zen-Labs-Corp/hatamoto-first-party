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
	if s.EmailField != "email" || s.PhoneField != "phone" || s.TikTok.Event != "SubmitForm" || s.LinkedIn.Version != "202610" {
		t.Fatalf("defaults = %+v", s)
	}
	if s.Forms != nil || s.ConsentField != "" || s.Value != nil || s.Google.Enabled || s.TikTok.Enabled || s.LinkedIn.Enabled {
		t.Fatalf("optional = %+v", s)
	}
}

func TestSettingsTrimmedAndCleaned(t *testing.T) {
	s := withSettings(t, `{"public_url":" https://example.com/ ","notify_email":" a@example.com ","forms":[" contact ","","  "," quote"],
		"email_field":" mail ","consent_field":" ok ","default_country_code":" 49 ","value":12.5,"currency":" eur ",
		"google_enabled":true,"google_customer_id":"123-456-7890","google_login_customer_id":" 111-222-3333 ","google_conversion_action_id":" 987 ",
		"tiktok_pixel_code":" CABC123 ","tiktok_event":" Lead ","linkedin_conversion_rule_id":" 104012 ","linkedin_version":"202601"}`)
	if s.PublicURL != "https://example.com" || s.NotifyEmail != "a@example.com" || s.EmailField != "mail" ||
		s.ConsentField != "ok" || s.DefaultCountryCode != "49" || s.Currency != "EUR" || s.Value == nil || *s.Value != 12.5 {
		t.Fatalf("settings = %+v", s)
	}
	if !s.Google.Enabled || s.Google.CustomerID != "1234567890" || s.Google.LoginCustomerID != "1112223333" || s.Google.ConversionActionID != "987" {
		t.Fatalf("google = %+v", s.Google)
	}
	if s.TikTok.PixelCode != "CABC123" || s.TikTok.Event != "Lead" || s.LinkedIn.RuleID != "104012" || s.LinkedIn.Version != "202601" {
		t.Fatalf("tiktok/linkedin = %+v %+v", s.TikTok, s.LinkedIn)
	}
	if !slices.Equal(s.Forms, []string{"contact", "quote"}) {
		t.Fatalf("forms = %q", s.Forms)
	}
}

func TestSettingsInvalidValuesAreDropped(t *testing.T) {
	s := withSettings(t, `{"default_country_code":"+49","currency":"euro","value":-1,"google_customer_id":"12a","google_conversion_action_id":"x1","linkedin_version":"v2","linkedin_conversion_rule_id":"1/2"}`)
	if s.DefaultCountryCode != "" || s.Currency != "" || s.Value != nil || s.Google.CustomerID != "" ||
		s.Google.ConversionActionID != "" || s.LinkedIn.Version != "202610" || s.LinkedIn.RuleID != "" {
		t.Fatalf("settings = %+v", s)
	}
}

func TestSettingsEmptyFormsMeansNone(t *testing.T) {
	for _, raw := range []string{`{}`, `{"forms":null}`, `{"forms":[" "]}`} {
		s := withSettings(t, raw)
		if len(s.Forms) != 0 || s.allowsForm("contact") || s.allowsForm("") {
			t.Fatalf("%s: forms = %v", raw, s.Forms)
		}
	}
	s := withSettings(t, `{"forms":["contact"]}`)
	if !s.allowsForm("contact") || s.allowsForm("quote") {
		t.Fatal("allowsForm with a list")
	}
}

func TestSettingsHostError(t *testing.T) {
	callHost = func(string, any, any) error { return errors.New("host down") }
	if _, err := loadSettings(); err == nil {
		t.Fatal("want the host error")
	}
}

func TestSettingsMissing(t *testing.T) {
	s := withSettings(t, `{}`)
	if got := s.missing(platGoogle); !slices.Equal(got, []string{"Site address", "Client ID", "Client secret", "Customer ID", "Conversion action ID"}) {
		t.Fatalf("google missing = %v", got)
	}
	if got := s.missing(platTikTok); !slices.Equal(got, []string{"Pixel code", "Access token"}) {
		t.Fatalf("tiktok missing = %v", got)
	}
	if got := s.missing(platLinkedIn); !slices.Equal(got, []string{"Site address", "Client ID", "Client secret", "Conversion rule ID"}) {
		t.Fatalf("linkedin missing = %v", got)
	}
	full := withSettings(t, `{"public_url":"https://example.com","google_client_id":"i","google_client_secret":"s","google_customer_id":"1234567890",
		"google_conversion_action_id":"9","tiktok_pixel_code":"CABC123","tiktok_access_token":"t",
		"linkedin_client_id":"i","linkedin_client_secret":"s","linkedin_conversion_rule_id":"5"}`)
	for _, p := range platforms {
		if !full.configured(p) {
			t.Errorf("%s: missing %v", p, full.missing(p))
		}
	}
	if s.configured(platTikTok) || s.missing("other") != nil {
		t.Error("configured / unknown platform")
	}
}

func TestSettingsEnabledAndRedirect(t *testing.T) {
	s := withSettings(t, `{"public_url":"https://example.com/","tiktok_enabled":true}`)
	if s.enabled(platGoogle) || !s.enabled(platTikTok) || s.enabled(platLinkedIn) || s.enabled("other") {
		t.Fatal("enabled")
	}
	if got := s.redirectURI(platLinkedIn); got != "https://example.com/p/ads-conversions/oauth/linkedin/callback" {
		t.Fatalf("redirectURI = %q", got)
	}
}
