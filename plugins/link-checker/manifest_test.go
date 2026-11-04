package main

import (
	"encoding/json"
	"os"
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
	UI   map[string]any `json:"ui"`
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
	Limits struct {
		HookTimeoutsMS map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
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

func TestManifestPermissionsAndRoutes(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "link-checker" || m.Version != "0.1.0" || m.SDK != "^0.11" {
		t.Fatalf("name %q version %q sdk %q", m.Name, m.Version, m.SDK)
	}
	want := []string{"entities.read", "redirects.read", "http.fetch", "http.fetch.any", "db", "kv", "settings", "log"}
	got := slices.Clone(m.Permissions)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	if !slices.Equal(m.AllowedHosts, []string{"*"}) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
	routes := map[string]bool{}
	for _, r := range m.Routes {
		if r.Public || len(r.Roles) != 0 {
			t.Fatalf("route %s %s must be owner only", r.Method, r.Path)
		}
		routes[r.Method+" "+r.Path] = true
	}
	for _, k := range []string{"GET /links", "POST /recheck/{id}", "POST /ignore/{id}"} {
		if !routes[k] {
			t.Fatalf("missing route %s", k)
		}
	}
	if len(routes) != 3 {
		t.Fatalf("routes %v", routes)
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must be absent (owner only)")
	}
}

func TestManifestCronAndLimits(t *testing.T) {
	m := loadManifest(t)
	if len(m.Cron) != 1 || m.Cron[0].Name != "scan" || m.Cron[0].Every != "5m" {
		t.Fatalf("cron %+v", m.Cron)
	}
	if m.Limits.HookTimeoutsMS["on_cron_scan"] != 10000 {
		t.Fatalf("limits %+v", m.Limits)
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
	if strings.Contains(string(raw), "replace") || strings.Contains(string(raw), "golang.org/x/net") {
		t.Fatal("go.mod must have no replace and no x/net")
	}
}
