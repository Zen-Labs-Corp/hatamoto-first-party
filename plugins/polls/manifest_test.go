package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
	if m.Name != "polls" || m.SDK != "^0.11" {
		t.Fatalf("name %q sdk %q", m.Name, m.SDK)
	}
}

func TestManifestPermissionsAndSources(t *testing.T) {
	m := loadManifest(t)
	want := []string{"builder.data", "db", "forms.read", "log", "mail.send", "settings"}
	got := slices.Clone(m.Permissions)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("permissions %v", got)
	}
	if len(m.BuilderSources) != 1 || m.BuilderSources[0].Name != "results" || m.BuilderSources[0].Kind != "items" || m.BuilderSources[0].Class != "" {
		t.Fatalf("builder sources %+v", m.BuilderSources)
	}
}

func TestManifestRoutesRegisterOnAServeMux(t *testing.T) {
	m := loadManifest(t)
	if len(m.Routes) != 4 {
		t.Fatalf("%d routes", len(m.Routes))
	}
	mux := http.NewServeMux()
	for _, r := range m.Routes {
		mux.HandleFunc(r.Method+" "+r.Path, func(http.ResponseWriter, *http.Request) {})
		if r.Public != (r.Path == "/results/{form_slug}") {
			t.Errorf("route %s %s: public = %v", r.Method, r.Path, r.Public)
		}
	}
}

func TestManifestSettingsSchemaAndNoExtras(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"allowed_hosts", "site"} {
		if _, ok := doc[key]; ok {
			t.Errorf("plugin.json has %q", key)
		}
	}
	var schema map[string]any
	if err := json.Unmarshal(doc["settings_schema"], &schema); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"writeOnly", "$ref", "oneOf", "additionalProperties", "site.scripts"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("plugin.json contains %q", bad)
		}
	}
}

func TestMigrationHasNoFunctionsTriggersOrGrants(t *testing.T) {
	files, _ := filepath.Glob("migrations/*.sql")
	if len(files) == 0 {
		t.Fatal("no migrations")
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if bad := regexp.MustCompile(`(?i)\b(function|trigger|grant|schema|public|copy)\b`).FindString(string(raw)); bad != "" {
			t.Fatalf("%s contains %q", file, bad)
		}
		if strings.Contains(string(raw), "-- +goose Down") {
			t.Fatalf("%s has a Down section", file)
		}
	}
}

func TestClosesAtPatternMatchesWhatParseTimeReads(t *testing.T) {
	m := loadManifest(t)
	var schema struct {
		Properties struct {
			Forms struct {
				Items struct {
					Properties map[string]struct {
						Format  string `json:"format"`
						Pattern string `json:"pattern"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"forms"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(m.SettingsSchema, &schema); err != nil {
		t.Fatal(err)
	}
	prop := schema.Properties.Forms.Items.Properties["closes_at"]
	if prop.Format != "" {
		t.Fatalf("closes_at has format %q, the admin form would render a datetime picker", prop.Format)
	}
	re := regexp.MustCompile(prop.Pattern)
	for _, ok := range []string{"", "2027-01-01", "2027-01-01T18:00", "2027-01-01 18:00", "2027-01-01 18:00:30",
		"2027-01-01T18:00Z", "2027-01-01 18:00:30+01:00", "2027-01-01T18:00-05:00"} {
		if !re.MatchString(ok) {
			t.Errorf("pattern rejects %q", ok)
		}
		if _, parsed := parseTime(ok); ok != "" && !parsed {
			t.Errorf("parseTime rejects %q", ok)
		}
	}
	for _, bad := range []string{"soon", "04/11/2026", "2027-01-01T", "2027-01-01 18", "2027-01-01Z", "2027-01-01T18:00+0100"} {
		if re.MatchString(bad) {
			t.Errorf("pattern accepts %q", bad)
		}
	}
}
