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

func TestManifestValidates(t *testing.T) {
	m := loadManifest(t)
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Name != "alt-text" || m.Version != "0.1.0" || m.SDK != "^0.18" {
		t.Fatalf("identity: %s %s %s", m.Name, m.Version, m.SDK)
	}
}

func TestManifestPermissionsAndHosts(t *testing.T) {
	m := loadManifest(t)
	perms := make([]string, 0, len(m.Permissions))
	for _, p := range m.Permissions {
		perms = append(perms, string(p))
	}
	slices.Sort(perms)
	if want := []string{"http.fetch", "media.read", "settings"}; !slices.Equal(perms, want) {
		t.Fatalf("permissions %v, want %v", perms, want)
	}
	hosts := slices.Clone(m.AllowedHosts)
	slices.Sort(hosts)
	if want := []string{"api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com"}; !slices.Equal(hosts, want) {
		t.Fatalf("allowed_hosts %v, want %v", hosts, want)
	}
}

func TestManifestRun(t *testing.T) {
	m := loadManifest(t)
	if len(m.Runs) != 1 {
		t.Fatalf("%d runs", len(m.Runs))
	}
	r := m.Runs[0]
	if r.Name != "suggest" || !slices.Equal(r.Stages, []string{"fetch", "transform"}) || !slices.Equal(r.Proposes, []string{"media_alt"}) {
		t.Fatalf("run %+v", r)
	}
	if len(r.Inputs) != 0 {
		t.Fatalf("the run declares inputs %v; core shows them as hidden fields, use settings", r.Inputs)
	}
}

func TestManifestSettings(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Settings struct {
			Properties map[string]struct {
				WriteOnly bool     `json:"writeOnly"`
				Default   any      `json:"default"`
				Enum      []string `json:"enum"`
			} `json:"properties"`
		} `json:"settings_schema"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	p := m.Settings.Properties
	for _, name := range []string{"provider", "api_key", "model", "language", "max_length", "site_url", "limit", "only_missing"} {
		if _, ok := p[name]; !ok {
			t.Errorf("setting %q missing", name)
		}
	}
	if !p["api_key"].WriteOnly {
		t.Error("api_key must be writeOnly")
	}
	for name, s := range p {
		if s.WriteOnly && name != "api_key" {
			t.Errorf("%s is writeOnly", name)
		}
	}
	if p["max_length"].Default != float64(defaultMaxLength) {
		t.Errorf("max_length default %v", p["max_length"].Default)
	}
	if !slices.Equal(p["provider"].Enum, []string{"openai", "anthropic", "gemini"}) || p["provider"].Default != "openai" {
		t.Errorf("provider: %+v", p["provider"])
	}
	if p["limit"].Default != float64(defaultLimit) || p["only_missing"].Default != true {
		t.Errorf("limit/only_missing defaults %v %v", p["limit"].Default, p["only_missing"].Default)
	}
	if p["language"].Default != "en" {
		t.Errorf("language default %v", p["language"].Default)
	}
}

func TestGoModPinsTheSDKTheManifestNames(t *testing.T) {
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	mod := string(raw)
	if !strings.Contains(mod, "hatamoto.dev/sdk v0.18.0") {
		t.Fatalf("go.mod does not require sdk v0.18.0:\n%s", mod)
	}
	if strings.Contains(mod, "replace") {
		t.Fatal("go.mod has a replace")
	}
}
