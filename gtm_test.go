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

func TestValidIDEmitsOneMeta(t *testing.T) {
	tags := head(t, `{"container_id":"GTM-ABC1234"}`, nil)
	if len(tags) != 1 || tags[0].Kind != "meta" ||
		tags[0].Attrs["name"] != "gtm-container" || tags[0].Attrs["content"] != "GTM-ABC1234" {
		t.Fatalf("tags = %+v", tags)
	}
}

func TestNormalizesLowercasePaddedID(t *testing.T) {
	tags := head(t, `{"container_id":"  gtm-abc1234 "}`, nil)
	if len(tags) != 1 || tags[0].Attrs["content"] != "GTM-ABC1234" {
		t.Fatalf("tags = %+v", tags)
	}
}

func TestNoTagForEmptyMissingOrInvalidID(t *testing.T) {
	for _, s := range []string{
		`{}`, `{"container_id":""}`, `{"container_id":"  "}`,
		`{"container_id":"GTM-AB"}`, `{"container_id":"GTM-ABCDEFGHIJKLM"}`,
		`{"container_id":"UA-12345-1"}`, `{"container_id":"GTM-AB\"><script>"}`,
		`{"container_id":"GTM-ABC_123"}`,
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

func TestManifestAsksForSettingsOnly(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m sdk.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Permissions) != 1 || m.Permissions[0] != sdk.PermSettings {
		t.Fatalf("permissions = %v, want [settings]", m.Permissions)
	}
}

// plugin.js is plain browser JS with no test runner here; pin its contract.
func TestLoaderScriptContract(t *testing.T) {
	raw, err := os.ReadFile("assets/plugin.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	for _, want := range []string{
		`meta[name="gtm-container"]`,
		`/^GTM-[A-Z0-9]{4,12}$/`,
		`google_tag_manager[id]`,
		`script[src*="googletagmanager.com/gtm.js?id=`,
		`'gtm.start'`, `event: 'gtm.js'`,
		`https://www.googletagmanager.com/gtm.js?id=`,
		`s.async = true`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("plugin.js lacks %q", want)
		}
	}
	for _, bad := range []string{"noscript", "<iframe", "=>", "const ", "let "} {
		if strings.Contains(js, bad) {
			t.Errorf("plugin.js must not contain %q", bad)
		}
	}
}
