package main

import (
	"encoding/json"
	"errors"
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

// args builds template arguments from Go values.
func args(t *testing.T, vs ...any) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	for _, v := range vs {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

func TestValidLink(t *testing.T) {
	long := "https://cal.com/" + strings.Repeat("a", maxLink)
	for link, want := range map[string]bool{
		"https://cal.com/acme/30min":         true,
		"https://app.cal.com/x":              true,
		"https://a.b.cal.com/x":              true,
		"https://calendly.com/acme/30min":    true,
		"https://cal.com/acme?month=2026-11": true,
		"http://cal.com/x":                   false,
		"javascript:alert(1)":                false,
		"https://evil-cal.com/x":             false,
		"https://cal.com.evil.com/x":         false,
		"https://evilcal.com/x":              false,
		"https://.cal.com/x":                 false,
		"https://a..cal.com/x":               false,
		"https://user:pw@cal.com/x":          false,
		"https://user@cal.com/x":             false,
		"https://cal.com:8443/x":             false,
		"https://cal.com:443/x":              false,
		"https://CAL.com/x":                  false,
		"https://cal.com":                    false,
		"https://cal.com/":                   false,
		"https://cal.com\\@evil.com/x":       false,
		"https://cal.com/a b":                false,
		"https://cal.com/a\nb":               false,
		"//cal.com/x":                        false,
		"https:cal.com/x":                    false,
		"https://calendly.com.evil.com/x":    false,
		"https://www.calendly.com/x":         false,
		long:                                 false,
		"":                                   false,
	} {
		if _, _, ok := validLink(link); ok != want {
			t.Errorf("validLink(%q) = %v, want %v", link, ok, want)
		}
	}
}

func TestValidLinkDropsFragmentAndKeepsParts(t *testing.T) {
	u, p, ok := validLink("https://cal.com/acme/30min?x=1#frag")
	if !ok || p != providerCal || u.String() != "https://cal.com/acme/30min?x=1" {
		t.Fatalf("got %v %q %v", u, p, ok)
	}
}

func TestEmbedURL(t *testing.T) {
	fakeSettings(t, `{"link":"https://cal.com/acme/30min"}`, nil)
	if got, _ := embedURL(nil); got != "https://cal.com/acme/30min?embed=true" {
		t.Errorf("setting link: %q", got)
	}
	if got, _ := embedURL(args(t, "https://calendly.com/acme/talk")); got != "https://calendly.com/acme/talk?embed_type=Inline" {
		t.Errorf("override: %q", got)
	}
	if got, _ := embedURL(args(t, "https://cal.com/a?theme=dark")); got != "https://cal.com/a?embed=true&theme=dark" {
		t.Errorf("existing query: %q", got)
	}
	if got, _ := embedURL(args(t, "https://calendly.com/acme/talk", "plain")); got != "https://calendly.com/acme/talk" {
		t.Errorf("plain: %q", got)
	}
	for name, a := range map[string][]json.RawMessage{
		"hostile override": args(t, "https://evil.com/x"),
		"http override":    args(t, "http://cal.com/x"),
	} {
		if got, _ := embedURL(a); got != "" {
			t.Errorf("%s: %q, want empty (no fallback to the setting)", name, got)
		}
	}
	for name, a := range map[string][]json.RawMessage{
		"nil":    nil,
		"null":   args(t, nil),
		"number": args(t, 5),
		"empty":  args(t, ""),
		"object": args(t, map[string]string{"a": "b"}),
	} {
		if got, _ := embedURL(a); got != "https://cal.com/acme/30min?embed=true" {
			t.Errorf("%s: %q, want the setting link", name, got)
		}
	}
}

func TestEmbedURLWithoutValidSetting(t *testing.T) {
	for name, c := range map[string]struct {
		fixture string
		err     error
	}{
		"host error": {"", errors.New("denied")},
		"empty":      {`{}`, nil},
		"hostile":    {`{"link":"https://evil.com/x"}`, nil},
		"wrong type": {`{"link":5}`, nil},
		"not object": {`[]`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			fakeSettings(t, c.fixture, c.err)
			if got, err := embedURL(nil); got != "" || err != nil {
				t.Errorf("embedURL = %q, %v", got, err)
			}
			if got, err := bookingURL(nil); got != "" || err != nil {
				t.Errorf("bookingURL = %q, %v", got, err)
			}
		})
	}
}

func TestBookingURLOrder(t *testing.T) {
	fakeSettings(t, `{"link":"https://cal.com/acme/30min"}`, nil)
	if got, _ := bookingURL(args(t, "https://calendly.com/other/x")); got != "https://cal.com/acme/30min" {
		t.Errorf("setting wins: %q", got)
	}
	fakeSettings(t, `{"link":"https://evil.com/x"}`, nil)
	if got, _ := bookingURL(args(t, "https://calendly.com/other/x")); got != "https://calendly.com/other/x" {
		t.Errorf("valid fallback: %q", got)
	}
	for name, a := range map[string][]json.RawMessage{
		"hostile": args(t, "javascript:alert(1)"),
		"number":  args(t, 7),
		"null":    args(t, nil),
		"none":    nil,
	} {
		if got, _ := bookingURL(a); got != "" {
			t.Errorf("%s: %q, want empty", name, got)
		}
	}
}

func TestButtonLabel(t *testing.T) {
	for fixture, want := range map[string]string{
		`{}`:                             defaultLabel,
		`{"button_label":"  "}`:          defaultLabel,
		`{"button_label":5}`:             defaultLabel,
		`{"button_label":"  Meet us  "}`: "Meet us",
		`{"button_label":"a\u0000b"}`:    defaultLabel,
		`{"button_label":"` + strings.Repeat("é", 80) + `"}`: strings.Repeat("é", maxLabel),
	} {
		fakeSettings(t, fixture, nil)
		if got, _ := buttonLabel(nil); got != want {
			t.Errorf("%.40s: label = %q, want %q", fixture, got, want)
		}
	}
	fakeSettings(t, "", errors.New("denied"))
	if got, _ := buttonLabel(nil); got != defaultLabel {
		t.Errorf("host error: %q", got)
	}
}

func TestHeightClamp(t *testing.T) {
	for fixture, want := range map[string]string{
		`{}`:                "700",
		`{"height":"tall"}`: "700",
		`{"height":null}`:   "700",
		`{"height":100}`:    "300",
		`{"height":5000}`:   "2000",
		`{"height":-1}`:     "300",
		`{"height":900}`:    "900",
		`{"height":850.9}`:  "850",
		`{"height":1e400}`:  "700",
	} {
		fakeSettings(t, fixture, nil)
		if got, _ := height(nil); got != want {
			t.Errorf("%s: height = %q, want %q", fixture, got, want)
		}
	}
}

func TestProviderName(t *testing.T) {
	fakeSettings(t, `{"link":"https://calendly.com/a/b"}`, nil)
	if got, _ := providerName(nil); got != "Calendly" {
		t.Errorf("setting: %q", got)
	}
	if got, _ := providerName(args(t, "https://app.cal.com/x")); got != "Cal.com" {
		t.Errorf("override: %q", got)
	}
	if got, _ := providerName(args(t, "https://evil.com/x")); got != "" {
		t.Errorf("hostile: %q", got)
	}
	fakeSettings(t, `{}`, nil)
	if got, _ := providerName(nil); got != "" {
		t.Errorf("none: %q", got)
	}
}

func TestFuncRoundTrip(t *testing.T) {
	fakeSettings(t, `{"link":"https://cal.com/acme/30min"}`, nil)
	out, err := guest.Handle(guest.FuncExport("embed_url"), []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "https://cal.com/acme/30min?embed=true" {
		t.Fatalf("embed_url = %q", out)
	}
}

func TestSettingsAreCachedBriefly(t *testing.T) {
	clock := time.Unix(1000, 0)
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = time.Now })
	calls := 0
	value := `{"link":"https://cal.com/a/b"}`
	cache.ok = false
	callHost = func(_ string, _, out any) error {
		calls++
		return json.Unmarshal([]byte(value), out)
	}
	render := func() string {
		embedURL(nil)
		bookingURL(nil)
		buttonLabel(nil)
		height(nil)
		providerName(nil)
		s, _ := bookingURL(nil)
		return s
	}
	if got := render(); got != "https://cal.com/a/b" || calls != 1 {
		t.Fatalf("first render: %q, %d settings.get calls, want 1", got, calls)
	}
	value = `{"link":"https://cal.com/c/d"}`
	clock = clock.Add(settingsTTL - time.Millisecond)
	if got := render(); got != "https://cal.com/a/b" || calls != 1 {
		t.Fatalf("within the TTL: %q, %d calls, want the cached link and 1", got, calls)
	}
	clock = clock.Add(2 * time.Millisecond)
	if got := render(); got != "https://cal.com/c/d" || calls != 2 {
		t.Fatalf("after the TTL: %q, %d calls, want the new link and 2", got, calls)
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
