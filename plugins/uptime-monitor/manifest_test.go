package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func loadManifest(t *testing.T) sdk.Manifest {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m sdk.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestValid(t *testing.T) {
	m := loadManifest(t)
	if err := m.WithDefaults().Validate(); err != nil {
		t.Fatalf("manifest refused by the SDK: %v", err)
	}
	if m.Name != "uptime-monitor" || m.Version != "0.1.1" || m.SDK != "^0.11" {
		t.Fatalf("name %q version %q sdk %q", m.Name, m.Version, m.SDK)
	}
}

func TestManifestPermissionsAndRoutes(t *testing.T) {
	m := loadManifest(t)
	var got []string
	for _, p := range m.Permissions {
		got = append(got, string(p))
	}
	want := []string{"settings", "http.fetch", "http.fetch.any", "db", "kv", "mail.send", "log"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("permissions %v", got)
	}
	if !slices.Equal(m.AllowedHosts, []string{"*", "crt.sh"}) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
	routes := map[string]bool{}
	for _, r := range m.Routes {
		if r.Public || len(r.Roles) != 0 {
			t.Fatalf("route %s %s must be owner only", r.Method, r.Path)
		}
		routes[r.Method+" "+r.Path] = true
	}
	for _, k := range []string{"GET /overview", "POST /monitors", "POST /check-now", "POST /test-alert"} {
		if !routes[k] {
			t.Fatalf("missing route %s", k)
		}
	}
	if len(routes) != 4 {
		t.Fatalf("routes %v", routes)
	}
	if len(m.UI.Roles) != 0 {
		t.Fatal("ui.roles must be absent (owner only)")
	}
}

func TestManifestCronAndLimits(t *testing.T) {
	m := loadManifest(t)
	got := map[string]string{}
	for _, c := range m.Cron {
		got[c.Name] = c.Every
	}
	if len(got) != 3 || got["check"] != "1m" || got["certs"] != "1h" || got["rollup"] != "24h" {
		t.Fatalf("cron %v", got)
	}
	for _, k := range []string{"on_cron_check", "on_cron_certs", "on_cron_rollup"} {
		if m.Limits.HookTimeoutsMS[k] != 10000 {
			t.Fatalf("hook timeout %s: %v", k, m.Limits.HookTimeoutsMS)
		}
	}
}

func TestManifestSettings(t *testing.T) {
	m := loadManifest(t)
	var schema struct {
		Properties map[string]struct {
			WriteOnly bool `json:"writeOnly"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(m.SettingsSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties) != 4 {
		t.Fatalf("settings %v", schema.Properties)
	}
	if !schema.Properties["heartbeat_url"].WriteOnly {
		t.Fatal("heartbeat_url must be writeOnly")
	}
}

func TestGoModPinsSDK(t *testing.T) {
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hatamoto.dev/sdk v0.11.0") {
		t.Fatal("go.mod must require hatamoto.dev/sdk v0.11.0")
	}
	if strings.Contains(string(raw), "replace") {
		t.Fatal("go.mod must have no replace")
	}
}

func TestRegistrationKeepsExportsInSync(t *testing.T) {
	// Every cron job in the manifest has a wasmexport line.
	raw, err := os.ReadFile("exports_wasip1.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range loadManifest(t).Cron {
		if !strings.Contains(string(raw), "wasmexport on_cron_"+c.Name+"\n") {
			t.Fatalf("no export for cron %s", c.Name)
		}
	}
}
