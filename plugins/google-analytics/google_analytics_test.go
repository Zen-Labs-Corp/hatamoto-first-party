package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// head runs the registered render.head filter with the stored setting fake.
func head(t *testing.T, settings string, hostErr error) []sdk.HeadTag {
	t.Helper()
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		if hostErr != nil {
			return hostErr
		}
		return json.Unmarshal([]byte(settings), out)
	}
	in, _ := json.Marshal(sdk.HeadContext{Path: "/"})
	out, err := guest.Handle(sdk.RenderHead.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var c sdk.HeadContext
	if err := json.Unmarshal(out, &c); err != nil {
		t.Fatal(err)
	}
	return c.Tags
}

func TestValidIDsEmitOneMetaUppercased(t *testing.T) {
	for in, want := range map[string]string{
		"G-ABC123XYZ9": "G-ABC123XYZ9",
		"g-abc123":     "G-ABC123",
		" G-ABC123 ":   "G-ABC123",
		"GT-ABCD12":    "GT-ABCD12",
	} {
		tags := head(t, `{"measurement_id":"`+in+`"}`, nil)
		if len(tags) != 1 || tags[0].Kind != "meta" ||
			tags[0].Attrs["name"] != "google-analytics-id" || tags[0].Attrs["content"] != want {
			t.Errorf("%q: tags = %+v, want one meta %q", in, tags, want)
		}
	}
}

func TestNoTagForEmptyMissingOrInvalidID(t *testing.T) {
	for _, s := range []string{
		`{}`, `{"measurement_id":""}`, `{"measurement_id":"  "}`,
		`{"measurement_id":"G-AB"}`, `{"measurement_id":"G-ABCDEFGHIJKLMNOPQ"}`,
		`{"measurement_id":"UA-123-1"}`, `{"measurement_id":"GTM-ABC1"}`,
		`{"measurement_id":"G-ABC_123"}`, `{"measurement_id":"G-ABC1\"><script>"}`,
	} {
		if tags := head(t, s, nil); len(tags) != 0 {
			t.Errorf("%s: tags = %+v, want none", s, tags)
		}
	}
}

func TestSettingsErrorNeverFailsTheRender(t *testing.T) {
	if tags := head(t, "", errors.New("unavailable")); len(tags) != 0 {
		t.Fatalf("tags = %+v, want none", tags)
	}
}

// The loader is browser JavaScript, so core links it only with site.scripts.
func TestManifestAsksForSettingsAndSiteScripts(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m sdk.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Permissions) != 2 || m.Permissions[0] != sdk.PermSettings || m.Permissions[1] != "site.scripts" {
		t.Fatalf("permissions = %v, want [settings site.scripts]", m.Permissions)
	}
}

// plugin.js is plain browser JS; loader_test.js runs it, this pins its contract.
func TestLoaderScriptContract(t *testing.T) {
	raw, err := os.ReadFile("assets/plugin.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	for _, want := range []string{
		`meta[name="google-analytics-id"]`,
		`/^(G|GT)-[A-Z0-9]{4,16}$/`,
		`gtag/js?id=`,
		`hatamoto-ga-exclude`,
		`#ga-exclude`, `#ga-include`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("plugin.js lacks %q", want)
		}
	}
	for _, bad := range []string{"noscript", "<iframe", "=>", "const ", "let ", ".style", "cssText", "innerHTML"} {
		if strings.Contains(js, bad) {
			t.Errorf("plugin.js must not contain %q", bad)
		}
	}
}
