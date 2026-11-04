package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"
)

type manifest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	SDK          string   `json:"sdk"`
	Permissions  []string `json:"permissions"`
	AllowedHosts []string `json:"allowed_hosts"`
	Funcs        []string `json:"funcs"`
	Hooks        any      `json:"hooks"`
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
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"builder_sources"`
	UI     map[string]any `json:"ui"`
	Limits struct {
		HookTimeouts map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
	Settings struct {
		Properties map[string]struct {
			WriteOnly bool `json:"writeOnly"`
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
	if m.Name != "reviews" || m.Version != "0.1.0" || m.SDK != "^0.11" {
		t.Fatalf("identity %q %q %q", m.Name, m.Version, m.SDK)
	}
	if !slices.Equal(m.Permissions, []string{"settings", "http.fetch", "db", "builder.data", "log"}) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	if !slices.Equal(m.AllowedHosts, []string{"places.googleapis.com", "api.trustpilot.com"}) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
	if m.Hooks != nil {
		t.Fatalf("hooks must be absent: %v", m.Hooks)
	}
}

func TestManifestRoutesCronFuncsSources(t *testing.T) {
	m := loadManifest(t)
	var got []string
	for _, r := range m.Routes {
		if r.Public || len(r.Roles) != 0 {
			t.Fatalf("route %s %s must be owner only", r.Method, r.Path)
		}
		got = append(got, r.Method+" "+r.Path)
	}
	if !slices.Equal(got, []string{"GET /overview", "POST /refresh-now"}) {
		t.Fatalf("routes %v", got)
	}
	if len(m.Cron) != 1 || m.Cron[0].Name != "refresh" || m.Cron[0].Every != "1h" {
		t.Fatalf("cron %+v", m.Cron)
	}
	if m.Limits.HookTimeouts["on_cron_refresh"] != 10000 || m.Limits.HookTimeouts["http_handle"] != 10000 {
		t.Fatalf("hook timeouts %v", m.Limits.HookTimeouts)
	}
	if !slices.Equal(m.Funcs, []string{"summary", "count", "field", "profile"}) {
		t.Fatalf("funcs %v", m.Funcs)
	}
	if len(m.BuilderSources) != 1 || m.BuilderSources[0].Name != "latest" || m.BuilderSources[0].Kind != "items" {
		t.Fatalf("builder sources %+v", m.BuilderSources)
	}
	for _, k := range []string{"google_api_key", "trustpilot_api_key"} {
		if !m.Settings.Properties[k].WriteOnly {
			t.Fatalf("%s must be writeOnly", k)
		}
	}
	for _, k := range []string{"google_place_id", "trustpilot_business_unit_id", "min_rating", "max_items"} {
		if _, ok := m.Settings.Properties[k]; !ok {
			t.Fatalf("setting %s missing", k)
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
	if regexp.MustCompile(`(?i)\bpg_`).MatchString(sql) {
		t.Fatal("pg_ names are refused")
	}
}
