package main

import (
	"net/mail"
	"regexp"
	"slices"
	"strings"
)

// cfg is the saved settings.
type cfg struct {
	Provider       string   `json:"provider"`
	Forms          []string `json:"forms"`
	EmailField     string   `json:"email_field"`
	ConsentField   string   `json:"consent_field"`
	AddNote        *bool    `json:"add_note"`
	LeadSource     string   `json:"lead_source"`
	OwnerID        string   `json:"owner_id"`
	AccessToken    string   `json:"access_token"`
	LifecycleStage string   `json:"lifecycle_stage"`
	APIToken       string   `json:"api_token"`
	CompanyDomain  string   `json:"company_domain"`
	CreateLead     *bool    `json:"create_lead"`
	MyDomain       string   `json:"my_domain"`
	ClientID       string   `json:"client_id"`
	ClientSecret   string   `json:"client_secret"`
	Object         string   `json:"object"`
	DefaultCompany string   `json:"default_company"`
}

func loadCfg() (cfg, error) {
	var c cfg
	if err := callHost("settings.get", struct{}{}, &c); err != nil {
		return cfg{}, err
	}
	c.EmailField = strings.TrimSpace(c.EmailField)
	c.ConsentField = strings.TrimSpace(c.ConsentField)
	return c, nil
}

// Secrets and ids are read trimmed: a pasted trailing newline must not break
// a header or a form body.
func (c cfg) token() string        { return strings.TrimSpace(c.AccessToken) }
func (c cfg) apiToken() string     { return strings.TrimSpace(c.APIToken) }
func (c cfg) clientID() string     { return strings.TrimSpace(c.ClientID) }
func (c cfg) clientSecret() string { return strings.TrimSpace(c.ClientSecret) }

// companyLabel is the Pipedrive company label, or "" when it is not valid.
func (c cfg) companyLabel() string { return normCompany(c.CompanyDomain) }

// sfHost is the Salesforce My Domain host, or "" when it is not valid.
func (c cfg) sfHost() string { return normMyDomain(c.MyDomain) }

// configured reports whether the provider and all its credentials are set.
func (c cfg) configured() bool {
	switch c.Provider {
	case "hubspot":
		return c.token() != ""
	case "pipedrive":
		return c.apiToken() != "" && c.companyLabel() != ""
	case "salesforce":
		return c.clientID() != "" && c.clientSecret() != "" && c.sfHost() != ""
	}
	return false
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

// addNote and createLead default to true when the setting was never saved.
func (c cfg) addNote() bool    { return c.AddNote == nil || *c.AddNote }
func (c cfg) createLead() bool { return c.CreateLead == nil || *c.CreateLead }

func (c cfg) leadSource() string { return orDefault(c.LeadSource, "Website") }

// object is "contact" only when exactly that; anything else is a lead.
func (c cfg) object() string {
	if c.Object == "contact" {
		return "contact"
	}
	return "lead"
}

func (c cfg) defaultCompany() string { return orDefault(c.DefaultCompany, "Website visitor") }
func (c cfg) lifecycle() string      { return orDefault(c.LifecycleStage, "lead") }

func orDefault(s, def string) string {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return def
}

var (
	digitsID   = regexp.MustCompile(`^[0-9]{1,20}$`)
	sfID       = regexp.MustCompile(`^[a-zA-Z0-9]{15,18}$`)
	companyRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	hostLabel  = regexp.MustCompile(`^[a-z0-9-]+$`)
	schemeTrim = []string{"https://", "http://"}
)

const (
	sfSuffix = ".my.salesforce.com"
	maxMyLen = 120
)

// owner is the configured owner id when it fits the provider's id format, else
// "": an invalid id is ignored, not sent.
func (c cfg) owner() string {
	id := strings.TrimSpace(c.OwnerID)
	switch c.Provider {
	case "hubspot", "pipedrive":
		if digitsID.MatchString(id) {
			return id
		}
	case "salesforce":
		if sfID.MatchString(id) {
			return id
		}
	}
	return ""
}

func stripScheme(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range schemeTrim {
		if rest, ok := strings.CutPrefix(s, p); ok {
			s = rest
			break
		}
	}
	return strings.TrimSuffix(s, "/")
}

// normCompany reduces "acme", "acme.pipedrive.com" or a URL to the company
// label; "" when the result is not a single valid DNS label.
func normCompany(s string) string {
	s = strings.TrimSuffix(stripScheme(s), ".pipedrive.com")
	if companyRE.MatchString(s) {
		return s
	}
	return ""
}

// normMyDomain returns the Salesforce My Domain host, or "" unless it is a
// plain host name ending in .my.salesforce.com.
func normMyDomain(s string) string {
	h := stripScheme(s)
	if len(h) > maxMyLen || !strings.HasSuffix(h, sfSuffix) {
		return ""
	}
	for _, l := range strings.Split(h, ".") {
		if !hostLabel.MatchString(l) {
			return ""
		}
	}
	if strings.TrimSuffix(h, sfSuffix) == "" {
		return ""
	}
	return h
}

// truthy reads a consent value: a ticked box arrives as true, text fields as
// strings. A checkbox with options arrives as the list of ticked values, so a
// list with a non-empty text value counts. Anything else (numbers other than
// 1, nil) is not consent.
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

// fieldValue is a submitted text value, trimmed and never cut: a long address
// must reach validEmail whole.
func fieldValue(fields map[string]any, key string) string {
	s, _ := fields[key].(string)
	return strings.TrimSpace(s)
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
