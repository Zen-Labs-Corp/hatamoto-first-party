package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func request(t *testing.T, method, pattern string) (int, map[string]any, string) {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: method, Pattern: pattern})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(res.Body), &body); err != nil {
		t.Fatalf("%q: %v", res.Body, err)
	}
	return res.Status, body, res.Body
}

// withOverviewRow makes db.query answer with one overview document.
func withOverviewRow(h *fakeHost, cell string) {
	callHost = func(fn string, a, out any) error {
		if fn == "db.query" {
			h.calls = append(h.calls, hostCall{fn, a.(map[string]any)})
			return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
		}
		return h.call(fn, a, out)
	}
}

func TestTestRouteRejectedKeyIsPlainAndStoresNothing(t *testing.T) {
	h := newHost(t)
	h.bing["GetUserSites"] = fetched{Status: 401, Body: `{"Message":"InvalidApiKey"}`}
	status, body, raw := request(t, "POST", "/test")
	if status != http.StatusBadGateway || body["error"] != "Bing rejected the API key." {
		t.Fatalf("%d %s", status, raw)
	}
	if strings.Contains(raw, testKey) || strings.Contains(raw, "apikey") {
		t.Fatalf("response leaks the key: %s", raw)
	}
	for _, c := range h.calls {
		if c.fn == "db.exec" || c.fn == "kv.set" {
			t.Fatalf("/test stored something: %s", c.fn)
		}
	}
}

func TestTestRouteReportsVerifiedSites(t *testing.T) {
	for name, tc := range map[string]struct {
		site string
		want bool
	}{
		"empty, one verified": {"", true},
		"matching":            {"https://example.com/", true},
		"not verified":        {"https://other.example.com/", false},
	} {
		h := newHost(t)
		h.settings.SiteURL = tc.site
		h.bing["GetUserSites"] = ok(`{"d":[{"Url":"https://example.com/","IsVerified":true},{"Url":"https://x.example.com/","IsVerified":false}]}`)
		status, body, raw := request(t, "POST", "/test")
		if status != 200 || body["ok"] != true || body["sites_verified"] != 1.0 || body["site_url_ok"] != tc.want {
			t.Errorf("%s: %d %s", name, status, raw)
		}
	}
	h := newHost(t)
	h.settings.APIKey = ""
	if status, body, _ := request(t, "POST", "/test"); status != http.StatusConflict || body["error"] != "Add your Bing API key in Settings." {
		t.Fatalf("%d %v", status, body)
	}
}

func TestOverviewNeverReturnsTheKey(t *testing.T) {
	h := newHost(t)
	h.bing["GetUserSites"] = ok(`{"d":[{"Url":"https://example.com/","IsVerified":true}]}`)
	withOverviewRow(h, `{"traffic":[{"day":"2026-11-03","clicks":52,"impressions":1880},{"day":"2026-11-02","clicks":8,"impressions":20}],
		"crawl_issues":[{"url":"https://example.com/old","http_code":404,"issues":20,"in_links":3}],
		"last_import":{"at":"2026-11-04T14:00:00+00:00","ok":true,"error":""}}`)
	status, body, raw := request(t, "GET", "/overview")
	if status != 200 || body["api_key_set"] != true || body["site_url"] != "https://example.com/" || body["site_source"] != "bing" {
		t.Fatalf("%d %s", status, raw)
	}
	if strings.Contains(raw, testKey) || strings.Contains(raw, "apikey") || strings.Contains(raw, "api_key\":\"") {
		t.Fatalf("overview leaks the key: %s", raw)
	}
	if tot := body["totals"].(map[string]any); tot["clicks"] != 60.0 || tot["impressions"] != 1900.0 {
		t.Fatalf("totals %v", tot)
	}
	issue := body["crawl_issues"].([]any)[0].(map[string]any)
	if l := issue["labels"].([]any); len(l) != 2 || l[0] != "4xx error" || l[1] != "Blocked by robots.txt" {
		t.Fatalf("labels %v", issue["labels"])
	}
	for _, k := range []string{"top_queries", "top_pages", "feeds"} {
		if l, ok := body[k].([]any); !ok || len(l) != 0 {
			t.Errorf("%s = %#v, want []", k, body[k])
		}
	}
	if body["last_submission"] != nil {
		t.Fatalf("last_submission %v", body["last_submission"])
	}
}

func TestOverviewWithoutKey(t *testing.T) {
	h := newHost(t)
	h.settings = settings{SiteURL: "https://example.com/"}
	status, body, raw := request(t, "GET", "/overview")
	if status != 200 || len(body) != 1 || body["api_key_set"] != false {
		t.Fatalf("%d %s", status, raw)
	}
	for _, c := range h.calls {
		if c.fn != "settings.get" {
			t.Fatalf("host call %s without a key", c.fn)
		}
	}
}

func TestOverviewShowsASiteProblem(t *testing.T) {
	h := newHost(t)
	h.bing["GetUserSites"] = fetched{Status: 401}
	withOverviewRow(h, `{}`)
	_, body, _ := request(t, "GET", "/overview")
	if body["api_key_set"] != true || body["site_source"] != "none" || body["site_error"] != "Bing rejected the API key." {
		t.Fatalf("%v", body)
	}
}

func TestSubmitSitemapRecordsTheAttempt(t *testing.T) {
	h := newHost(t)
	h.settings.SiteURL = "https://example.com/"
	h.bing["SubmitFeed"] = ok(`{"d":null}`)
	status, body, _ := request(t, "POST", "/submit-sitemap")
	if status != 200 || body["feed_url"] != "https://example.com/sitemap.xml" {
		t.Fatalf("%d %v", status, body)
	}
	post := h.fetches()[0].args
	if post["method"] != "POST" || post["body"] != `{"feedUrl":"https://example.com/sitemap.xml","siteUrl":"https://example.com/"}` {
		t.Fatalf("%v", post)
	}
	runs := writes(h, "INSERT INTO sitemap_submissions")
	if len(runs) != 1 || runs[0].args["args"].([]any)[2] != "" {
		t.Fatalf("%v", runs)
	}

	h.calls = nil
	h.bing["SubmitFeed"] = fetched{Status: 400, Body: `{"Message":"Invalid feed"}`}
	status, body, _ = request(t, "POST", "/submit-sitemap")
	if status != http.StatusBadGateway || body["error"] != "Bing returned 400: Invalid feed" {
		t.Fatalf("%d %v", status, body)
	}
	if runs := writes(h, "INSERT INTO sitemap_submissions"); len(runs) != 1 || runs[0].args["args"].([]any)[2] != "Bing returned 400: Invalid feed" {
		t.Fatalf("failure not recorded: %v", runs)
	}
}

func TestSubmitSitemapNeedsAKeyAndASite(t *testing.T) {
	h := newHost(t)
	h.settings.APIKey = ""
	if status, _, _ := request(t, "POST", "/submit-sitemap"); status != http.StatusConflict {
		t.Fatalf("no key: %d", status)
	}
	h = newHost(t)
	h.settings.SiteURL = "example.com"
	if status, _, _ := request(t, "POST", "/submit-sitemap"); status != http.StatusUnprocessableEntity {
		t.Fatalf("bad site: %d", status)
	}
	if len(h.fetches()) != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestImportNowShape(t *testing.T) {
	h := newHost(t)
	goodBing(h)
	status, body, raw := request(t, "POST", "/import-now")
	if status != 200 || body["done"] != true || body["rows"] != 5.0 {
		t.Fatalf("%d %s", status, raw)
	}
	if r, ok := body["remaining"].([]any); !ok || len(r) != 0 {
		t.Fatalf("remaining %s", raw)
	}
	if strings.Contains(raw, testKey) {
		t.Fatal("leak")
	}
	h = newHost(t)
	goodBing(h)
	h.bing["GetPageStats"] = fetched{Status: 401}
	if status, body, _ := request(t, "POST", "/import-now"); status != http.StatusBadGateway || body["error"] != "Bing rejected the API key." {
		t.Fatalf("%d %v", status, body)
	}
	h = newHost(t)
	h.settings.APIKey = ""
	if status, _, _ := request(t, "POST", "/import-now"); status != http.StatusConflict {
		t.Fatalf("no key: %d", status)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	newHost(t)
	if status, _, _ := request(t, "GET", "/nope"); status != http.StatusNotFound {
		t.Fatal(status)
	}
	if status, _, _ := request(t, "GET", "/import-now"); status != http.StatusNotFound {
		t.Fatal(status)
	}
}

func TestSubmitSitemapSharesOneDeadline(t *testing.T) {
	h := newHost(t)
	h.bing["GetUserSites"] = ok(`{"d":[{"Url":"https://example.com/","IsVerified":true}]}`)
	h.bing["SubmitFeed"] = ok(`{"d":null}`)
	slow := h.call
	callHost = func(fn string, a, out any) error {
		if fn == "http.fetch" {
			time.Sleep(700 * time.Millisecond)
		}
		return slow(fn, a, out)
	}
	if status, body, _ := request(t, "POST", "/submit-sitemap"); status != 200 {
		t.Fatalf("%d %v", status, body)
	}
	f := h.fetches()
	if len(f) != 2 {
		t.Fatalf("%d fetches", len(f))
	}
	first, second := f[0].args["timeout_ms"].(int), f[1].args["timeout_ms"].(int)
	if second >= first {
		t.Fatalf("timeouts %d then %d: the second must be cut by the time the first took", first, second)
	}
}
