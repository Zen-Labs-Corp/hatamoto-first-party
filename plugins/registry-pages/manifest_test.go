package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func rawManifest(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func loadManifest(t *testing.T) sdk.Manifest {
	t.Helper()
	var m sdk.Manifest
	if err := json.Unmarshal(rawManifest(t), &m); err != nil {
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
	if m.Name != "registry-pages" || m.SDK != "^0.29" || m.Version != "0.1.0" {
		t.Fatalf("name %q sdk %q version %q", m.Name, m.SDK, m.Version)
	}
}

func TestManifestSDKMatchesGoMod(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`hatamoto\.dev/sdk v0\.(\d+)\.\d+(-rc\.\d+)?`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod has no hatamoto.dev/sdk requirement")
	}
	if want := "^0." + string(m[1]); loadManifest(t).SDK != want {
		t.Fatalf("plugin.json sdk %q, go.mod requires %q", loadManifest(t).SDK, want)
	}
}

func TestManifestPermissions(t *testing.T) {
	got := slices.Clone(loadManifest(t).Permissions)
	slices.Sort(got)
	want := []string{"entities.read", "entities.write.own", "http.fetch", "kv"}
	if !slices.Equal(got, want) {
		t.Fatalf("permissions %v, want %v", got, want)
	}
	raw := string(rawManifest(t))
	for _, bad := range []string{"writeOnly", `"db"`, `"settings"`, "site.scripts", "mail.send", "http.fetch.any", `"*"`, "settings_schema", `"routes"`} {
		if strings.Contains(raw, bad) {
			t.Errorf("plugin.json contains %q", bad)
		}
	}
}

func TestManifestHostsAndPages(t *testing.T) {
	var doc struct {
		AllowedHosts     []string `json:"allowed_hosts"`
		EntitySchema     string   `json:"entity_schema"`
		EntityFields     []string `json:"entity_fields"`
		PagePaths        []string `json:"page_paths"`
		PagePathPrefixes []string `json:"page_path_prefixes"`
		Cron             []struct {
			Name  string `json:"name"`
			Every string `json:"every"`
		} `json:"cron"`
	}
	if err := json.Unmarshal(rawManifest(t), &doc); err != nil {
		t.Fatal(err)
	}
	if want := []string{"hatamoto.dev"}; !slices.Equal(doc.AllowedHosts, want) {
		t.Errorf("allowed_hosts %v", doc.AllowedHosts)
	}
	if doc.EntitySchema != "registry-entry" {
		t.Errorf("entity_schema %q", doc.EntitySchema)
	}
	if want := []string{"kind", "meta_title", "meta_description", "canonical_url"}; !slices.Equal(doc.EntityFields, want) {
		t.Errorf("entity_fields %v", doc.EntityFields)
	}
	if want := []string{"/plugins", "/themes"}; !slices.Equal(doc.PagePaths, want) {
		t.Errorf("page_paths %v", doc.PagePaths)
	}
	seen := map[string]bool{}
	for _, p := range doc.PagePaths {
		if seen[p] {
			t.Errorf("page path %q twice", p)
		}
		seen[p] = true
		if strings.ContainsAny(p, "?#*") || strings.HasSuffix(p, "/") {
			t.Errorf("page path %q has a query, fragment, wildcard or trailing slash", p)
		}
	}
	if want := []string{"/plugins/", "/themes/"}; !slices.Equal(doc.PagePathPrefixes, want) {
		t.Errorf("page_path_prefixes %v", doc.PagePathPrefixes)
	}
	reserved := []string{"/p/", "/api/", "/uploads/", "/media/", "/theme/", "/-/", "/admin/", "/mcp/"}
	for _, p := range doc.PagePathPrefixes {
		if !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") {
			t.Errorf("prefix %q must start and end with /", p)
		}
		for _, r := range reserved {
			if strings.HasPrefix(p, r) {
				t.Errorf("prefix %q starts with reserved %q", p, r)
			}
		}
	}
	if len(doc.Cron) != 1 || doc.Cron[0].Name != "sync" || doc.Cron[0].Every != "1m" {
		t.Errorf("cron %+v", doc.Cron)
	}
}

func TestSchemaFileMatchesEntityFields(t *testing.T) {
	raw, err := os.ReadFile("schemas/registry-entry.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	var props []string
	for k := range schema.Properties {
		props = append(props, k)
	}
	fields := slices.Clone(loadManifest(t).EntityFields)
	slices.Sort(props)
	slices.Sort(fields)
	if !slices.Equal(props, fields) {
		t.Fatalf("schema properties %v, entity_fields %v", props, fields)
	}
	if want := []string{"plugin", "theme"}; !slices.Equal(schema.Properties["kind"].Enum, want) {
		t.Errorf("kind enum %v", schema.Properties["kind"].Enum)
	}
}
