package main

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const simpleRSS = `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title><link>https://example.com/</link>
<item><title>One</title><link>https://example.com/1</link><guid>1</guid></item></channel></rss>`

var testFeed = feedCfg{Key: "k", Name: "Alpha", URL: "https://example.com/feed.xml", MaxItems: 50}

func deadlineIn(f *fakeHost, d time.Duration) time.Time { return f.clock.Add(d) }

func serve(f *fakeHost, res fetchRes) {
	f.fetch = func(string, map[string]string) (fetchRes, error) { return res, nil }
}

func TestFetchSendsStoredValidators(t *testing.T) {
	f := newFake(t)
	serve(f, fetchRes{Status: 200, Body: simpleRSS, Headers: map[string]string{"etag": `"abc"`, "last-modified": "Tue, 03 Nov 2026 09:30:00 GMT"}})
	out, err := fetchFeed(testFeed, feedRow{ETag: `"old"`, LastModified: "Mon, 02 Nov 2026 00:00:00 GMT"}, deadlineIn(f, 9*time.Second))
	if err != nil || out.NotModified || len(out.Feed.Items) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	h := f.requests[0].Headers
	if h["If-None-Match"] != `"old"` || h["If-Modified-Since"] != "Mon, 02 Nov 2026 00:00:00 GMT" || h["Accept"] == "" {
		t.Fatalf("headers %v", h)
	}
	if out.ETag != `"abc"` || out.LastModified != "Tue, 03 Nov 2026 09:30:00 GMT" {
		t.Fatalf("validators %q %q", out.ETag, out.LastModified)
	}
	if f.requests[0].TimeoutMS != 7500 {
		t.Fatalf("timeout %d", f.requests[0].TimeoutMS)
	}
}

func TestFetchSendsNoValidatorsWhenNoneStored(t *testing.T) {
	f := newFake(t)
	serve(f, fetchRes{Status: 200, Body: simpleRSS})
	if _, err := fetchFeed(testFeed, feedRow{}, deadlineIn(f, 9*time.Second)); err != nil {
		t.Fatal(err)
	}
	h := f.requests[0].Headers
	if _, ok := h["If-None-Match"]; ok {
		t.Fatalf("headers %v", h)
	}
	if _, ok := h["If-Modified-Since"]; ok {
		t.Fatalf("headers %v", h)
	}
}

func TestFetch304IsNotModified(t *testing.T) {
	f := newFake(t)
	serve(f, fetchRes{Status: 304})
	out, err := fetchFeed(testFeed, feedRow{ETag: `"x"`}, deadlineIn(f, 9*time.Second))
	if err != nil || !out.NotModified {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestFetchRequestTimeoutFollowsTimeLeft(t *testing.T) {
	f := newFake(t)
	serve(f, fetchRes{Status: 200, Body: simpleRSS})
	if _, err := fetchFeed(testFeed, feedRow{}, deadlineIn(f, 4000*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if f.requests[0].TimeoutMS != 2500 {
		t.Fatalf("timeout %d", f.requests[0].TimeoutMS)
	}
	if _, err := fetchFeed(testFeed, feedRow{}, deadlineIn(f, 2400*time.Millisecond)); err != errNoTime {
		t.Fatalf("too little time left must not start a request: %v", err)
	}
	if len(f.requests) != 1 {
		t.Fatal("a request was made without time for it")
	}
}

func TestFetchFixedPhrasesForFailures(t *testing.T) {
	const secret = "https://example.com/secret-path?token=SECRET-BODY"
	cases := []struct {
		name string
		res  fetchRes
		err  error
		want string
	}{
		{"http 500", fetchRes{Status: 500, Body: "SECRET-BODY"}, nil, "HTTP 500"},
		{"http 404", fetchRes{Status: 404}, nil, "HTTP 404"},
		{"bad xml", fetchRes{Status: 200, Body: "<rss><channel>SECRET-BODY"}, nil, "invalid feed"},
		{"html page", fetchRes{Status: 200, Body: "<html>SECRET-BODY</html>"}, nil, "invalid feed"},
		{"truncated", fetchRes{Status: 200, Body: simpleRSS, Truncated: true}, nil, "feed too large"},
		{"oversized", fetchRes{Status: 200, Body: strings.Repeat("x", maxBody+1)}, nil, "feed too large"},
		{"bad base64", fetchRes{Status: 200, BodyB64: "***"}, nil, "invalid feed"},
		{"charset", fetchRes{Status: 200, Body: `<?xml version="1.0" encoding="Shift_JIS"?><rss/>`}, nil, "unsupported charset"},
		{"timeout", fetchRes{}, &sdk.HostError{Code: sdk.ErrTimeout, Message: secret}, "timeout"},
		{"blocked", fetchRes{}, &sdk.HostError{Code: sdk.ErrBlocked, Message: secret}, "blocked"},
		{"other", fetchRes{}, errors.New(secret), "fetch failed"},
	}
	for _, c := range cases {
		f := newFake(t)
		f.fetch = func(string, map[string]string) (fetchRes, error) { return c.res, c.err }
		_, err := fetchFeed(testFeed, feedRow{}, deadlineIn(f, 9*time.Second))
		if err == nil || err.Error() != c.want {
			t.Fatalf("%s: err %v, want %q", c.name, err, c.want)
		}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "example.com") {
			t.Fatalf("%s: status leaks: %q", c.name, err)
		}
	}
}

func TestFetchDecodesBase64Latin1Body(t *testing.T) {
	f := newFake(t)
	raw := []byte(`<?xml version="1.0" encoding="ISO-8859-1"?><rss><channel><item><title>Caf` + "\xe9" + `</title><link>https://example.com/1</link></item></channel></rss>`)
	serve(f, fetchRes{Status: 200, BodyB64: base64.StdEncoding.EncodeToString(raw)})
	out, err := fetchFeed(testFeed, feedRow{}, deadlineIn(f, 9*time.Second))
	if err != nil || len(out.Feed.Items) != 1 || out.Feed.Items[0].Title != "Café" {
		t.Fatalf("%+v %v", out, err)
	}
}
