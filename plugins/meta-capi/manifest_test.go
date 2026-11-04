package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strconv"
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
		Method string `json:"method"`
		Path   string `json:"path"`
		Public bool   `json:"public"`
	} `json:"routes"`
	UI           map[string]any  `json:"ui"`
	Cron         json.RawMessage `json:"cron"`
	SettingsDefs struct {
		Properties map[string]struct {
			Type      string `json:"type"`
			WriteOnly bool   `json:"writeOnly"`
			Default   any    `json:"default"`
			Pattern   string `json:"pattern"`
			Items     struct {
				Type string `json:"type"`
			} `json:"items"`
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

func TestManifestIdentityAndPermissions(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "meta-capi" || m.Version != "0.1.0" || m.SDK != "^0.8" {
		t.Fatalf("name/version/sdk = %q %q %q", m.Name, m.Version, m.SDK)
	}
	perms := slices.Clone(m.Permissions)
	slices.Sort(perms)
	if want := []string{"db", "forms.read", "http.fetch", "log", "settings"}; !slices.Equal(perms, want) {
		t.Fatalf("permissions = %v, want %v", perms, want)
	}
	if !slices.Equal(m.AllowedHosts, []string{"graph.facebook.com"}) {
		t.Fatalf("allowed_hosts = %v", m.AllowedHosts)
	}
	if len(m.Cron) != 0 {
		t.Fatalf("cron must not be declared: %s", m.Cron)
	}
	if m.UI["title"] != "Meta Conversions API" {
		t.Fatalf("ui = %v", m.UI)
	}
}

func TestManifestRoutes(t *testing.T) {
	m := loadManifest(t)
	var got []string
	for _, r := range m.Routes {
		if r.Public {
			t.Errorf("route %s %s is public", r.Method, r.Path)
		}
		got = append(got, r.Method+" "+r.Path)
	}
	slices.Sort(got)
	if want := []string{"GET /log", "POST /test"}; !slices.Equal(got, want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
}

func TestManifestSettings(t *testing.T) {
	p := loadManifest(t).SettingsDefs.Properties
	for _, k := range []string{"pixel_id", "access_token", "graph_version", "site_url", "event_name", "forms",
		"email_field", "phone_field", "name_field", "consent_field", "test_event_code"} {
		if _, ok := p[k]; !ok {
			t.Errorf("setting %q missing", k)
		}
	}
	if !p["access_token"].WriteOnly {
		t.Error("access_token must be writeOnly")
	}
	if f := p["forms"]; f.Type != "array" || f.Items.Type != "string" {
		t.Errorf("forms = %+v, want an array of strings", f)
	}
	if p["graph_version"].Default != "v26.0" || p["event_name"].Default != "Lead" {
		t.Errorf("defaults: graph_version %v, event_name %v", p["graph_version"].Default, p["event_name"].Default)
	}
	pixel := regexp.MustCompile(p["pixel_id"].Pattern)
	for in, want := range map[string]bool{"": true, "123456789012345": true, "1234": false, "12a45": false} {
		if pixel.MatchString(in) != want {
			t.Errorf("pixel_id pattern on %q = %v, want %v", in, !want, want)
		}
	}
	ver := regexp.MustCompile(p["graph_version"].Pattern)
	if !ver.MatchString("v26.0") || ver.MatchString("26.0") || ver.MatchString("v26") {
		t.Error("graph_version pattern")
	}
	site := regexp.MustCompile(p["site_url"].Pattern)
	if !site.MatchString("") || !site.MatchString("https://example.com") || site.MatchString("example.com") || site.MatchString("https://example.com/x") {
		t.Error("site_url pattern")
	}
}

func TestManifestSDKRangeMatchesGoMod(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`hatamoto\.dev/sdk v0\.(\d+)\.\d+`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("no hatamoto.dev/sdk requirement in go.mod")
	}
	minor, _ := strconv.Atoi(string(m[1]))
	rng := regexp.MustCompile(`^\^0\.(\d+)$`).FindStringSubmatch(loadManifest(t).SDK)
	if rng == nil {
		t.Fatalf("sdk range %q is not of the form ^0.N", loadManifest(t).SDK)
	}
	if low, _ := strconv.Atoi(rng[1]); minor < low {
		t.Fatalf("go.mod sdk minor %d is below the manifest range %s", minor, loadManifest(t).SDK)
	}
}

func TestMigrationShape(t *testing.T) {
	raw, err := os.ReadFile("migrations/001_deliveries.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !strings.HasPrefix(sql, "-- +goose Up") {
		t.Fatal("migration must start with -- +goose Up")
	}
	up := strings.ToUpper(sql)
	for _, bad := range []string{"FUNCTION", "TRIGGER", "PUBLIC"} {
		if strings.Contains(up, bad) {
			t.Errorf("migration contains %s", bad)
		}
	}
	if !strings.Contains(sql, "submission_id text NOT NULL UNIQUE") {
		t.Error("submission_id must be unique")
	}
}
