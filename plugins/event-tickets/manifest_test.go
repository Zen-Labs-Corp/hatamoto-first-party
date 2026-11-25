package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

type manifestFile struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	SDK            string   `json:"sdk"`
	Permissions    []string `json:"permissions"`
	AllowedHosts   []string `json:"allowed_hosts"`
	RequestHeaders []string `json:"request_headers"`
	BuilderSources []struct {
		Name string `json:"name"`
	} `json:"builder_sources"`
	UI struct {
		Title string   `json:"title"`
		Entry string   `json:"entry"`
		Roles []string `json:"roles"`
		Allow []string `json:"allow"`
	} `json:"ui"`
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
	Routes []struct {
		Method string   `json:"method"`
		Path   string   `json:"path"`
		Public bool     `json:"public"`
		Roles  []string `json:"roles"`
	} `json:"routes"`
	Limits struct {
		HookTimeouts map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
	Settings struct {
		Properties map[string]struct {
			Type      string   `json:"type"`
			Desc      string   `json:"description"`
			Default   any      `json:"default"`
			Pattern   string   `json:"pattern"`
			MaxLen    int      `json:"maxLength"`
			WriteOnly bool     `json:"writeOnly"`
			Enum      []string `json:"enum"`
			Min       *int     `json:"minimum"`
			Max       *int     `json:"maximum"`
		} `json:"properties"`
	} `json:"settings_schema"`
}

func loadManifest(t *testing.T) manifestFile {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifestFile
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func exists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s: %v", path, err)
	}
}

func TestManifest(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "event-tickets" || m.Version != "0.4.0" {
		t.Errorf("name %q version %q", m.Name, m.Version)
	}

	// sdk range (npm caret on 0.x: ^0.M is >=0.M.0 <0.(M+1).0) holds the go.mod version
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	got := regexp.MustCompile(`hatamoto\.dev/sdk v(\d+\.\d+\.\d+)`).FindSubmatch(mod)
	if m.SDK != "^0.22.2" || got == nil || !strings.HasPrefix(string(got[1]), "0.22.") {
		t.Errorf("sdk range %q, go.mod %q", m.SDK, got)
	}

	wantPerms := []string{"builder.data", "builder.island", "db", "entities.read", "http.fetch", "mail.send", "settings"}
	if perms := slices.Sorted(slices.Values(m.Permissions)); !slices.Equal(perms, wantPerms) {
		t.Errorf("permissions = %v, want %v", perms, wantPerms)
	}
	if wantHosts := []string{"api.stripe.com", "api-m.paypal.com", "api-m.sandbox.paypal.com"}; !slices.Equal(m.AllowedHosts, wantHosts) {
		t.Errorf("allowed_hosts = %v, want %v", m.AllowedHosts, wantHosts)
	}
	wantHeaders := []string{"stripe-signature", "paypal-transmission-id", "paypal-transmission-time", "paypal-transmission-sig", "paypal-cert-url", "paypal-auth-algo"}
	if !slices.Equal(m.RequestHeaders, wantHeaders) {
		t.Errorf("request_headers = %v, want %v", m.RequestHeaders, wantHeaders)
	}
	if len(m.BuilderSources) != 1 || m.BuilderSources[0].Name != "tickets" {
		t.Errorf("builder_sources = %+v", m.BuilderSources)
	}
	if len(m.Cron) != 2 || m.Cron[0].Name != "mail" || m.Cron[0].Every != "5m" || m.Cron[1].Name != "maintain" || m.Cron[1].Every != "1h" {
		t.Errorf("cron = %+v", m.Cron)
	}

	wantRoutes := []struct {
		method, path string
		public       bool
	}{
		{"GET", "/admin/events", false}, {"GET", "/admin/events/{id}", false},
		{"POST", "/admin/events/{id}/types", false}, {"POST", "/admin/types/{id}/delete", false},
		{"POST", "/admin/events/{id}/fields", false}, {"GET", "/admin/events/{id}/attendees", false},
		{"GET", "/admin/events/{id}/attendees.csv", false}, {"POST", "/admin/attendees/{id}/resend", false},
		{"POST", "/admin/attendees/{id}/cancel", false}, {"GET", "/admin/settings", false},
		{"GET", "/token", true}, {"POST", "/rsvp", true},
		{"GET", "/waitlist/{token}/claim", true}, {"GET", "/waitlist/{token}/leave", true},
		{"GET", "/ticket/{token}", true}, {"GET", "/ticket/{token}/qr.png", true},
		{"POST", "/quote", true}, {"POST", "/checkout", true}, {"POST", "/stripe/webhook", true},
		{"POST", "/paypal/webhook", true}, {"GET", "/return/{token}", true}, {"GET", "/cancel/{token}", true},
		{"GET", "/admin/orders", false}, {"POST", "/admin/orders/{id}/refund", false}, {"POST", "/admin/orders/{id}/cancel", false},
		{"GET", "/admin/coupons", false}, {"POST", "/admin/coupons", false}, {"POST", "/admin/coupons/{id}/delete", false},
		{"POST", "/admin/events/{id}/bundles", false},
		{"GET", "/admin/events/{id}/checkin", false}, {"POST", "/admin/events/{id}/checkin", false},
		{"POST", "/admin/attendees/{id}/uncheck", false},
		{"POST", "/admin/events/{id}/splits", false}, {"GET", "/admin/payouts", false},
		{"GET", "/admin/payouts.csv", false}, {"POST", "/admin/payouts/mark", false}, {"POST", "/admin/payouts/unmark", false},
	}
	// Refund, order cancel, coupon changes and bundle rules move or give away money: the owner only (the handlers check again).
	ownerOnly := map[string]bool{"/admin/orders/{id}/refund": true, "/admin/orders/{id}/cancel": true,
		"/admin/coupons/{id}/delete": true, "/admin/events/{id}/bundles": true,
		"/admin/events/{id}/splits": true, "/admin/payouts": true,
		"/admin/payouts.csv": true, "/admin/payouts/mark": true, "/admin/payouts/unmark": true}
	if len(m.Routes) != len(wantRoutes) {
		t.Fatalf("routes = %+v", m.Routes)
	}
	for i, r := range m.Routes {
		w := wantRoutes[i]
		if r.Method != w.method || r.Path != w.path || r.Public != w.public {
			t.Errorf("route %d = %+v, want %+v", i, r, w)
		}
		wantRoles := []string{"owner", "editor"}
		if ownerOnly[w.path] || (w.method == "POST" && w.path == "/admin/coupons") {
			wantRoles = []string{"owner"}
		}
		if !w.public && !slices.Equal(r.Roles, wantRoles) || w.public && len(r.Roles) != 0 {
			t.Errorf("route %s roles = %v", r.Path, r.Roles)
		}
	}

	if m.UI.Title != "Event tickets" || m.UI.Entry != "index.html" || !slices.Equal(m.UI.Roles, []string{"owner", "editor"}) || !slices.Equal(m.UI.Allow, []string{"camera"}) {
		t.Errorf("ui = %+v", m.UI)
	}

	known := []string{"builder.data", "entity.created", "entity.updated", "entity.published", "entity.deleted"}
	for _, c := range m.Cron {
		known = append(known, "on_cron_"+c.Name)
	}
	for k := range m.Limits.HookTimeouts {
		if !slices.Contains(known, k) {
			t.Errorf("hook timeout for unknown %q", k)
		}
	}

	p := m.Settings.Properties
	if len(p) != 17 {
		t.Errorf("%d settings, want 17", len(p))
	}
	if c := p["currency"]; c.Default != "USD" || !regexp.MustCompile(c.Pattern).MatchString("EUR") || regexp.MustCompile(c.Pattern).MatchString("eur") {
		t.Errorf("currency = %+v", c)
	}
	if c := p["default_capacity"]; c.Default != float64(50) || *c.Min != 1 || *c.Max != 100000 {
		t.Errorf("default_capacity = %+v", c)
	}
	if c := p["retention_months"]; c.Default != float64(24) || *c.Min != 1 || *c.Max != 120 {
		t.Errorf("retention_months = %+v", c)
	}
	if c := p["site_url"]; c.Default != "" || c.MaxLen != 200 || !regexp.MustCompile(c.Pattern).MatchString("") || !regexp.MustCompile(c.Pattern).MatchString("https://example.org") {
		t.Errorf("site_url = %+v", c)
	}
	if c := p["schema_slug"]; c.Default != "" || !regexp.MustCompile(c.Pattern).MatchString("") {
		t.Errorf("schema_slug = %+v", c)
	}
	for _, key := range []string{"sender_name", "confirmation_text"} {
		if p[key].Default != "" || p[key].MaxLen == 0 {
			t.Errorf("%s = %+v", key, p[key])
		}
	}
	// the signing key lives in the plugin's own table; the provider secrets, and only they, are write-only
	var secret []string
	for k, c := range p {
		if c.WriteOnly {
			secret = append(secret, k)
		}
	}
	slices.Sort(secret)
	if !slices.Equal(secret, []string{"paypal_secret", "stripe_secret_key", "stripe_webhook_secret"}) {
		t.Errorf("write-only settings = %v", secret)
	}
	if c := p["stripe_secret_key"]; !regexp.MustCompile(c.Pattern).MatchString("rk_live_abcdefghij") || regexp.MustCompile(c.Pattern).MatchString("pk_live_abcdefghij") || !regexp.MustCompile(c.Pattern).MatchString("") {
		t.Errorf("stripe_secret_key = %+v", c)
	}
	if c := p["stripe_webhook_secret"]; !regexp.MustCompile(c.Pattern).MatchString("whsec_abcdefghij") || regexp.MustCompile(c.Pattern).MatchString("abcdefghij") {
		t.Errorf("stripe_webhook_secret = %+v", c)
	}
	if c := p["fee_per"]; c.Default != "order" || !slices.Equal(c.Enum, []string{"order", "ticket"}) {
		t.Errorf("fee_per = %+v", c)
	}
	if c := p["fee_payer"]; c.Default != "buyer" || !slices.Equal(c.Enum, []string{"buyer", "organizer"}) {
		t.Errorf("fee_payer = %+v", c)
	}
	if c := p["fee_percent_bp"]; c.Default != float64(0) || *c.Max != 5000 {
		t.Errorf("fee_percent_bp = %+v", c)
	}
	if c := p["paypal_sandbox"]; c.Type != "boolean" || c.Default != false {
		t.Errorf("paypal_sandbox = %+v", c)
	}
	if strings.Contains(p["currency"].Desc, "free for now") {
		t.Error("the currency text must not say tickets are free")
	}
}

func TestShippedFiles(t *testing.T) {
	for _, f := range []string{"migrations/001_init.sql", "migrations/002_paid.sql", "migrations/003_checkin.sql", "migrations/004_payouts.sql", "migrations/005_emails.sql", "widgets/tickets/widget.html", "widgets/ticket-type/widget.html", "ui/index.html"} {
		exists(t, f)
	}
}

// Every permission is explained in the README, and the QR library's licence is named.
func TestReadmeExplainsPermissions(t *testing.T) {
	m := loadManifest(t)
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(raw), "## Permissions")
	if !ok {
		t.Fatal("README has no Permissions section")
	}
	section, _, _ := strings.Cut(after, "\n## ")
	for _, p := range m.Permissions {
		if !strings.Contains(section, "- `"+p+"`") {
			t.Errorf("README Permissions does not explain %s", p)
		}
	}
	for _, h := range m.AllowedHosts {
		if !strings.Contains(section, h) {
			t.Errorf("README Permissions does not name %s", h)
		}
	}
	if !strings.Contains(string(raw), "boombuler/barcode") || !strings.Contains(string(raw), "MIT") {
		t.Error("README must credit boombuler/barcode and its MIT licence")
	}
	if !strings.Contains(string(raw), "jsQR") || !strings.Contains(string(raw), "Apache-2.0") {
		t.Error("README must credit jsQR and its Apache-2.0 licence")
	}
	if slices.Contains(m.UI.Allow, "camera") && !strings.Contains(section, "camera") {
		t.Error("README Permissions does not explain the camera")
	}
}

// TestManifestSDKValidate runs the manifest through the validation core
// applies at load, so a manifest core would refuse fails the build.
func TestManifestSDKValidate(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m sdk.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("core would refuse this manifest: %v", err)
	}
}
