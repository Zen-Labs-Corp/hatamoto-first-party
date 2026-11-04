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
		HookTimeoutsMs map[string]int `json:"hook_timeouts_ms"`
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

func TestManifestPermissionsAndHosts(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "bing-webmaster" || m.SDK != "^0.8" {
		t.Fatalf("name %q sdk %q", m.Name, m.SDK)
	}
	if !slices.Equal(m.Permissions, []string{"settings", "http.fetch", "db", "kv", "log"}) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	if !slices.Equal(m.AllowedHosts, []string{"ssl.bing.com"}) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
}

func TestManifestRoutesAreOwnerOnly(t *testing.T) {
	m := loadManifest(t)
	var got []string
	for _, r := range m.Routes {
		if r.Public || len(r.Roles) != 0 {
			t.Errorf("route %s %s is not owner-only", r.Method, r.Path)
		}
		got = append(got, r.Method+" "+r.Path)
	}
	want := []string{"GET /overview", "POST /test", "POST /submit-sitemap", "POST /import-now"}
	if !slices.Equal(got, want) {
		t.Fatalf("routes %v", got)
	}
	if m.UI["title"] != "Bing Webmaster Tools" {
		t.Fatalf("ui %v", m.UI)
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must be absent (owner only)")
	}
}

func TestManifestCronAndTimeouts(t *testing.T) {
	m := loadManifest(t)
	if len(m.Cron) != 1 || m.Cron[0].Name != "import" || m.Cron[0].Every != "6h" {
		t.Fatalf("cron %+v", m.Cron)
	}
	for _, hook := range []string{"on_cron_import", "http_handle"} {
		if m.Limits.HookTimeoutsMs[hook] != 10000 {
			t.Errorf("hook_timeouts_ms[%s] = %d", hook, m.Limits.HookTimeoutsMs[hook])
		}
	}
}

func TestManifestAPIKeyIsWriteOnly(t *testing.T) {
	m := loadManifest(t)
	if !m.Settings.Properties["api_key"].WriteOnly {
		t.Fatal("api_key must be writeOnly")
	}
	if m.Settings.Properties["site_url"].WriteOnly {
		t.Fatal("site_url is shown to the owner")
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
}
