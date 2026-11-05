package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestNewTranslator(t *testing.T) {
	if _, ok := newTranslator(settings{Provider: "google", APIKey: "k"}, "").(google); !ok {
		t.Fatal("google provider must give the google client")
	}
	d, ok := newTranslator(settings{Provider: "deepl", APIKey: "k", Formality: "less"}, "api.deepl.com").(deepl)
	if !ok || d.host != "api.deepl.com" || d.formality != "less" || d.key != "k" {
		t.Fatalf("deepl client %+v", d)
	}
}

func TestGoogleRequestShape(t *testing.T) {
	reqs := fetchWorld(t, 200, `{"data":{"translations":[{"translatedText":"<p>Hallo &amp; Welt</p>"},{"translatedText":"Zwei"}]}}`)
	got, err := google{testKey}.translate([]string{"<p>Hello</p>", "Two"}, "de", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "<p>Hallo &amp; Welt</p>" || got[1] != "Zwei" {
		t.Fatalf("got %q", got)
	}
	if len(reqs()) != 1 {
		t.Fatalf("%d fetches", len(reqs()))
	}
	r := reqs()[0]
	if r.Method != "POST" || r.URL != "https://translation.googleapis.com/language/translate/v2" {
		t.Fatalf("request %s %s", r.Method, r.URL)
	}
	if r.Headers["X-Goog-Api-Key"] != testKey || strings.Contains(r.URL, "key") || strings.Contains(r.URL, "?") {
		t.Fatalf("key handling: url %s headers %v", r.URL, r.Headers)
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(r.Body), &b); err != nil {
		t.Fatal(err)
	}
	if b["target"] != "de" || b["source"] != "en" || b["format"] != "html" {
		t.Fatalf("body %v", b)
	}
	if q, _ := b["q"].([]any); len(q) != 2 || q[1] != "Two" {
		t.Fatalf("q %v", b["q"])
	}
}

func TestGoogleOmitsEmptySource(t *testing.T) {
	reqs := fetchWorld(t, 200, `{"data":{"translations":[{"translatedText":"x"}]}}`)
	if _, err := (google{"k"}).translate([]string{"a"}, "de", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reqs()[0].Body, "source") {
		t.Fatalf("body %s", reqs()[0].Body)
	}
}

func TestGoogleCountMismatch(t *testing.T) {
	fetchWorld(t, 200, `{"data":{"translations":[]}}`)
	if _, err := (google{"k"}).translate([]string{"a"}, "de", ""); err == nil {
		t.Fatal("want a count error")
	}
	fetchWorld(t, 200, `<html>`)
	if _, err := (google{"k"}).translate([]string{"a"}, "de", ""); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestGoogleStatusMapping(t *testing.T) {
	key := "SECRET-KEY"
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{
		{400, `{"error":{"message":"API key not valid SECRET-KEY","errors":[{"reason":"keyInvalid"}]}}`, "Google rejected the API key"},
		{400, `{"error":{"message":"Invalid  Value SECRET-KEY","errors":[{"reason":"invalid"}]}}`, "Google refused the request: Invalid Value ***"},
		{403, `{"error":{"message":"Billing disabled"}}`, "Google Translation is not enabled for this key or the quota/billing is exhausted: Billing disabled"},
		{429, ``, "Google is rate limiting requests, try again later"},
		{500, ``, "Google Translation is temporarily unavailable"},
		{503, ``, "Google Translation is temporarily unavailable"},
		{404, ``, "Google error 404"},
	} {
		reqs := fetchWorld(t, c.status, c.body)
		_, err := (google{key}).translate([]string{"a"}, "de", "")
		if err == nil || err.Error() != c.want {
			t.Fatalf("status %d: got %v, want %q", c.status, err, c.want)
		}
		if strings.Contains(err.Error(), key) || strings.Contains(reqs()[0].URL, key) {
			t.Fatalf("status %d: key leaked", c.status)
		}
	}
}

func TestGoogleFetchErrorRedacted(t *testing.T) {
	f := newFakeHost(t)
	f.fetchErr = &sdk.HostError{Code: sdk.ErrBlocked, Message: "boom " + testKey}
	_, err := (google{testKey}).translate([]string{"a"}, "de", "")
	if err == nil || err.Error() != "host not allowed" {
		t.Fatalf("got %v", err)
	}
}
