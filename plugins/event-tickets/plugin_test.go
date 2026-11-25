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

func TestLoadSettingsPayments(t *testing.T) {
	fakeSettings(t, `{"stripe_secret_key":"sk_test_abcdefghij","stripe_webhook_secret":"whsec_abcdefghij","paypal_client_id":"cid","paypal_secret":"sec",
"paypal_webhook_id":"ABCDEFGH1","paypal_sandbox":true,"fee_fixed_cents":30,"fee_percent_bp":250,"fee_per":"ticket","fee_payer":"organizer"}`, nil)
	want := defaultSettings()
	want.StripeKey, want.StripeWebhookSecret = "sk_test_abcdefghij", "whsec_abcdefghij"
	want.PayPalClientID, want.PayPalSecret, want.PayPalWebhookID, want.PayPalSandbox = "cid", "sec", "ABCDEFGH1", true
	want.FeeFixed, want.FeeBP, want.FeePerTicket, want.FeeAbsorbed = 30, 250, true, true
	got := loadSettings()
	if got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	if !got.stripeReady() || !got.paypalReady() {
		t.Error("both providers are configured")
	}
	// out of range values fall back; a key of the wrong shape is not used
	fakeSettings(t, `{"stripe_secret_key":"pk_live_abcdefghij","stripe_webhook_secret":"nope","paypal_webhook_id":"abc","fee_fixed_cents":-1,"fee_percent_bp":5001,"fee_per":"x","fee_payer":"y"}`, nil)
	if got := loadSettings(); got != defaultSettings() || got.stripeReady() || got.paypalReady() {
		t.Errorf("%+v", got)
	}
	// each provider needs both of its credentials
	s := defaultSettings()
	s.StripeKey = "sk_test_abcdefghij"
	s.PayPalClientID = "cid"
	if s.stripeReady() || s.paypalReady() {
		t.Error("half a configuration is not ready")
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
