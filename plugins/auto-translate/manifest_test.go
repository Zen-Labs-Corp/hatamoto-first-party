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

type schemaProp struct {
	Type      string   `json:"type"`
	Enum      []string `json:"enum"`
	WriteOnly bool     `json:"writeOnly"`
	MaxLength int      `json:"maxLength"`
	MaxItems  int      `json:"maxItems"`
	Items     struct {
		Properties map[string]schemaProp `json:"properties"`
	} `json:"items"`
}

func TestManifest(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m sdk.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Name != "auto-translate" || m.Version != "0.1.0" {
		t.Fatalf("name %q version %q", m.Name, m.Version)
	}

	perms := make([]string, len(m.Permissions))
	for i, p := range m.Permissions {
		perms[i] = string(p)
	}
	slices.Sort(perms)
	if !slices.Equal(perms, []string{"entities.read", "http.fetch", "settings"}) {
		t.Fatalf("permissions %v", perms)
	}
	hosts := slices.Clone(m.AllowedHosts)
	slices.Sort(hosts)
	if !slices.Equal(hosts, []string{"api-free.deepl.com", "api.deepl.com", "translation.googleapis.com"}) {
		t.Fatalf("allowed_hosts %v", hosts)
	}
	if len(m.Routes) != 0 || len(m.Cron) != 0 || m.UI != nil {
		t.Fatalf("unexpected routes, cron or ui: %+v", m)
	}

	if len(m.Runs) != 1 {
		t.Fatalf("runs %+v", m.Runs)
	}
	r := m.Runs[0]
	if r.Name != "translate" || !slices.Equal(r.Stages, []string{"fetch", "transform"}) {
		t.Fatalf("run %+v", r)
	}
	if len(r.Inputs) != 3 {
		t.Fatalf("inputs %+v", r.Inputs)
	}
	locale, schema, missing := r.Inputs[0], r.Inputs[1], r.Inputs[2]
	if locale.Name != "locale" || locale.Type != "text" || !locale.Required {
		t.Fatalf("locale input %+v", locale)
	}
	if schema.Name != "schema" || schema.Type != "select" || !schema.Required || !slices.Equal(schema.Options, []string{"page", "post"}) {
		t.Fatalf("schema input %+v", schema)
	}
	if missing.Name != "only_missing" || missing.Type != "bool" || missing.Required {
		t.Fatalf("only_missing input %+v", missing)
	}
	if m.Limits.HookTimeoutsMS["on_run_step"] != 10000 {
		t.Fatalf("hook timeouts %v", m.Limits.HookTimeoutsMS)
	}

	var schemaDoc struct {
		Properties map[string]schemaProp `json:"properties"`
	}
	if err := json.Unmarshal(m.SettingsSchema, &schemaDoc); err != nil {
		t.Fatal(err)
	}
	props := schemaDoc.Properties
	if !props["api_key"].WriteOnly {
		t.Fatal("api_key must be a top-level writeOnly property")
	}
	g := props["glossary"]
	if g.MaxItems == 0 || g.Items.Properties["source"].MaxLength == 0 || g.Items.Properties["target"].MaxLength == 0 {
		t.Fatalf("glossary bounds %+v", g)
	}
	if !slices.Equal(props["formality"].Enum, []string{"default", "more", "less", "prefer_more", "prefer_less"}) {
		t.Fatalf("formality %v", props["formality"].Enum)
	}
	if !slices.Equal(props["provider"].Enum, []string{"deepl", "google"}) {
		t.Fatalf("provider %v", props["provider"].Enum)
	}

	// The sdk range matches the module's sdk version (^0.18 with v0.18.x).
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`hatamoto\.dev/sdk v0\.18\.\d+`).Match(mod) || m.SDK != "^0.18" {
		t.Fatalf("sdk range %q does not fit go.mod:\n%s", m.SDK, mod)
	}
	if strings.Contains(string(mod), "replace") {
		t.Fatal("go.mod has a replace")
	}
}
