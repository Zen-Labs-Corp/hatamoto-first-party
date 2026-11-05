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
	if m.Name != "ratings" || m.SDK != "^0.18" || m.Version != "0.1.0" {
		t.Fatalf("name %q sdk %q version %q", m.Name, m.SDK, m.Version)
	}
}

func TestManifestPermissionsCookiesAndFuncs(t *testing.T) {
	m := loadManifest(t)
	got := slices.Clone(m.Permissions)
	slices.Sort(got)
	if !slices.Equal(got, []string{"db", "settings"}) {
		t.Fatalf("permissions %v", got)
	}
	if !slices.Equal(m.Cookies, []string{"voter"}) {
		t.Fatalf("cookies %v", m.Cookies)
	}
	if !slices.Equal(m.RequestHeaders, []string{"sec-fetch-site"}) {
		t.Fatalf("request headers %v", m.RequestHeaders)
	}
	if !slices.Equal(m.Funcs, []string{"summary", "enabled"}) {
		t.Fatalf("funcs %v", m.Funcs)
	}
}

func TestManifestRoutesRegisterOnAServeMux(t *testing.T) {
	m := loadManifest(t)
	if len(m.Routes) != 2 {
		t.Fatalf("%d routes", len(m.Routes))
	}
	mux := http.NewServeMux()
	for _, r := range m.Routes {
		mux.HandleFunc(r.Method+" "+r.Path, func(http.ResponseWriter, *http.Request) {})
		if want := r.Method == "POST" && r.Path == "/rate"; r.Public != want {
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
	for _, bad := range []string{"writeOnly", "entities.read", "http.fetch", "site.scripts"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("plugin.json contains %q", bad)
		}
	}
	var schema struct {
		Properties struct {
			Schemas struct {
				Default []string `json:"default"`
			} `json:"schemas"`
			AllowChange struct {
				Type    string `json:"type"`
				Default *bool  `json:"default"`
			} `json:"allow_change"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(doc["settings_schema"], &schema); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(schema.Properties.Schemas.Default, []string{"post"}) {
		t.Errorf("schemas default %v", schema.Properties.Schemas.Default)
	}
	if a := schema.Properties.AllowChange; a.Type != "boolean" || a.Default == nil || *a.Default {
		t.Errorf("allow_change %+v", a)
	}
}

func TestMigrationShape(t *testing.T) {
	files, _ := filepath.Glob("migrations/*.sql")
	if len(files) == 0 {
		t.Fatal("no migrations")
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if bad := regexp.MustCompile(`(?i)\b(function|trigger|grant|schema|public|copy)\b|pg_`).FindString(string(raw)); bad != "" {
			t.Fatalf("%s contains %q", file, bad)
		}
		if strings.Contains(string(raw), "-- +goose Down") {
			t.Fatalf("%s has a Down section", file)
		}
	}
	raw, _ := os.ReadFile("migrations/001_init.sql")
	for _, want := range []string{"CREATE TABLE votes", "PRIMARY KEY (entity_id, voter)", "stars BETWEEN 1 AND 5", "CREATE TABLE pages"} {
		if !strings.Contains(regexp.MustCompile(`\s+`).ReplaceAllString(string(raw), " "), want) {
			t.Errorf("migration lacks %q", want)
		}
	}
}
