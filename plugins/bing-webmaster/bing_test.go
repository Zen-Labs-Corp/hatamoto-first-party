package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testKey = "k3y-SECRET-0123"

// hostCall is one recorded host call.
type hostCall struct {
	fn   string
	args map[string]any
}

func (c hostCall) sql() string { s, _ := c.args["sql"].(string); return s }

// fakeHost stands in for the host: Bing answers by method name, kv and settings
// are in memory, db.exec and db.query are recorded.
type fakeHost struct {
	settings settings
	bing     map[string]fetched // by Bing method name
	kv       map[string]json.RawMessage
	calls    []hostCall
	dbErr    error
}

func newHost(t *testing.T) *fakeHost {
	t.Helper()
	h := &fakeHost{settings: settings{APIKey: testKey}, bing: map[string]fetched{}, kv: map[string]json.RawMessage{}}
	callHost = h.call
	config = func(string) (string, bool) { return "", false }
	budgetEnd = time.Time{}
	t.Cleanup(func() { callHost = nil; budgetEnd = time.Time{} })
	return h
}

func ok(body string) fetched { return fetched{Status: 200, Body: body} }

func (h *fakeHost) call(fn string, a, out any) error {
	args, _ := a.(map[string]any)
	h.calls = append(h.calls, hostCall{fn, args})
	var res any
	switch fn {
	case "settings.get":
		res = h.settings
	case "http.fetch":
		u, err := url.Parse(args["url"].(string))
		if err != nil {
			return err
		}
		name := u.Path[strings.LastIndex(u.Path, "/")+1:]
		r, found := h.bing[name]
		if !found {
			r = fetched{Status: 404, Body: `{"Message":"no such method"}`}
		}
		res = r
	case "kv.get":
		v, found := h.kv[a.(map[string]string)["key"]]
		res = map[string]any{"found": found, "value": v}
	case "kv.set":
		b, _ := json.Marshal(args["value"])
		h.kv[args["key"].(string)] = b
		res = struct{}{}
	case "kv.delete":
		delete(h.kv, a.(map[string]string)["key"])
		res = struct{}{}
	case "db.exec":
		if h.dbErr != nil {
			return h.dbErr
		}
		res = struct{}{}
	case "db.query":
		res = map[string]any{"rows": [][]any{}}
	default:
		res = struct{}{}
	}
	if out == nil {
		return nil
	}
	b, _ := json.Marshal(res)
	return json.Unmarshal(b, out)
}

// fetches returns the http.fetch calls so far.
func (h *fakeHost) fetches() []hostCall {
	var out []hostCall
	for _, c := range h.calls {
		if c.fn == "http.fetch" {
			out = append(out, c)
		}
	}
	return out
}

func TestParseBingDate(t *testing.T) {
	for in, want := range map[string]string{
		"/Date(1700000000000)/":      "2023-11-14",
		"/Date(1700000000000-0700)/": "2023-11-14",
		"/Date(1700000000000+0530)/": "2023-11-15", // 22:13 UTC + 5:30 = next day
		"/Date(1699920000000-0700)/": "2023-11-13", // 00:00 UTC - 7h = previous day
		"/Date(0)/":                  "1970-01-01",
	} {
		got, err := parseBingDate(in)
		if err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "2023-11-14", "/Date(x)/", "/Date(1+07)/"} {
		if _, err := parseBingDate(bad); err == nil {
			t.Errorf("%q must not parse", bad)
		}
	}
	if parseBingTime("/Date(-62135596800000)/") != nil {
		t.Error("Bing's year-1 value means never")
	}
}

func TestBadKeyIsPlain(t *testing.T) {
	for name, res := range map[string]fetched{
		"401":          {Status: 401, Body: `{"Message":"nope"}`},
		"403 no body":  {Status: 403},
		"200 fault":    ok(`{"Message":"InvalidApiKey","ErrorCode":3}`),
		"numeric code": {Status: 400, Body: `{"Message":"something else","ErrorCode":3}`},
		"string code":  ok(`{"ErrorCode":"InvalidApiKey"}`),
	} {
		h := newHost(t)
		h.bing["GetUserSites"] = res
		_, err := getUserSites(h.settings)
		if err == nil || err.Error() != "Bing rejected the API key." {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNotAuthorizedBlamesTheSiteNotTheKey(t *testing.T) {
	want := "Bing says this key has no access to " + testSite + ". Check that Site URL matches the site exactly as verified in Bing (including the trailing slash)."
	for name, res := range map[string]fetched{
		"400 text":     {Status: 400, Body: `{"Message":"NotAuthorized: key not valid"}`},
		"403 text":     {Status: 403, Body: `{"Message":"NotAuthorized"}`},
		"numeric code": {Status: 400, Body: `{"Message":"x","ErrorCode":14}`},
	} {
		h := newHost(t)
		h.bing["GetRankAndTrafficStats"] = res
		_, err := trafficStats(h.settings, testSite)
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil && !stopsRun(err) {
			t.Errorf("%s: must stop a run", name)
		}
	}
}

func TestOtherErrorsKeepStatusAndMessageOnly(t *testing.T) {
	h := newHost(t)
	long := strings.Repeat("x", 300)
	h.bing["GetUserSites"] = fetched{Status: 500, Body: `{"Message":"boom ` + testKey + " " + long + `"}`}
	_, err := getUserSites(h.settings)
	if err == nil || !strings.HasPrefix(err.Error(), "Bing returned 500: boom [key] ") {
		t.Fatalf("%v", err)
	}
	if len([]rune(err.Error())) > 230 {
		t.Fatalf("message not cut: %d", len(err.Error()))
	}
}

func TestErrorsNeverCarryKeyOrURL(t *testing.T) {
	h := newHost(t)
	callHost = func(fn string, a, out any) error {
		if fn == "http.fetch" {
			return errors.New("Get " + a.(map[string]any)["url"].(string) + ": dial failed")
		}
		return h.call(fn, a, out)
	}
	_, err := getUserSites(h.settings)
	if err == nil || strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "apikey=") {
		t.Fatalf("%v", err)
	}
	for _, body := range []string{`{"Message":"` + testKey + `"}`, `not json`} {
		h2 := newHost(t)
		h2.bing["GetUserSites"] = fetched{Status: 500, Body: body}
		if _, err := getUserSites(h2.settings); err == nil || strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "apikey") {
			t.Fatalf("%q: %v", body, err)
		}
	}
}

func TestTruncatedIsAnError(t *testing.T) {
	h := newHost(t)
	h.bing["GetQueryStats"] = fetched{Status: 200, Body: `{"d":[{"Que`, Truncated: true}
	_, err := queryStats(h.settings, "https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "too large") || !strings.Contains(err.Error(), "GetQueryStats") {
		t.Fatalf("%v", err)
	}
}

func TestRequestShape(t *testing.T) {
	h := newHost(t)
	h.bing["GetRankAndTrafficStats"] = ok(`{"d":[{"Date":"/Date(1700000000000)/","Clicks":5,"Impressions":50},{"Date":"junk","Clicks":1}]}`)
	h.bing["SubmitFeed"] = ok(`{"d":null}`)
	rows, err := trafficStats(h.settings, "https://example.com/a b")
	if err != nil || len(rows) != 1 || rows[0] != (trafficRow{Day: "2023-11-14", Clicks: 5, Impressions: 50}) {
		t.Fatalf("%v %+v", err, rows)
	}
	u, _ := url.Parse(h.fetches()[0].args["url"].(string))
	if u.Host != "ssl.bing.com" || u.Path != "/webmaster/api.svc/json/GetRankAndTrafficStats" ||
		u.Query().Get("apikey") != testKey || u.Query().Get("siteUrl") != "https://example.com/a b" {
		t.Fatalf("url %v", u)
	}
	if err := submitFeed(h.settings, "https://example.com/", "https://example.com/sitemap.xml"); err != nil {
		t.Fatal(err)
	}
	post := h.fetches()[1].args
	if post["method"] != "POST" || post["headers"].(map[string]string)["Content-Type"] != "application/json; charset=utf-8" ||
		post["body"] != `{"feedUrl":"https://example.com/sitemap.xml","siteUrl":"https://example.com/"}` {
		t.Fatalf("post %v", post)
	}
}

func TestBaseOverrideFromConfig(t *testing.T) {
	h := newHost(t)
	config = func(k string) (string, bool) { return "http://fake.test/api/", k == "bing_base" }
	h.bing["GetUserSites"] = ok(`{"d":[]}`)
	if _, err := getUserSites(h.settings); err != nil {
		t.Fatal(err)
	}
	if u := h.fetches()[0].args["url"].(string); !strings.HasPrefix(u, "http://fake.test/api/GetUserSites?") {
		t.Fatal(u)
	}
}

func TestTimeoutIsCutToTheBudget(t *testing.T) {
	h := newHost(t)
	h.bing["GetUserSites"] = ok(`{"d":[]}`)
	if _, err := getUserSites(h.settings); err != nil {
		t.Fatal(err)
	}
	if got := h.fetches()[0].args["timeout_ms"]; got != 8000 {
		t.Fatalf("timeout %v", got)
	}
	budgetEnd = time.Now().Add(3 * time.Second)
	if _, err := getUserSites(h.settings); err != nil {
		t.Fatal(err)
	}
	if got := h.fetches()[1].args["timeout_ms"].(int); got > 3000 || got < 2000 {
		t.Fatalf("timeout %v", got)
	}
	budgetEnd = time.Now().Add(500 * time.Millisecond)
	n := len(h.fetches())
	if _, err := getUserSites(h.settings); !errors.Is(err, errBudget) || len(h.fetches()) != n {
		t.Fatalf("%v, fetches %d", err, len(h.fetches()))
	}
}

func TestTolerantDecoding(t *testing.T) {
	h := newHost(t)
	h.bing["GetCrawlIssues"] = ok(`{"d":[{"Url":"https://example.com/a","HttpCode":404,"Issues":4},{"HttpCode":1}]}`)
	h.bing["GetFeeds"] = ok(`{"d":[{"Url":"https://example.com/sitemap.xml","Status":"Success","Submitted":"/Date(1700000000000)/","LastCrawled":"/Date(-62135596800000)/","UrlCount":7}]}`)
	h.bing["GetPageStats"] = ok(`{"d":null}`)
	c, err := crawlIssues(h.settings, "s")
	if err != nil || len(c) != 1 || c[0].InLinks != 0 || c[0].Issues != 4 {
		t.Fatalf("%v %+v", err, c)
	}
	f, err := feeds(h.settings, "s")
	if err != nil || len(f) != 1 || f[0].SubmittedAt == nil || f[0].LastCrawledAt != nil || f[0].URLCount != 7 {
		t.Fatalf("%v %+v", err, f)
	}
	if p, err := pageStats(h.settings, "s"); err != nil || len(p) != 0 {
		t.Fatalf("%v %v", err, p)
	}
	h.bing["GetFeeds"] = ok(`{"d":"oops"}`)
	if _, err := feeds(h.settings, "s"); err == nil || !strings.Contains(err.Error(), "GetFeeds") {
		t.Fatalf("%v", err)
	}
}

func TestResolveSite(t *testing.T) {
	sites := func(urls ...string) fetched {
		var parts []string
		for _, u := range urls {
			v := "true"
			if strings.HasPrefix(u, "!") {
				v, u = "false", u[1:]
			}
			parts = append(parts, `{"Url":"`+u+`","IsVerified":`+v+`}`)
		}
		return ok(`{"d":[` + strings.Join(parts, ",") + `]}`)
	}
	t.Run("setting wins and is kept as written", func(t *testing.T) {
		h := newHost(t)
		got, src, err := resolveSite(settings{APIKey: testKey, SiteURL: "https://example.com"})
		if err != nil || got != "https://example.com" || src != "setting" || len(h.calls) != 0 {
			t.Fatalf("%q %q %v %d", got, src, err, len(h.calls))
		}
	})
	t.Run("invalid setting", func(t *testing.T) {
		newHost(t)
		if _, _, err := resolveSite(settings{APIKey: testKey, SiteURL: "example.com"}); err != errSiteInvalid {
			t.Fatal(err)
		}
	})
	t.Run("one verified site, then cached", func(t *testing.T) {
		h := newHost(t)
		h.bing["GetUserSites"] = sites("https://example.com/", "!https://other.example.com/")
		got, src, err := resolveSite(h.settings)
		if err != nil || got != "https://example.com/" || src != "bing" {
			t.Fatalf("%q %q %v", got, src, err)
		}
		h.bing = map[string]fetched{}
		if got, _, err = resolveSite(h.settings); err != nil || got != "https://example.com/" {
			t.Fatalf("cache: %q %v", got, err)
		}
	})
	t.Run("cached site is not reused for another key", func(t *testing.T) {
		h := newHost(t)
		h.bing["GetUserSites"] = sites("https://example.com/")
		if _, _, err := resolveSite(h.settings); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(h.kv["site"]), testKey) {
			t.Fatal("the key must not be stored")
		}
		h.bing["GetUserSites"] = sites("https://other.example.org/")
		h.settings.APIKey = "another-key"
		got, _, err := resolveSite(h.settings)
		if err != nil || got != "https://other.example.org/" {
			t.Fatalf("%q %v", got, err)
		}
	})
	for name, list := range map[string]fetched{
		"none verified": sites("!https://example.com/"),
		"two verified":  sites("https://example.com/", "https://other.example.com/"),
		"empty":         ok(`{"d":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			h.bing["GetUserSites"] = list
			if _, _, err := resolveSite(h.settings); err != errSiteChoice || !strings.Contains(err.Error(), "Site URL") {
				t.Fatal(err)
			}
			if _, ok := h.kv["site"]; ok {
				t.Fatal("nothing to cache")
			}
		})
	}
	t.Run("no key", func(t *testing.T) {
		newHost(t)
		if _, _, err := resolveSite(settings{}); err != errNoKey {
			t.Fatal(err)
		}
	})
}

func TestIssueLabels(t *testing.T) {
	got := strings.Join(issueLabels(4|16|256), ", ")
	if got != "4xx error, Blocked by robots.txt, Timeout" {
		t.Fatal(got)
	}
	if l := issueLabels(0); l == nil || len(l) != 0 {
		t.Fatalf("%#v", l)
	}
}

func TestSitemapURLIsBuiltFromTheOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/":         "https://example.com/sitemap.xml",
		"https://example.com":          "https://example.com/sitemap.xml",
		"https://example.com/blog/":    "https://example.com/sitemap.xml",
		"https://example.com/?x=1#f":   "https://example.com/sitemap.xml",
		"http://example.com:8080/a/b?": "http://example.com:8080/sitemap.xml",
	} {
		if got, err := sitemapURL(in); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "example.com", "ftp://example.com/", "https:///x", "https://u:p@example.com/",
		"https://example.com/a b", "https://example.com/\n", "https://example.com/" + strings.Repeat("a", maxSiteURL)} {
		if _, err := sitemapURL(bad); err != errSiteInvalid {
			t.Errorf("%q must be invalid: %v", bad, err)
		}
		newHost(t)
		if _, _, err := resolveSite(settings{APIKey: testKey, SiteURL: bad}); bad != "" && err != errSiteInvalid {
			t.Errorf("%q must be invalid as a setting: %v", bad, err)
		}
	}
}
