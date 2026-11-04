package main

import (
	"net/mail"
	"slices"
	"strings"
	"unicode/utf8"
)

// cfg is the saved settings.
type cfg struct {
	Provider       string   `json:"provider"`
	APIKey         string   `json:"api_key"`
	ListID         string   `json:"list_id"`
	Forms          []string `json:"forms"`
	EmailField     string   `json:"email_field"`
	FirstNameField string   `json:"first_name_field"`
	LastNameField  string   `json:"last_name_field"`
	ConsentField   string   `json:"consent_field"`
	DoubleOptIn    *bool    `json:"double_opt_in"`
	Tags           []string `json:"tags"`
}

func loadCfg() (cfg, error) {
	var c cfg
	if err := callHost("settings.get", struct{}{}, &c); err != nil {
		return cfg{}, err
	}
	return c, nil
}

// configured reports whether provider, key and list are all set.
func (c cfg) configured() bool {
	switch c.Provider {
	case "mailchimp", "brevo", "mailerlite", "kit":
	default:
		return false
	}
	return strings.TrimSpace(c.APIKey) != "" && strings.TrimSpace(c.ListID) != ""
}

// watched reports whether the form is one to sync. No forms means none.
func (c cfg) watched(slug string) bool {
	return slug != "" && slices.Contains(c.Forms, slug)
}

func (c cfg) emailField() string {
	if f := strings.TrimSpace(c.EmailField); f != "" {
		return f
	}
	return "email"
}

// doubleOptIn defaults to true when the setting was never saved.
func (c cfg) doubleOptIn() bool { return c.DoubleOptIn == nil || *c.DoubleOptIn }

// truthy reads a consent value: a ticked box arrives as true, text fields as
// strings. A checkbox with options arrives as the list of ticked values, so a
// list with a truthy or non-empty text value counts. Anything else (numbers
// other than 1, nil) is not consent.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x == 1
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "yes", "on", "checked", "y":
			return true
		}
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
		}
	}
	return false
}

// fieldText is a submitted text value, trimmed and cut to 100 characters.
func fieldText(fields map[string]any, key string) string {
	if key == "" {
		return ""
	}
	s, _ := fields[key].(string)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 100 {
		s = string([]rune(s)[:100])
	}
	return s
}

// validEmail accepts a plain address: x@y.z, at most 254 characters.
func validEmail(s string) bool {
	if s == "" || len(s) > 254 || strings.ContainsAny(s, " \t\r\n<>,;\"()") {
		return false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return false
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || strings.Contains(domain, "@") {
		return false
	}
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".") && !strings.Contains(domain, "..")
}
