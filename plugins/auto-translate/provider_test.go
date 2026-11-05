package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const testKey = "SECRET-KEY:fx 1/2"

type hostCall struct {
	fn   string
	args any
}

// fakeHost replaces callHost for one test; tests using it must not run in
// parallel.
type fakeHost struct {
	t        *testing.T
	settings string
	fetchRes string
	fetchErr error
	calls    []hostCall
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	f := &fakeHost{t: t}
	budgetEnd = time.Time{}
	callHost = f.call
	t.Cleanup(func() { callHost = nil; budgetEnd = time.Time{} })
	return f
}

func (f *fakeHost) call(fn string, args, out any) error {
	f.calls = append(f.calls, hostCall{fn, args})
	switch fn {
	case "settings.get":
		return json.Unmarshal([]byte(f.settings), out)
	case "http.fetch":
		if f.fetchErr != nil {
			return f.fetchErr
		}
		return json.Unmarshal([]byte(f.fetchRes), out)
	}
	f.t.Fatalf("unexpected host call %q", fn)
	return nil
}

func TestLoadSettingsTrimsAndDefaults(t *testing.T) {
	f := newFakeHost(t)
	f.settings = `{"api_key":"  k1  ","glossary":[{"source":" Hatamoto ","target":""},{"source":"  ","target":"x"},{"source":"cms","target":" CMS "}]}`
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Provider != "deepl" || s.APIKey != "k1" || s.Formality != "default" {
		t.Fatalf("settings %+v", s)
	}
	want := []glossaryEntry{{"Hatamoto", ""}, {"cms", "CMS"}}
	if len(s.Glossary) != 2 || s.Glossary[0] != want[0] || s.Glossary[1] != want[1] {
		t.Fatalf("glossary %+v", s.Glossary)
	}
	if err := s.configured(); err != nil {
		t.Fatal(err)
	}

	f.settings = `{"provider":" Google ","formality":"MORE"}`
	s, _ = loadSettings()
	if s.Provider != "google" || s.Formality != "more" {
		t.Fatalf("settings %+v", s)
	}
}

func TestLoadSettingsHostErrorHasNoDetail(t *testing.T) {
	f := newFakeHost(t)
	f.settings = `not json`
	if _, err := loadSettings(); err == nil {
		t.Fatal("want an error")
	}
}

func TestConfigured(t *testing.T) {
	if err := (settings{Provider: "deepl"}).configured(); err == nil {
		t.Fatal("missing key must fail")
	}
	err := settings{Provider: "other", APIKey: testKey}.configured()
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("unknown provider error %v", err)
	}
	if err := (settings{Provider: "google", APIKey: "k"}).configured(); err != nil {
		t.Fatal(err)
	}
}

func TestRedactRemovesRawAndEscapedKeyBeforeTruncation(t *testing.T) {
	msg := "bad key " + testKey + " and " + url.QueryEscape(testKey) + " end"
	got := redact(msg, testKey)
	if strings.Contains(got, "SECRET") || strings.Count(got, "***") != 2 {
		t.Fatalf("redact = %q", got)
	}
	// Redacting first means a cut at any length cannot leave half a key.
	for n := 1; n <= len(got); n++ {
		if strings.Contains(got[:n], "SECRET") {
			t.Fatalf("key part survives at %d: %q", n, got[:n])
		}
	}
	if redact("plain", "") != "plain" {
		t.Fatal("empty secret must leave the text alone")
	}
}

func TestHTTPFetchBudgetUsedUpMakesNoCall(t *testing.T) {
	f := newFakeHost(t)
	budgetEnd = time.Now().Add(minCall / 2)
	_, err := httpFetch(fetchReq{Method: "POST", URL: "https://api.deepl.com/v2/translate"})
	if !errors.Is(err, errBudget) || err.Error() != "step time budget used up" {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("host called: %+v", f.calls)
	}
}

func TestHTTPFetchCutsTimeoutToBudget(t *testing.T) {
	f := newFakeHost(t)
	f.fetchRes = `{"status":200,"body":"ok"}`
	if _, err := httpFetch(fetchReq{Method: "GET", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	if got := f.calls[0].args.(fetchReq).TimeoutMS; got != 7000 {
		t.Fatalf("no budget: timeout_ms = %d", got)
	}
	budgetEnd = time.Now().Add(3 * time.Second)
	if _, err := httpFetch(fetchReq{Method: "GET", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	if got := f.calls[1].args.(fetchReq).TimeoutMS; got <= 1500 || got > 3000 {
		t.Fatalf("budgeted timeout_ms = %d", got)
	}
}

func TestHTTPFetchErrors(t *testing.T) {
	f := newFakeHost(t)
	f.fetchRes = `{"status":200,"body":"x","truncated":true}`
	if _, err := httpFetch(fetchReq{}); err == nil || err.Error() != "response too large" {
		t.Fatalf("truncated: %v", err)
	}
	for code, want := range map[string]string{
		sdk.ErrTimeout:  "provider timed out",
		sdk.ErrBlocked:  "host not allowed",
		sdk.ErrInternal: "provider request failed (internal)",
	} {
		f.fetchErr = &sdk.HostError{Code: code, Message: "boom " + testKey}
		_, err := httpFetch(fetchReq{})
		if err == nil || err.Error() != want || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("%s: err = %v, want %q", code, err, want)
		}
	}
	f.fetchErr = errors.New("plain " + testKey)
	if _, err := httpFetch(fetchReq{}); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("plain error leaks: %v", err)
	}
}

func TestProviderHost(t *testing.T) {
	all := []string{"api.deepl.com", "api-free.deepl.com", "translation.googleapis.com"}
	cases := []struct {
		s     settings
		hosts []string
		want  string
		bad   string
	}{
		{settings{Provider: "deepl", APIKey: "abc:fx"}, all, "api-free.deepl.com", ""},
		{settings{Provider: "deepl", APIKey: "abc"}, all, "api.deepl.com", ""},
		{settings{Provider: "google", APIKey: "abc"}, all, "translation.googleapis.com", ""},
		{settings{Provider: "deepl", APIKey: "abc:fx"}, []string{"api.deepl.com"}, "", "the host api-free.deepl.com was not confirmed when the run started"},
		{settings{Provider: "google", APIKey: "abc"}, nil, "", "the host translation.googleapis.com was not confirmed when the run started"},
		{settings{Provider: "x"}, all, "", `unknown provider "x"`},
	}
	for _, c := range cases {
		got, err := providerHost(c.s, c.hosts)
		if c.bad != "" {
			if err == nil || err.Error() != c.bad {
				t.Errorf("%+v: err = %v, want %q", c.s, err, c.bad)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%+v: got %q, %v; want %q", c.s, got, err, c.want)
		}
	}
}
