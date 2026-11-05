package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"unicode/utf8"
)

type manifest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
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
	if m.Name != "crm-sync" || m.Version != "0.1.1" {
		t.Fatalf("name %q version %q", m.Name, m.Version)
	}
	want := []string{"db", "forms.read", "http.fetch", "kv", "log", "settings"}
	got := slices.Clone(m.Permissions)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	hosts := slices.Clone(m.AllowedHosts)
	slices.Sort(hosts)
	if !slices.Equal(hosts, []string{"*.my.salesforce.com", "*.pipedrive.com", "api.hubapi.com"}) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
}

func TestManifestDescriptionLength(t *testing.T) {
	n := utf8.RuneCountInString(loadManifest(t).Description)
	if n < 20 || n > 160 {
		t.Fatalf("description is %d characters, want 20 to 160", n)
	}
	m := loadManifest(t)
	for k, v := range m.Schema.Properties {
		if v["description"] == m.Description {
			t.Fatalf("setting %s repeats the plugin description", k)
		}
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
	if len(m.Routes) != 5 || !seen["GET /log"] || !seen["POST /test"] || !seen["GET /fields"] || !seen["GET /map"] || !seen["PUT /map"] {
		t.Fatalf("routes %+v", m.Routes)
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must be absent (owner only)")
	}
}

func TestManifestCronsAndHookTimeouts(t *testing.T) {
	m := loadManifest(t)
	every := map[string]string{}
	for _, c := range m.Cron {
		every[c.Name] = c.Every
	}
	if len(m.Cron) != 3 || every["deliver"] != "1m" || every["retry"] != "5m" || every["refresh"] != "30m" {
		t.Fatalf("cron %+v", m.Cron)
	}
	want := map[string]int{"form.submitted": 3000, "on_cron_deliver": 10000, "on_cron_retry": 10000, "on_cron_refresh": 10000, "http_handle": 10000}
	got := m.Limits.HookTimeouts
	if len(got) != len(want) {
		t.Fatalf("hook timeouts %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("hook timeouts %v", got)
		}
	}
}

func TestManifestSecretsAreWriteOnly(t *testing.T) {
	m := loadManifest(t)
	for _, k := range []string{"access_token", "api_token", "client_secret"} {
		if m.Schema.Properties[k]["writeOnly"] != true {
			t.Fatalf("%s must be writeOnly", k)
		}
	}
}

func TestManifestSettingsSchema(t *testing.T) {
	p := loadManifest(t).Schema.Properties
	if _, ok := p["field_map"]; ok {
		t.Fatal("the mapping lives in kv, not in settings")
	}
	if !equalStrings(p["provider"]["enum"], "hubspot", "pipedrive", "salesforce") {
		t.Fatalf("provider enum %v", p["provider"]["enum"])
	}
	if !equalStrings(p["object"]["enum"], "lead", "contact") {
		t.Fatalf("object enum %v", p["object"]["enum"])
	}
	for _, k := range []string{"add_note", "create_lead"} {
		if p[k]["default"] != true {
			t.Fatalf("%s must default to true", k)
		}
	}
	if p["forms"]["x-hm-field"] != "lines" {
		t.Fatal("forms must be a lines field")
	}
	for k, v := range p {
		if v["type"] == "string" && v["enum"] == nil && v["maxLength"] == nil {
			t.Fatalf("string setting %s has no maxLength", k)
		}
	}
}

func equalStrings(v any, want ...string) bool {
	list, _ := v.([]any)
	if len(list) != len(want) {
		return false
	}
	for i, e := range list {
		if s, _ := e.(string); s != want[i] {
			return false
		}
	}
	return true
}
