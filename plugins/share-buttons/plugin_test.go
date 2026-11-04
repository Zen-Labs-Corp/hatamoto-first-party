package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk/guest"
)

// fakeSettings serves a settings.get fixture (or an error) to the funcs.
func fakeSettings(t *testing.T, fixture string, hostErr error) {
	t.Helper()
	cache.ok = false
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		if hostErr != nil {
			return hostErr
		}
		return json.Unmarshal([]byte(fixture), out)
	}
}

// list collects network(0), network(1), ... until the end marker.
func list(t *testing.T, fixture string, hostErr error) []string {
	t.Helper()
	fakeSettings(t, fixture, hostErr)
	var out []string
	for i := 0; ; i++ {
		n, err := network([]json.RawMessage{json.RawMessage(strconv.Itoa(i))})
		if err != nil {
			t.Fatal(err)
		}
		if n == "-" {
			return out
		}
		out = append(out, n)
	}
}

func TestNetworksFallBackToDefaults(t *testing.T) {
	for name, c := range map[string]struct {
		fixture string
		err     error
	}{
		"host error":    {"", errors.New("denied")},
		"empty object":  {`{}`, nil},
		"empty list":    {`{"networks":[]}`, nil},
		"unknown only":  {`{"networks":["myspace","icq"]}`, nil},
		"wrong type":    {`{"networks":"x"}`, nil},
		"null":          {`{"networks":null}`, nil},
		"not an object": {`[]`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := list(t, c.fixture, c.err); !reflect.DeepEqual(got, defaultNetworks) {
				t.Fatalf("got %v, want %v", got, defaultNetworks)
			}
		})
	}
}

func TestNetworksOrderSubsetDedupe(t *testing.T) {
	for fixture, want := range map[string][]string{
		`{"networks":["email","x"]}`:                 {"email", "x"},
		`{"networks":["X","x"," Email ","email"]}`:   {"x", "email"},
		`{"networks":["bluesky","nope","linkedin"]}`: {"bluesky", "linkedin"},
		`{"networks":["linkedin"],"style":7}`:        {"linkedin"},
	} {
		if got := list(t, fixture, nil); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", fixture, got, want)
		}
	}
	for _, n := range list(t, `{}`, nil) {
		if n == "bluesky" {
			t.Fatal("bluesky must only appear when listed")
		}
	}
}

func TestNetworkArguments(t *testing.T) {
	fakeSettings(t, `{"networks":["x"]}`, nil)
	for arg, want := range map[string]string{"0": "x", "1": "-", "99": "-", "1.5": "", "-1": "", `"0"`: "", "null": "", `{}`: ""} {
		got, err := network([]json.RawMessage{json.RawMessage(arg)})
		if err != nil || got != want {
			t.Errorf("network(%s) = %q, %v; want %q", arg, got, err, want)
		}
	}
	if got, err := network(nil); err != nil || got != "" {
		t.Errorf("network() = %q, %v", got, err)
	}
}

func TestStyle(t *testing.T) {
	for fixture, want := range map[string]string{
		`{}`: "both", `{"style":""}`: "both", `{"style":"bogus"}`: "both", `{"style":5}`: "both",
		`{"style":"icons"}`: "icons", `{"style":"labels"}`: "labels", `{"style":"both"}`: "both",
	} {
		fakeSettings(t, fixture, nil)
		if got, err := style(nil); err != nil || got != want {
			t.Errorf("%s: style = %q, %v; want %q", fixture, got, err, want)
		}
	}
	fakeSettings(t, "", errors.New("denied"))
	if got, _ := style(nil); got != "both" {
		t.Errorf("host error: style = %q", got)
	}
}

func TestSiteOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com":   "https://example.com",
		"https://example.com/":  "https://example.com",
		" https://example.com ": "https://example.com",
		"http://localhost:8080": "http://localhost:8080",
		"ftp://x":               "",
		"//x":                   "",
		"https://u:p@x":         "",
		"https://x/a":           "",
		"https://x?q=1":         "",
		"https://x/#f":          "",
		"javascript:alert(1)":   "",
		"example.com":           "",
		"":                      "",
	} {
		if got := siteOrigin(in); got != want {
			t.Errorf("siteOrigin(%q) = %q, want %q", in, got, want)
		}
	}
	if got := siteOrigin("https://" + strings.Repeat("a", 200) + ".com"); got != "" {
		t.Errorf("over-long origin accepted: %q", got)
	}
}

func TestPageURL(t *testing.T) {
	fakeSettings(t, `{"site_url":"https://example.com/"}`, nil)
	for path, want := range map[string]string{
		"/blog/x":    "https://example.com/blog/x",
		"/":          "https://example.com/",
		"blog":       "",
		"":           "",
		"//evil.com": "",
		`/a\b`:       "",
		"/a\nb":      "",
		"/a\x7fb":    "",
	} {
		raw, _ := json.Marshal(path)
		if got, err := pageURL([]json.RawMessage{raw}); err != nil || got != want {
			t.Errorf("pageURL(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	if got, _ := pageURL(nil); got != "" {
		t.Errorf("pageURL() = %q", got)
	}
	if got, _ := pageURL([]json.RawMessage{json.RawMessage(`5`)}); got != "" {
		t.Errorf("pageURL(5) = %q", got)
	}
	for _, f := range []string{`{}`, `{"site_url":""}`, `{"site_url":"nope"}`, `{"site_url":3}`} {
		fakeSettings(t, f, nil)
		if got, _ := pageURL([]json.RawMessage{json.RawMessage(`"/a"`)}); got != "" {
			t.Errorf("%s: pageURL = %q", f, got)
		}
	}
	fakeSettings(t, "", errors.New("denied"))
	if got, _ := pageURL([]json.RawMessage{json.RawMessage(`"/a"`)}); got != "" {
		t.Errorf("host error: pageURL = %q", got)
	}
}

func TestFuncRoundTrip(t *testing.T) {
	fakeSettings(t, `{"networks":["bluesky","email"]}`, nil)
	out, err := guest.Handle(guest.FuncExport("network"), []byte(`[0]`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "bluesky" {
		t.Fatalf("network [0] = %q", out)
	}
}

func TestSettingsAreCachedBriefly(t *testing.T) {
	clock := time.Unix(1000, 0)
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = time.Now })
	calls := 0
	value := `{"style":"icons"}`
	cache.ok = false
	callHost = func(_ string, _, out any) error {
		calls++
		return json.Unmarshal([]byte(value), out)
	}
	render := func() string {
		for i := 0; i < 6; i++ {
			if _, err := network([]json.RawMessage{json.RawMessage(strconv.Itoa(i))}); err != nil {
				t.Fatal(err)
			}
		}
		s, _ := style(nil)
		return s
	}
	if got := render(); got != "icons" || calls != 1 {
		t.Fatalf("first render: style %q, %d settings.get calls, want icons and 1", got, calls)
	}
	value = `{"style":"labels"}`
	clock = clock.Add(settingsTTL - time.Millisecond)
	if got := render(); got != "icons" || calls != 1 {
		t.Fatalf("within the TTL: style %q, %d calls, want the cached icons and 1", got, calls)
	}
	clock = clock.Add(2 * time.Millisecond)
	if got := render(); got != "labels" || calls != 2 {
		t.Fatalf("after the TTL: style %q, %d calls, want labels and 2", got, calls)
	}
	callHost = func(string, any, any) error { calls++; return errors.New("boom") }
	clock = clock.Add(settingsTTL)
	render()
	before := calls
	render()
	if calls == before {
		t.Error("a failed read was cached")
	}
}
