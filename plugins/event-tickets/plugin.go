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

	stripeKeyRe     = regexp.MustCompile(`^(sk|rk)_(test|live)_[A-Za-z0-9]{10,200}$`)
	stripeWebhookRe = regexp.MustCompile(`^whsec_[A-Za-z0-9]{10,200}$`)
	paypalHookRe    = regexp.MustCompile(`^[A-Z0-9]{8,40}$`)
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

	// Payments. Secrets are only ever read here, never answered or logged.
	StripeKey           string
	StripeWebhookSecret string
	PayPalClientID      string
	PayPalSecret        string
	PayPalWebhookID     string
	PayPalSandbox       bool
	FeeFixed            int64 // minor units, per order or per priced ticket
	FeeBP               int   // hundredths of a percent
	FeePerTicket        bool  // fee_per = ticket
	FeeAbsorbed         bool  // fee_payer = organizer
}

func defaultSettings() settings {
	return settings{Currency: defaultCurrency, DefaultCapacity: defaultCapacity, RetentionMonths: defaultRetain}
}

// stripeReady reports whether Stripe checkout can be offered: it needs the key
// to create sessions and the webhook secret to trust their completion.
func (s settings) stripeReady() bool { return s.StripeKey != "" && s.StripeWebhookSecret != "" }

// paypalReady reports whether PayPal can be offered. The webhook id is
// optional: without it a payment is completed when the buyer returns.
func (s settings) paypalReady() bool { return s.PayPalClientID != "" && s.PayPalSecret != "" }

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

		StripeKey           string `json:"stripe_secret_key"`
		StripeWebhookSecret string `json:"stripe_webhook_secret"`
		PayPalClientID      string `json:"paypal_client_id"`
		PayPalSecret        string `json:"paypal_secret"`
		PayPalWebhookID     string `json:"paypal_webhook_id"`
		PayPalSandbox       bool   `json:"paypal_sandbox"`
		FeeFixed            int64  `json:"fee_fixed_cents"`
		FeeBP               int    `json:"fee_percent_bp"`
		FeePer              string `json:"fee_per"`
		FeePayer            string `json:"fee_payer"`
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
	if k := strings.TrimSpace(raw.StripeKey); stripeKeyRe.MatchString(k) {
		s.StripeKey = k
	}
	if k := strings.TrimSpace(raw.StripeWebhookSecret); stripeWebhookRe.MatchString(k) {
		s.StripeWebhookSecret = k
	}
	if id := strings.TrimSpace(raw.PayPalClientID); len(id) <= 200 {
		s.PayPalClientID = id
	}
	if k := strings.TrimSpace(raw.PayPalSecret); len(k) <= 200 {
		s.PayPalSecret = k
	}
	if id := strings.TrimSpace(raw.PayPalWebhookID); paypalHookRe.MatchString(id) {
		s.PayPalWebhookID = id
	}
	s.PayPalSandbox = raw.PayPalSandbox
	if raw.FeeFixed >= 0 && raw.FeeFixed <= 100000 {
		s.FeeFixed = raw.FeeFixed
	}
	if raw.FeeBP >= 0 && raw.FeeBP <= 5000 {
		s.FeeBP = raw.FeeBP
	}
	s.FeePerTicket = raw.FeePer == "ticket"
	s.FeeAbsorbed = raw.FeePayer == "organizer"
	return s
}
