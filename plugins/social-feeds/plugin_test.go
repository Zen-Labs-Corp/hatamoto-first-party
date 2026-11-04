package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type manifest struct {
	Name           string   `json:"name"`
	SDK            string   `json:"sdk"`
	Permissions    []string `json:"permissions"`
	AllowedHosts   []string `json:"allowed_hosts"`
	BuilderSources []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Class string `json:"class"`
	} `json:"builder_sources"`
	Routes []struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Public bool   `json:"public"`
	} `json:"routes"`
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
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

func sorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func TestManifestPermissionsAndHosts(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "social-feeds" || m.SDK != "^0.11" {
		t.Fatalf("name %q sdk %q", m.Name, m.SDK)
	}
	want := []string{"settings", "http.fetch", "db", "kv", "builder.data", "log"}
	if !slices.Equal(sorted(m.Permissions), sorted(want)) {
		t.Fatalf("permissions = %v", m.Permissions)
	}
	hosts := []string{"api.instagram.com", "graph.instagram.com", "www.googleapis.com"}
	if !slices.Equal(sorted(m.AllowedHosts), sorted(hosts)) {
		t.Fatalf("allowed_hosts = %v", m.AllowedHosts)
	}
}

func TestManifestOnlyCallbackIsPublic(t *testing.T) {
	var public []string
	for _, r := range loadManifest(t).Routes {
		if r.Public {
			public = append(public, r.Method+" "+r.Path)
		}
	}
	if !slices.Equal(public, []string{"GET /oauth/callback"}) {
		t.Fatalf("public routes = %v", public)
	}
}

func TestManifestCronAndSources(t *testing.T) {
	m := loadManifest(t)
	cron := map[string]string{}
	for _, c := range m.Cron {
		cron[c.Name] = c.Every
	}
	if len(cron) != 2 || cron["refresh"] != "3h" || cron["token"] != "24h" {
		t.Fatalf("cron = %v", cron)
	}
	var names []string
	for _, s := range m.BuilderSources {
		if s.Kind != "items" || s.Class != "" {
			t.Fatalf("source %+v", s)
		}
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"instagram", "youtube"}) {
		t.Fatalf("sources = %v", names)
	}
}

func TestMigrationHasNoFunctionsTriggersOrGrants(t *testing.T) {
	raw, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if bad := regexp.MustCompile(`(?i)\b(function|trigger|grant|schema|public\.)|pg_`).FindString(string(raw)); bad != "" {
		t.Fatalf("migration contains %q", bad)
	}
}

func TestRedactStripsSecrets(t *testing.T) {
	in := `Get "https://graph.instagram.com/me/media?fields=id&access_token=IGQVsecret123&limit=5": timeout; ` +
		`https://www.googleapis.com/youtube/v3/channels?id=UC1&key=AIzaSECRET body client_secret=shh&x=1 tok-abc-def`
	out := redact(in, "tok-abc-def")
	for _, leak := range []string{"IGQVsecret123", "AIzaSECRET", "shh", "tok-abc-def"} {
		if strings.Contains(out, leak) {
			t.Fatalf("%q leaked in %q", leak, out)
		}
	}
	if !strings.Contains(out, "fields=id") || !strings.Contains(out, "limit=5") || !strings.Contains(out, "id=UC1") {
		t.Fatalf("over-redacted: %q", out)
	}
	if got := redact("monkey=banana"); got != "monkey=banana" {
		t.Fatalf("key= matched inside a word: %q", got)
	}
}

func TestLoadSettingsClampsAndTrims(t *testing.T) {
	for in, want := range map[string]int{`{"items":0}`: 9, `{"items":500}`: 48, `{"items":12}`: 12, `{}`: 9} {
		callHost = func(_ string, _, out any) error { return json.Unmarshal([]byte(in), out) }
		if got := loadSettings().Items; got != want {
			t.Fatalf("%s: items = %d, want %d", in, got, want)
		}
	}
	callHost = func(_ string, _, out any) error {
		return json.Unmarshal([]byte(`{"public_url":" https://example.com/ "}`), out)
	}
	if got := loadSettings().redirectURI(); got != "https://example.com/p/social-feeds/oauth/callback" {
		t.Fatalf("redirect = %q", got)
	}
}
