package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

type manifest struct {
	Name         string   `json:"name"`
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
		HookTimeouts map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
	Schema struct {
		Properties map[string]map[string]any `json:"properties"`
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
	if m.Name != "subscriber-sync" {
		t.Fatalf("name %q", m.Name)
	}
	want := []string{"db", "forms.read", "http.fetch", "log", "settings"}
	got := slices.Clone(m.Permissions)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	hosts := slices.Clone(m.AllowedHosts)
	slices.Sort(hosts)
	if !slices.Equal(hosts, []string{"*.api.mailchimp.com", "api.brevo.com", "api.kit.com", "connect.mailerlite.com"}) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
}

func TestManifestRoutesAreOwnerOnly(t *testing.T) {
	m := loadManifest(t)
	seen := map[string]bool{}
	for _, r := range m.Routes {
		if r.Public || len(r.Roles) != 0 {
			t.Fatalf("route %s %s must be owner only", r.Method, r.Path)
		}
		seen[r.Method+" "+r.Path] = true
	}
	if len(m.Routes) != 2 || !seen["GET /log"] || !seen["POST /test"] {
		t.Fatalf("routes %+v", m.Routes)
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must be absent (owner only)")
	}
}

func TestManifestCronAndHookTimeouts(t *testing.T) {
	m := loadManifest(t)
	if len(m.Cron) != 1 || m.Cron[0].Name != "retry" || m.Cron[0].Every != "5m" {
		t.Fatalf("cron %+v", m.Cron)
	}
	if m.Limits.HookTimeouts["form.submitted"] != 9000 || m.Limits.HookTimeouts["on_cron_retry"] != 10000 {
		t.Fatalf("hook timeouts %v", m.Limits.HookTimeouts)
	}
}

func TestManifestAPIKeyIsWriteOnly(t *testing.T) {
	m := loadManifest(t)
	if m.Schema.Properties["api_key"]["writeOnly"] != true {
		t.Fatal("api_key must be writeOnly")
	}
	if m.Schema.Properties["double_opt_in"]["default"] != true {
		t.Fatal("double_opt_in must default to true")
	}
}
