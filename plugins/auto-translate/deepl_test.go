package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// fetchWorld answers every http.fetch with a fixed reply and returns a func
// listing the recorded requests.
func fetchWorld(t *testing.T, status int, body string) func() []fetchReq {
	t.Helper()
	f := newFakeHost(t)
	b, _ := json.Marshal(map[string]any{"status": status, "body": body})
	f.fetchRes = string(b)
	return func() (r []fetchReq) {
		for _, c := range f.calls {
			if c.fn == "http.fetch" {
				r = append(r, c.args.(fetchReq))
			}
		}
		return r
	}
}

func TestDeepLRequestShape(t *testing.T) {
	reqs := fetchWorld(t, 200, `{"translations":[{"text":"<p>Hallo</p>"},{"text":"Welt"}]}`)
	d := deepl{key: testKey, host: "api-free.deepl.com", formality: "more"}
	got, err := d.translate([]string{"<p>Hello</p>", "World"}, "DE", "EN")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "<p>Hallo</p>" || got[1] != "Welt" {
		t.Fatalf("got %q", got)
	}
	if len(reqs()) != 1 {
		t.Fatalf("%d fetches", len(reqs()))
	}
	r := reqs()[0]
	if r.Method != "POST" || r.URL != "https://api-free.deepl.com/v2/translate" {
		t.Fatalf("request %s %s", r.Method, r.URL)
	}
	if r.Headers["Authorization"] != "DeepL-Auth-Key "+testKey || r.Headers["Content-Type"] != "application/json" {
		t.Fatalf("headers %v", r.Headers)
	}
	if strings.Contains(r.URL, "SECRET") || strings.Contains(r.URL, "?") {
		t.Fatal("key or query in the URL")
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(r.Body), &b); err != nil {
		t.Fatal(err)
	}
	if b["target_lang"] != "DE" || b["source_lang"] != "EN" || b["tag_handling"] != "html" || b["formality"] != "more" {
		t.Fatalf("body %v", b)
	}
	if txt, _ := b["text"].([]any); len(txt) != 2 || txt[0] != "<p>Hello</p>" {
		t.Fatalf("text %v", b["text"])
	}
}

func TestDeepLOmitsOptionalFields(t *testing.T) {
	for _, c := range []struct{ formality, target, source string }{
		{"default", "DE", ""},
		{"", "DE", ""},
		{"more", "EN-GB", ""},
	} {
		reqs := fetchWorld(t, 200, `{"translations":[{"text":"x"}]}`)
		if _, err := (deepl{key: "k", host: "api.deepl.com", formality: c.formality}).translate([]string{"a"}, c.target, c.source); err != nil {
			t.Fatal(err)
		}
		body := reqs()[0].Body
		if strings.Contains(body, "formality") || strings.Contains(body, "source_lang") {
			t.Fatalf("%+v: body %s", c, body)
		}
	}
}

func TestDeepLCountMismatch(t *testing.T) {
	fetchWorld(t, 200, `{"translations":[{"text":"x"}]}`)
	if _, err := (deepl{key: "k", host: "api.deepl.com"}).translate([]string{"a", "b"}, "DE", ""); err == nil {
		t.Fatal("want a count error")
	}
	fetchWorld(t, 200, `not json`)
	if _, err := (deepl{key: "k", host: "api.deepl.com"}).translate([]string{"a"}, "DE", ""); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestDeepLStatusMapping(t *testing.T) {
	key := "SECRET-KEY"
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{
		{403, `{"message":"Forbidden"}`, "DeepL rejected the key (check the key and whether it is a Free or Pro key)"},
		{456, ``, "DeepL quota exceeded"},
		{429, ``, "DeepL is rate limiting requests, try again later"},
		{529, ``, "DeepL is rate limiting requests, try again later"},
		{413, ``, "request too large"},
		{400, `{"message":"bad  tag\nSECRET-KEY"}`, "DeepL refused the request: bad tag ***"},
		{503, ``, "DeepL is temporarily unavailable"},
		{418, ``, "DeepL error 418"},
	} {
		reqs := fetchWorld(t, c.status, c.body)
		_, err := (deepl{key: key, host: "api.deepl.com"}).translate([]string{"a"}, "DE", "")
		if err == nil || err.Error() != c.want {
			t.Fatalf("status %d: got %v, want %q", c.status, err, c.want)
		}
		if strings.Contains(err.Error(), key) || strings.Contains(reqs()[0].URL, key) {
			t.Fatalf("status %d: key leaked", c.status)
		}
	}
}

func TestDeepLFetchErrorRedacted(t *testing.T) {
	f := newFakeHost(t)
	f.fetchErr = &sdk.HostError{Code: sdk.ErrTimeout, Message: "boom " + testKey}
	_, err := (deepl{key: "k", host: "api.deepl.com"}).translate([]string{"a"}, "DE", "")
	if err == nil || err.Error() != "provider timed out" {
		t.Fatalf("got %v", err)
	}
}
