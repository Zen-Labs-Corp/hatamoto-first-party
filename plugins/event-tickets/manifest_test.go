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
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	SDK         string   `json:"sdk"`
	Permissions []string `json:"permissions"`
	Cron        []struct {
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
			Type    string `json:"type"`
			Default any    `json:"default"`
			Pattern string `json:"pattern"`
			MaxLen  int    `json:"maxLength"`
			Min     *int   `json:"minimum"`
			Max     *int   `json:"maximum"`
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
	if m.Name != "event-tickets" || m.Version != "0.1.0" {
		t.Errorf("name %q version %q", m.Name, m.Version)
	}

	// sdk range (npm caret on 0.x: ^0.M is >=0.M.0 <0.(M+1).0) holds the go.mod version
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	got := regexp.MustCompile(`hatamoto\.dev/sdk v(\d+\.\d+\.\d+)`).FindSubmatch(mod)
	if m.SDK != "^0.22" || got == nil || !strings.HasPrefix(string(got[1]), "0.22.") {
		t.Errorf("sdk range %q, go.mod %q", m.SDK, got)
	}

	allowed := []string{"entities.read", "db", "settings", "mail.send", "builder.data", "builder.island"}
	for _, p := range m.Permissions {
		if !slices.Contains(allowed, p) {
			t.Errorf("permission %q is not one this plugin may ask for", p)
		}
	}

	for _, r := range m.Routes {
		switch {
		case r.Public && len(r.Roles) != 0:
			t.Errorf("public route %s %s has roles %v", r.Method, r.Path, r.Roles)
		case !r.Public && !slices.Equal(r.Roles, []string{"owner", "editor"}):
			t.Errorf("admin route %s %s roles = %v, want owner and editor", r.Method, r.Path, r.Roles)
		}
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
	if len(p) != 7 {
		t.Errorf("%d settings, want 7", len(p))
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
	// the signing key lives in the plugin's own table: no secret setting
	raw, _ := os.ReadFile("plugin.json")
	if strings.Contains(string(raw), "writeOnly") {
		t.Error("no setting may be a secret")
	}
}

func TestShippedFiles(t *testing.T) {
	exists(t, "migrations/001_init.sql")
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
