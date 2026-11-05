package main

import (
	"regexp"
	"strings"
)

var (
	digitsRe   = regexp.MustCompile(`^[0-9]+$`)
	countryRe  = regexp.MustCompile(`^[0-9]{1,3}$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
	versionRe  = regexp.MustCompile(`^20[0-9]{4}$`)
)

// Platform names, as used in the queue, the log and the routes.
const (
	platGoogle   = "google"
	platTikTok   = "tiktok"
	platLinkedIn = "linkedin"
)

// platforms lists every platform in display order.
var platforms = []string{platGoogle, platTikTok, platLinkedIn}

// settings is the plugin's configuration as the host returns it. The name
// fields are deliberately not read: names are never sent or stored.
type settings struct {
	PublicURL          string   `json:"public_url"`
	NotifyEmail        string   `json:"notify_email"`
	Forms              []string `json:"forms"`
	EmailField         string   `json:"email_field"`
	PhoneField         string   `json:"phone_field"`
	ConsentField       string   `json:"consent_field"`
	DefaultCountryCode string   `json:"default_country_code"`
	Value              *float64 `json:"value"`
	Currency           string   `json:"currency"`

	Google struct {
		Enabled            bool
		ClientID           string
		ClientSecret       string
		CustomerID         string
		LoginCustomerID    string
		ConversionActionID string
	} `json:"-"`
	TikTok struct {
		Enabled       bool
		PixelCode     string
		AccessToken   string
		Event         string
		TestEventCode string
	} `json:"-"`
	LinkedIn struct {
		Enabled      bool
		ClientID     string
		ClientSecret string
		RuleID       string
		AdAccountID  string
		Version      string
	} `json:"-"`
}

// rawSettings mirrors the flat keys of settings_schema.
type rawSettings struct {
	settings
	GoogleEnabled            bool   `json:"google_enabled"`
	GoogleClientID           string `json:"google_client_id"`
	GoogleClientSecret       string `json:"google_client_secret"`
	GoogleCustomerID         string `json:"google_customer_id"`
	GoogleLoginCustomerID    string `json:"google_login_customer_id"`
	GoogleConversionActionID string `json:"google_conversion_action_id"`

	TikTokEnabled       bool   `json:"tiktok_enabled"`
	TikTokPixelCode     string `json:"tiktok_pixel_code"`
	TikTokAccessToken   string `json:"tiktok_access_token"`
	TikTokEvent         string `json:"tiktok_event"`
	TikTokTestEventCode string `json:"tiktok_test_event_code"`

	LinkedInEnabled      bool   `json:"linkedin_enabled"`
	LinkedInClientID     string `json:"linkedin_client_id"`
	LinkedInClientSecret string `json:"linkedin_client_secret"`
	LinkedInRuleID       string `json:"linkedin_conversion_rule_id"`
	LinkedInAdAccountID  string `json:"linkedin_ad_account_id"`
	LinkedInVersion      string `json:"linkedin_version"`
}

// loadSettings reads the settings, trims them and fills the defaults the host
// may not have merged. A malformed identifier is treated as not set, so it
// never ends up in a request URL or body.
func loadSettings() (settings, error) {
	var r rawSettings
	if err := callHost("settings.get", struct{}{}, &r); err != nil {
		return settings{}, err
	}
	s := r.settings
	s.PublicURL = strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	s.NotifyEmail = strings.TrimSpace(s.NotifyEmail)
	s.EmailField = orDefault(s.EmailField, "email")
	s.PhoneField = orDefault(s.PhoneField, "phone")
	s.ConsentField = strings.TrimSpace(s.ConsentField)
	s.DefaultCountryCode = strings.TrimSpace(s.DefaultCountryCode)
	if !countryRe.MatchString(s.DefaultCountryCode) {
		s.DefaultCountryCode = ""
	}
	if s.Value != nil && *s.Value < 0 {
		s.Value = nil
	}
	s.Currency = strings.ToUpper(strings.TrimSpace(s.Currency))
	if !currencyRe.MatchString(s.Currency) {
		s.Currency = ""
	}
	var forms []string
	for _, f := range s.Forms {
		if f = strings.TrimSpace(f); f != "" {
			forms = append(forms, f)
		}
	}
	s.Forms = forms

	g := &s.Google
	g.Enabled = r.GoogleEnabled
	g.ClientID = strings.TrimSpace(r.GoogleClientID)
	g.ClientSecret = strings.TrimSpace(r.GoogleClientSecret)
	g.CustomerID = digitsOnly(r.GoogleCustomerID)
	g.LoginCustomerID = digitsOnly(r.GoogleLoginCustomerID)
	g.ConversionActionID = onlyDigits(r.GoogleConversionActionID)

	t := &s.TikTok
	t.Enabled = r.TikTokEnabled
	t.PixelCode = strings.TrimSpace(r.TikTokPixelCode)
	t.AccessToken = strings.TrimSpace(r.TikTokAccessToken)
	t.Event = orDefault(r.TikTokEvent, "SubmitForm")
	t.TestEventCode = strings.TrimSpace(r.TikTokTestEventCode)

	l := &s.LinkedIn
	l.Enabled = r.LinkedInEnabled
	l.ClientID = strings.TrimSpace(r.LinkedInClientID)
	l.ClientSecret = strings.TrimSpace(r.LinkedInClientSecret)
	l.RuleID = onlyDigits(r.LinkedInRuleID)
	l.AdAccountID = onlyDigits(r.LinkedInAdAccountID)
	l.Version = orDefault(r.LinkedInVersion, "202610")
	if !versionRe.MatchString(l.Version) {
		l.Version = "202610"
	}
	return s, nil
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

// digitsOnly drops dashes and spaces and keeps the result only if it is all
// digits (customer IDs are shown as 123-456-7890).
func digitsOnly(v string) string {
	v = strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(v))
	return onlyDigits(v)
}

// onlyDigits returns v trimmed when it is all digits, else "".
func onlyDigits(v string) string {
	if v = strings.TrimSpace(v); digitsRe.MatchString(v) {
		return v
	}
	return ""
}

// enabled reports whether the platform's switch is on.
func (s settings) enabled(platform string) bool {
	switch platform {
	case platGoogle:
		return s.Google.Enabled
	case platTikTok:
		return s.TikTok.Enabled
	case platLinkedIn:
		return s.LinkedIn.Enabled
	}
	return false
}

// missing lists, in human words, the settings the platform still needs.
func (s settings) missing(platform string) []string {
	var m []string
	add := func(empty bool, label string) {
		if empty {
			m = append(m, label)
		}
	}
	switch platform {
	case platGoogle:
		add(s.PublicURL == "", "Site address")
		add(s.Google.ClientID == "", "Client ID")
		add(s.Google.ClientSecret == "", "Client secret")
		add(s.Google.CustomerID == "", "Customer ID")
		add(s.Google.ConversionActionID == "", "Conversion action ID")
	case platTikTok:
		add(s.TikTok.PixelCode == "", "Pixel code")
		add(s.TikTok.AccessToken == "", "Access token")
	case platLinkedIn:
		add(s.PublicURL == "", "Site address")
		add(s.LinkedIn.ClientID == "", "Client ID")
		add(s.LinkedIn.ClientSecret == "", "Client secret")
		add(s.LinkedIn.RuleID == "", "Conversion rule ID")
	}
	return m
}

// configured reports whether nothing is missing for the platform.
func (s settings) configured(platform string) bool {
	return len(s.missing(platform)) == 0
}

// allowsForm reports whether conversions are sent for the form. An empty list
// means none, so nothing is sent by accident.
func (s settings) allowsForm(slug string) bool {
	for _, f := range s.Forms {
		if f == slug {
			return true
		}
	}
	return false
}

// redirectURI is the OAuth callback address the owner registers with the
// platform.
func (s settings) redirectURI(platform string) string {
	return s.PublicURL + "/p/ads-conversions/oauth/" + platform + "/callback"
}
