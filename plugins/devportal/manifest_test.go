package main

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
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

func TestManifestIsValidForTheSDK(t *testing.T) {
	m := loadManifest(t)
	if err := m.WithDefaults().Validate(); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.Compatible(); !ok || err != nil {
		t.Fatalf("Compatible() = %v, %v", ok, err)
	}
	if m.Name != "devportal" || m.SDK != "^0.21" || m.Version != "0.1.0" {
		t.Fatalf("name %q sdk %q version %q", m.Name, m.SDK, m.Version)
	}
}

func TestManifestPermissionsCookiesAndHosts(t *testing.T) {
	m := loadManifest(t)
	got := slices.Clone(m.Permissions)
	slices.Sort(got)
	if want := []string{"db", "http.fetch", "kv", "log", "settings"}; !slices.Equal(got, want) {
		t.Fatalf("permissions %v", got)
	}
	if !slices.Equal(m.Cookies, []string{"sid", "st", "fl"}) {
		t.Fatalf("cookies %v", m.Cookies)
	}
	if !slices.Equal(m.AllowedHosts, []string{"github.com", "api.github.com"}) {
		t.Fatalf("allowed hosts %v", m.AllowedHosts)
	}
	raw, _ := os.ReadFile("plugin.json")
	for _, bad := range []string{"site.scripts", "mail.send", `"site"`} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("plugin.json contains %q", bad)
		}
	}
}

func TestManifestRoutes(t *testing.T) {
	m := loadManifest(t)
	public := map[string]bool{
		"GET /auth/callback": true, "POST /account": true, "POST /key": true,
		"POST /logout": true, "GET /terms": true,
		"GET /admin/accounts": false, "POST /admin/accounts/{id}/suspend": false,
		"POST /admin/accounts/{id}/unsuspend": false,
	}
	if len(m.Routes) != len(public) {
		t.Fatalf("%d routes", len(m.Routes))
	}
	mux := http.NewServeMux()
	for _, r := range m.Routes {
		key := r.Method + " " + r.Path
		want, ok := public[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if r.Public != want {
			t.Errorf("route %s: public = %v", key, r.Public)
		}
		if len(r.Roles) != 0 {
			t.Errorf("route %s: roles %v", key, r.Roles)
		}
		mux.HandleFunc(key, func(http.ResponseWriter, *http.Request) {})
	}
}

func TestManifestLimitsCronAndConfig(t *testing.T) {
	m := loadManifest(t)
	if got := m.Limits.HookTimeoutsMS["render.page"]; got != 100 {
		t.Errorf("render.page timeout %d", got)
	}
	if got := m.Limits.HookTimeoutsMS["http_handle"]; got != 10000 {
		t.Errorf("http_handle timeout %d", got)
	}
	if len(m.Cron) != 1 || m.Cron[0].Name != "gc" || m.Cron[0].Every != "1h" {
		t.Errorf("cron %+v", m.Cron)
	}
	if m.UI == nil || !slices.Equal(m.UI.Roles, []string{"owner"}) {
		t.Errorf("ui %+v", m.UI)
	}
	for _, k := range []string{"github_authorize_url", "github_token_url", "github_api_url"} {
		if !strings.HasPrefix(m.Config[k], "https://") {
			t.Errorf("config %s = %q", k, m.Config[k])
		}
	}
}

func TestManifestSettingsSchema(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema struct {
			Properties map[string]struct {
				Type      string `json:"type"`
				WriteOnly bool   `json:"writeOnly"`
				Default   string `json:"default"`
			} `json:"properties"`
		} `json:"settings_schema"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	p := doc.Schema.Properties
	for _, k := range []string{"github_client_id", "github_client_secret", "review_email", "public_url", "terms_markdown"} {
		if p[k].Type != "string" {
			t.Errorf("setting %s missing or not a string", k)
		}
	}
	for k, v := range p {
		if v.WriteOnly != (k == "github_client_secret") {
			t.Errorf("setting %s writeOnly = %v", k, v.WriteOnly)
		}
	}
	if p["public_url"].Default != "https://hatamoto.dev" {
		t.Errorf("public_url default %q", p["public_url"].Default)
	}
	if strings.TrimSpace(p["terms_markdown"].Default) == "" {
		t.Error("terms_markdown has no default")
	}
}

func TestMigrationShape(t *testing.T) {
	raw, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if bad := regexp.MustCompile(`(?i)\b(function|trigger|grant|schema|public|copy)\b|pg_`).FindString(sql); bad != "" {
		t.Fatalf("migration contains %q", bad)
	}
	if strings.Contains(sql, "-- +goose Down") || !strings.Contains(sql, "-- +goose Up") {
		t.Fatal("migration needs exactly an Up section")
	}
	flat := regexp.MustCompile(`\s+`).ReplaceAllString(sql, " ")
	for _, want := range []string{"CREATE TABLE accounts", "github_id bigint NOT NULL UNIQUE", "CREATE TABLE keys", "pubkey text NOT NULL UNIQUE", "CREATE TABLE sessions"} {
		if !strings.Contains(flat, want) {
			t.Errorf("migration lacks %q", want)
		}
	}
}
