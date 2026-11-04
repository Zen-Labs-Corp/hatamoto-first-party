package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type manifest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	SDK          string   `json:"sdk"`
	Permissions  []string `json:"permissions"`
	AllowedHosts []string `json:"allowed_hosts"`
	Routes       []struct {
		Method string   `json:"method"`
		Path   string   `json:"path"`
		Public bool     `json:"public"`
		Roles  []string `json:"roles"`
	} `json:"routes"`
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
	BuilderSources []struct {
		Name  string `json:"name"`
		Label string `json:"label"`
		Kind  string `json:"kind"`
	} `json:"builder_sources"`
	UI     map[string]any `json:"ui"`
	Limits struct {
		HookTimeouts map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
	Settings struct {
		Properties map[string]struct {
			WriteOnly bool `json:"writeOnly"`
			MaxItems  int  `json:"maxItems"`
		} `json:"properties"`
	} `json:"settings_schema"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestIdentityPermissionsHosts(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "feed-reader" || m.Version != "0.1.0" || m.SDK != "^0.11" {
		t.Fatalf("identity %q %q %q", m.Name, m.Version, m.SDK)
	}
	want := []string{"settings", "http.fetch", "http.fetch.any", "db", "builder.data", "log"}
	if !slices.Equal(m.Permissions, want) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	if !slices.Equal(m.AllowedHosts, []string{"*"}) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
}

func TestManifestRoutesCronTimeoutsSources(t *testing.T) {
	m := loadManifest(t)
	var got []string
	for _, r := range m.Routes {
		if r.Public || len(r.Roles) != 0 {
			t.Fatalf("route %s %s must be owner only", r.Method, r.Path)
		}
		got = append(got, r.Method+" "+r.Path)
	}
	if !slices.Equal(got, []string{"GET /overview", "POST /fetch-now"}) {
		t.Fatalf("routes %v", got)
	}
	if len(m.Cron) != 1 || m.Cron[0].Name != "fetch" || m.Cron[0].Every != "5m" {
		t.Fatalf("cron %+v", m.Cron)
	}
	if m.Limits.HookTimeouts["on_cron_fetch"] != 10000 || m.Limits.HookTimeouts["http_handle"] != 10000 ||
		m.Limits.HookTimeouts["builder.data"] != 400 {
		t.Fatalf("hook timeouts %v", m.Limits.HookTimeouts)
	}
	if m.UI["title"] != "Feeds" {
		t.Fatalf("ui %v", m.UI)
	}
	if len(m.BuilderSources) != 8 {
		t.Fatalf("builder sources %+v", m.BuilderSources)
	}
	name := regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	seen := map[string]bool{}
	for _, s := range m.BuilderSources {
		if !name.MatchString(s.Name) || seen[s.Name] || s.Kind != "items" || s.Label == "" {
			t.Fatalf("bad source %+v", s)
		}
		seen[s.Name] = true
	}
	if !seen["items"] {
		t.Fatal("source items missing")
	}
	for i := 1; i <= 7; i++ {
		if !seen["group-"+string(rune('0'+i))] {
			t.Fatalf("source group-%d missing", i)
		}
	}
}

func TestManifestSettings(t *testing.T) {
	m := loadManifest(t)
	if len(m.Settings.Properties) != 1 || m.Settings.Properties["feeds"].MaxItems != 20 {
		t.Fatalf("settings %+v", m.Settings.Properties)
	}
	for k, p := range m.Settings.Properties {
		if p.WriteOnly {
			t.Fatalf("%s must not be writeOnly: the plugin keeps no secrets", k)
		}
	}
}

func TestMigrationShape(t *testing.T) {
	raw, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !regexp.MustCompile(`(?m)^-- \+goose Up\s*$`).MatchString(sql) {
		t.Fatal("missing -- +goose Up")
	}
	if regexp.MustCompile(`(?i)\bCREATE\s+(OR\s+REPLACE\s+)?(FUNCTION|TRIGGER|PROCEDURE|EXTENSION)\b`).MatchString(sql) {
		t.Fatal("functions, triggers and extensions are refused")
	}
	if strings.Contains(sql, "items_pub") {
		t.Fatal("no query orders by a plain published_at, so the index is never used")
	}
	if regexp.MustCompile(`(?i)\bpg_|\bGRANT\b`).MatchString(sql) {
		t.Fatal("pg_ names and GRANT are refused")
	}
}
