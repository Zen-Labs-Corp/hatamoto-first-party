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
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
	UI     map[string]any `json:"ui"`
	Limits struct {
		MemoryPages  int            `json:"memory_pages"`
		MaxInstances int            `json:"max_instances"`
		HookTimeouts map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
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

func TestManifestIdentityPermissionsHosts(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "ads-conversions" || m.Version != "0.1.0" || m.SDK != "^0.8" {
		t.Fatalf("name/version/sdk = %q %q %q", m.Name, m.Version, m.SDK)
	}
	perms := slices.Clone(m.Permissions)
	slices.Sort(perms)
	if want := []string{"db", "forms.read", "http.fetch", "kv", "log", "mail.send", "settings", "site.scripts"}; !slices.Equal(perms, want) {
		t.Fatalf("permissions = %v, want %v", perms, want)
	}
	hosts := slices.Clone(m.AllowedHosts)
	slices.Sort(hosts)
	want := []string{"api.linkedin.com", "business-api.tiktok.com", "datamanager.googleapis.com", "oauth2.googleapis.com", "www.linkedin.com"}
	if !slices.Equal(hosts, want) {
		t.Fatalf("allowed_hosts = %v, want %v", hosts, want)
	}
	if m.UI["title"] != "Server-side conversions" {
		t.Fatalf("ui = %v", m.UI)
	}
}

func TestManifestRoutesCronAndLimits(t *testing.T) {
	m := loadManifest(t)
	var got, public []string
	for _, r := range m.Routes {
		got = append(got, r.Method+" "+r.Path)
		if r.Public {
			public = append(public, r.Method+" "+r.Path)
		}
	}
	slices.Sort(got)
	slices.Sort(public)
	wantAll := []string{"GET /connect/{platform}", "GET /log", "GET /oauth/{platform}/callback", "POST /click", "POST /disconnect/{platform}", "POST /test"}
	if !slices.Equal(got, wantAll) {
		t.Fatalf("routes = %v, want %v", got, wantAll)
	}
	if want := []string{"GET /oauth/{platform}/callback", "POST /click"}; !slices.Equal(public, want) {
		t.Fatalf("public routes = %v, want %v", public, want)
	}
	crons := map[string]string{}
	for _, c := range m.Cron {
		crons[c.Name] = c.Every
	}
	if len(crons) != 3 || crons["deliver"] != "1m" || crons["retry"] != "5m" || crons["refresh"] != "12h" {
		t.Fatalf("cron = %v", crons)
	}
	if m.Limits.MemoryPages != 256 || m.Limits.MaxInstances != 4 {
		t.Fatalf("limits = %+v", m.Limits)
	}
	wantT := map[string]int{"form.submitted": 9000, "on_cron_deliver": 10000, "on_cron_retry": 10000, "on_cron_refresh": 10000}
	if len(m.Limits.HookTimeouts) != len(wantT) {
		t.Fatalf("hook_timeouts_ms = %v", m.Limits.HookTimeouts)
	}
	for k, v := range wantT {
		if m.Limits.HookTimeouts[k] != v {
			t.Errorf("hook_timeouts_ms[%s] = %d, want %d", k, m.Limits.HookTimeouts[k], v)
		}
	}
}

func TestManifestSettings(t *testing.T) {
	p := loadManifest(t).SettingsDefs.Properties
	for _, k := range []string{"public_url", "notify_email", "forms", "email_field", "phone_field", "first_name_field", "last_name_field",
		"consent_field", "default_country_code", "value", "currency",
		"google_enabled", "google_client_id", "google_client_secret", "google_customer_id", "google_login_customer_id", "google_conversion_action_id",
		"tiktok_enabled", "tiktok_pixel_code", "tiktok_access_token", "tiktok_event", "tiktok_test_event_code",
		"linkedin_enabled", "linkedin_client_id", "linkedin_client_secret", "linkedin_conversion_rule_id", "linkedin_ad_account_id", "linkedin_version"} {
		if _, ok := p[k]; !ok {
			t.Errorf("setting %q missing", k)
		}
	}
	if _, ok := p["google_developer_token"]; ok {
		t.Error("no developer token setting")
	}
	for k, def := range p {
		secret := strings.HasSuffix(k, "_secret") || strings.HasSuffix(k, "_token")
		if def.WriteOnly != secret {
			t.Errorf("%s: writeOnly = %v, want %v", k, def.WriteOnly, secret)
		}
	}
	if f := p["forms"]; f.Type != "array" || f.Items.Type != "string" {
		t.Errorf("forms = %+v, want an array of strings", f)
	}
	if p["tiktok_event"].Default != "SubmitForm" || p["linkedin_version"].Default != "202610" {
		t.Errorf("defaults: %v %v", p["tiktok_event"].Default, p["linkedin_version"].Default)
	}
	check := func(key string, in map[string]bool) {
		re := regexp.MustCompile(p[key].Pattern)
		for v, want := range in {
			if re.MatchString(v) != want {
				t.Errorf("%s pattern on %q = %v, want %v", key, v, !want, want)
			}
		}
	}
	check("public_url", map[string]bool{"": true, "https://example.com": true, "example.com": false, "https://example.com/x": false})
	check("currency", map[string]bool{"": true, "EUR": true, "eur": false, "EURO": false})
	check("default_country_code", map[string]bool{"": true, "49": true, "1234": false, "+49": false})
	check("linkedin_version", map[string]bool{"202610": true, "2026": false, "v202610": false})
	check("tiktok_event", map[string]bool{"SubmitForm": true, "1x": false})
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
	raw, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !strings.HasPrefix(sql, "-- +goose Up") {
		t.Fatal("migration must start with -- +goose Up")
	}
	up := strings.ToUpper(sql)
	for _, bad := range []string{"FUNCTION", "TRIGGER", "PUBLIC", "+GOOSE DOWN"} {
		if strings.Contains(up, bad) {
			t.Errorf("migration contains %s", bad)
		}
	}
	for _, want := range []string{"CREATE TABLE queue", "CREATE TABLE clicks", "CREATE TABLE log", "UNIQUE (submission_id, platform)"} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration lacks %q", want)
		}
	}
}
