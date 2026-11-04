package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const testToken = "SECRET-TOKEN-123"

type hostCall struct {
	fn   string
	args any
}

// fake is the host for one test. Tests replace the package-level callHost, so
// none of them may run in parallel.
type fake struct {
	t           *testing.T
	settings    map[string]any
	entities    map[string]string // id -> entities.get answer; absent = not_found
	kv          map[string]string
	fetchStatus int
	fetchBody   string
	calls       []hostCall
	rows        []logRow
}

func baseSettings() map[string]any {
	return map[string]any{
		"provider": "cloudflare", "zone_id": "z1", "pull_zone_id": "pz1", "service_id": "svc1",
		"api_token": testToken, "site_url": "https://example.com",
		"also_purge": []string{"/", "/blog", "/sitemap.xml", "/feed.xml"},
	}
}

func newFake(t *testing.T, override map[string]any) *fake {
	t.Helper()
	f := &fake{t: t, settings: baseSettings(), entities: map[string]string{}, kv: map[string]string{}, fetchStatus: 200}
	for k, v := range override {
		f.settings[k] = v
	}
	budgetEnd = time.Time{}
	callHost = f.call
	t.Cleanup(func() { callHost = nil; budgetEnd = time.Time{} })
	return f
}

func (f *fake) call(fn string, args, out any) error {
	f.calls = append(f.calls, hostCall{fn, args})
	var resp any
	switch fn {
	case "settings.get":
		resp = f.settings
	case "entities.get":
		id := args.(map[string]string)["id"]
		raw, ok := f.entities[id]
		if !ok {
			return &sdk.HostError{Code: sdk.ErrNotFound, Message: "no such entity"}
		}
		return json.Unmarshal([]byte(raw), out)
	case "kv.get":
		v, ok := f.kv[args.(map[string]string)["key"]]
		resp = map[string]any{"found": ok, "value": v}
	case "kv.set":
		a := args.(map[string]any)
		f.kv[a["key"].(string)] = a["value"].(string)
		return nil
	case "kv.delete":
		delete(f.kv, args.(map[string]string)["key"])
		return nil
	case "http.fetch":
		resp = map[string]any{"status": f.fetchStatus, "body": f.fetchBody}
	case "db.exec":
		a := args.(map[string]any)
		if strings.HasPrefix(a["sql"].(string), "INSERT") {
			p := a["args"].([]any)
			var urls []string
			_ = json.Unmarshal([]byte(p[2].(string)), &urls)
			f.rows = append(f.rows, logRow{ID: int64(len(f.rows) + 1), At: time.Now(), Trigger: p[0].(string),
				Provider: p[1].(string), URLs: urls, Status: p[3].(int), Error: p[4].(string)})
		}
		return nil
	case "db.query":
		b, _ := json.Marshal(f.rows)
		resp = map[string]any{"rows": [][]json.RawMessage{{b}}}
	case "log":
		return nil
	default:
		f.t.Fatalf("unexpected host call %q", fn)
	}
	b, _ := json.Marshal(resp)
	return json.Unmarshal(b, out)
}

func (f *fake) named(fn string) []hostCall {
	var out []hostCall
	for _, c := range f.calls {
		if c.fn == fn {
			out = append(out, c)
		}
	}
	return out
}

func (f *fake) fetches() []map[string]any {
	var out []map[string]any
	for _, c := range f.named("http.fetch") {
		out = append(out, c.args.(map[string]any))
	}
	return out
}

func (f *fake) logLines(level string) []string {
	var out []string
	for _, c := range f.named("log") {
		if m := c.args.(map[string]string); m["level"] == level {
			out = append(out, m["message"])
		}
	}
	return out
}

func (f *fake) setEntity(id, status, path, rawURL string) {
	b, _ := json.Marshal(entity{ID: id, Status: status, Path: path, URL: rawURL})
	f.entities[id] = string(b)
}

func (f *fake) fire(action sdk.Action[sdk.EntityEvent], id string) {
	f.t.Helper()
	in, _ := json.Marshal(sdk.EntityEvent{ID: id})
	if _, err := guest.Handle(action.Export(), in); err != nil {
		f.t.Fatalf("hook returned an error: %v", err)
	}
}

// filesOf reads the Cloudflare files array of one fetch.
func filesOf(t *testing.T, fetch map[string]any) []string {
	t.Helper()
	var body struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(fetch["body"].(string)), &body); err != nil {
		t.Fatal(err)
	}
	return body.Files
}

func headersOf(fetch map[string]any) map[string]string { return fetch["headers"].(map[string]string) }

func TestPublishCloudflareOneCall(t *testing.T) {
	f := newFake(t, nil)
	f.setEntity("e1", "published", "/blog/x", "https://example.com/blog/x")
	f.fire(sdk.EntityPublished, "e1")

	fetches := f.fetches()
	if len(fetches) != 1 {
		t.Fatalf("fetches = %d, want 1", len(fetches))
	}
	fe := fetches[0]
	if fe["method"] != "POST" || fe["url"] != "https://api.cloudflare.com/client/v4/zones/z1/purge_cache" {
		t.Fatalf("request %v %v", fe["method"], fe["url"])
	}
	if h := headersOf(fe)["Authorization"]; h != "Bearer "+testToken {
		t.Fatalf("Authorization = %q", h)
	}
	want := []string{"https://example.com/blog/x", "https://example.com/", "https://example.com/blog",
		"https://example.com/sitemap.xml", "https://example.com/feed.xml"}
	if got := filesOf(t, fe); !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if len(f.rows) != 1 || f.rows[0].Trigger != "published" || f.rows[0].Status != 200 || f.rows[0].Error != "" {
		t.Fatalf("log rows = %+v", f.rows)
	}
	if f.kv["url:e1"] != "https://example.com/blog/x" {
		t.Fatalf("kv = %v", f.kv)
	}
}

func TestBarePathUsesSiteURL(t *testing.T) {
	f := newFake(t, map[string]any{"also_purge": []string{}})
	f.setEntity("e1", "published", "/a/b", "/a/b")
	f.fire(sdk.EntityPublished, "e1")
	if got := filesOf(t, f.fetches()[0]); !slices.Equal(got, []string{"https://example.com/a/b"}) {
		t.Fatalf("files = %v", got)
	}
}

func TestCloudflareChunk30(t *testing.T) {
	var urls []string
	for i := range 65 {
		urls = append(urls, fmt.Sprintf("https://example.com/p%d", i))
	}
	var sizes []int
	for _, c := range chunk(urls, 30) {
		sizes = append(sizes, len(c))
	}
	if !slices.Equal(sizes, []int{30, 30, 5}) {
		t.Fatalf("chunk sizes %v", sizes)
	}

	f := newFake(t, nil)
	s, _ := loadSettings()
	res := purgeURLs(s, urls[:31])
	if len(res) != 2 || len(f.fetches()) != 2 {
		t.Fatalf("results %d, fetches %d, want 2 and 2", len(res), len(f.fetches()))
	}
	if n := len(filesOf(t, f.fetches()[0])); n != 30 {
		t.Fatalf("first request has %d files", n)
	}

	// 20 also_purge entries plus the page fit one request.
	f = newFake(t, map[string]any{"also_purge": urls[:20]})
	f.setEntity("e1", "published", "/x", "https://example.com/x")
	f.fire(sdk.EntityPublished, "e1")
	if len(f.fetches()) != 1 || len(filesOf(t, f.fetches()[0])) != 21 {
		t.Fatalf("fetches %d", len(f.fetches()))
	}
}

func TestBunnyFastlyPerURL(t *testing.T) {
	cases := []struct {
		provider, header, firstURL, secondURL string
	}{
		{"bunny", "AccessKey", "https://api.bunny.net/purge?url=" + url.QueryEscape("https://example.com/blog/x"),
			"https://api.bunny.net/purge?url=" + url.QueryEscape("https://example.com/")},
		{"fastly", "Fastly-Key", "https://api.fastly.com/purge/example.com/blog/x", "https://api.fastly.com/purge/example.com/"},
	}
	for _, c := range cases {
		t.Run(c.provider, func(t *testing.T) {
			f := newFake(t, map[string]any{"provider": c.provider, "also_purge": []string{"/"}})
			f.setEntity("e1", "published", "/blog/x", "https://example.com/blog/x")
			f.fire(sdk.EntityPublished, "e1")
			fetches := f.fetches()
			if len(fetches) != 2 || len(f.rows) != 2 {
				t.Fatalf("fetches %d, rows %d, want 2 and 2", len(fetches), len(f.rows))
			}
			for i, want := range []string{c.firstURL, c.secondURL} {
				if fetches[i]["method"] != "POST" || fetches[i]["url"] != want {
					t.Fatalf("request %d = %v %v, want POST %s", i, fetches[i]["method"], fetches[i]["url"], want)
				}
				if headersOf(fetches[i])[c.header] != testToken {
					t.Fatalf("request %d lacks the %s header", i, c.header)
				}
			}
		})
	}
}

func TestBudgetSkipsTheRest(t *testing.T) {
	f := newFake(t, map[string]any{"provider": "bunny"})
	s, _ := loadSettings()
	budgetEnd = time.Now().Add(-time.Second)
	res := purgeURLs(s, []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"})
	if len(res) != 1 || res[0].ok() || !strings.Contains(res[0].Err, "skipped 3") || len(res[0].URLs) != 3 {
		t.Fatalf("results %+v", res)
	}
	if len(f.fetches()) != 0 {
		t.Fatal("a request started after the budget ran out")
	}
}

func TestDeleteUsesKV(t *testing.T) {
	f := newFake(t, map[string]any{"also_purge": []string{"/"}})
	f.kv["url:e1"] = "https://example.com/old-page"
	f.fire(sdk.EntityDeleted, "e1")

	fetches := f.fetches()
	if len(fetches) != 1 {
		t.Fatalf("fetches = %d", len(fetches))
	}
	if got := filesOf(t, fetches[0]); !slices.Equal(got, []string{"https://example.com/old-page", "https://example.com/"}) {
		t.Fatalf("files = %v", got)
	}
	if _, ok := f.kv["url:e1"]; ok {
		t.Fatal("kv key not deleted")
	}
	// The key goes after the purge, not before.
	var fetchAt, deleteAt int
	for i, c := range f.calls {
		switch c.fn {
		case "http.fetch":
			fetchAt = i
		case "kv.delete":
			deleteAt = i
		}
	}
	if deleteAt < fetchAt {
		t.Fatal("kv.delete ran before the purge")
	}
}

func TestDeleteWithoutKVPurgesAlsoPurgeOnly(t *testing.T) {
	f := newFake(t, map[string]any{"also_purge": []string{"/", "/blog"}})
	f.fire(sdk.EntityDeleted, "e9")
	if got := filesOf(t, f.fetches()[0]); !slices.Equal(got, []string{"https://example.com/", "https://example.com/blog"}) {
		t.Fatalf("files = %v", got)
	}
}

func TestUpdatedMovedAndUnpublished(t *testing.T) {
	f := newFake(t, map[string]any{"also_purge": []string{}})
	f.kv["url:e1"] = "https://example.com/old"
	f.setEntity("e1", "published", "/new", "https://example.com/new")
	f.fire(sdk.EntityUpdated, "e1")
	if got := filesOf(t, f.fetches()[0]); !slices.Equal(got, []string{"https://example.com/new", "https://example.com/old"}) {
		t.Fatalf("moved: files = %v", got)
	}
	if f.kv["url:e1"] != "https://example.com/new" {
		t.Fatalf("kv = %v", f.kv)
	}

	f = newFake(t, map[string]any{"also_purge": []string{}})
	f.kv["url:e1"] = "https://example.com/live"
	f.setEntity("e1", "draft", "/live", "https://example.com/live")
	f.fire(sdk.EntityUpdated, "e1")
	if got := filesOf(t, f.fetches()[0]); !slices.Equal(got, []string{"https://example.com/live"}) {
		t.Fatalf("unpublished: files = %v", got)
	}
	if _, ok := f.kv["url:e1"]; ok {
		t.Fatal("kv key kept after unpublish")
	}

	f = newFake(t, nil)
	f.setEntity("e2", "draft", "/d", "https://example.com/d")
	f.fire(sdk.EntityUpdated, "e2")
	if got := filesOf(t, f.fetches()[0]); len(got) != 5 || got[0] != "https://example.com/d" {
		t.Fatalf("unpublished without kv: files = %v", got)
	}
	if len(f.rows) != 1 {
		t.Fatalf("rows = %+v", f.rows)
	}

	f = newFake(t, nil)
	f.fire(sdk.EntityUpdated, "gone")
	if len(f.fetches()) != 0 || len(f.rows) != 0 {
		t.Fatal("an unknown entity without kv purged something")
	}
}

func TestProviderError500(t *testing.T) {
	f := newFake(t, nil)
	f.fetchStatus = 500
	f.fetchBody = "upstream broke"
	f.setEntity("e1", "published", "/x", "https://example.com/x")
	f.fire(sdk.EntityPublished, "e1") // fire fails the test on a hook error

	if len(f.fetches()) != 1 {
		t.Fatalf("fetches = %d, want 1 (no retry)", len(f.fetches()))
	}
	if lines := f.logLines("error"); len(lines) != 1 {
		t.Fatalf("error log lines = %v", lines)
	}
	if len(f.rows) != 1 || f.rows[0].Status != 500 || !strings.HasPrefix(f.rows[0].Error, "500 upstream broke") {
		t.Fatalf("rows = %+v", f.rows)
	}
}

func TestNoOriginSkips(t *testing.T) {
	f := newFake(t, map[string]any{"site_url": ""})
	f.setEntity("e1", "published", "/x", "/x")
	f.fire(sdk.EntityPublished, "e1")
	if len(f.fetches()) != 0 {
		t.Fatal("purged without an origin")
	}
	lines := f.logLines("warn")
	if len(lines) != 1 || !strings.Contains(lines[0], "set Site URL") {
		t.Fatalf("warn lines = %v", lines)
	}
	if len(f.rows) != 1 || f.rows[0].Error != "set Site URL" {
		t.Fatalf("rows = %+v", f.rows)
	}
}

func TestNotConfiguredDoesNothing(t *testing.T) {
	f := newFake(t, map[string]any{"api_token": ""})
	f.setEntity("e1", "published", "/x", "https://example.com/x")
	f.fire(sdk.EntityPublished, "e1")
	if len(f.fetches()) != 0 || len(f.rows) != 0 {
		t.Fatal("an unconfigured plugin did something")
	}
}

func TestPurgeEverythingToggle(t *testing.T) {
	cases := []struct{ provider, url, header, body string }{
		{"cloudflare", "https://api.cloudflare.com/client/v4/zones/z1/purge_cache", "Authorization", `{"purge_everything":true}`},
		{"bunny", "https://api.bunny.net/pullzone/pz1/purgeCache", "AccessKey", ""},
		{"fastly", "https://api.fastly.com/service/svc1/purge_all", "Fastly-Key", ""},
	}
	for _, c := range cases {
		t.Run(c.provider, func(t *testing.T) {
			f := newFake(t, map[string]any{"provider": c.provider, "purge_everything_on_change": true})
			f.setEntity("e1", "published", "/x", "https://example.com/x")
			f.fire(sdk.EntityPublished, "e1")
			fetches := f.fetches()
			if len(fetches) != 1 || fetches[0]["method"] != "POST" || fetches[0]["url"] != c.url || fetches[0]["body"] != c.body {
				t.Fatalf("fetches = %v", fetches)
			}
			if _, ok := headersOf(fetches[0])[c.header]; !ok {
				t.Fatalf("missing %s header", c.header)
			}
			if len(f.rows) != 1 || len(f.rows[0].URLs) != 0 {
				t.Fatalf("rows = %+v", f.rows)
			}
		})
	}
}

func TestTestConnectionEndpoints(t *testing.T) {
	cases := map[string]string{
		"cloudflare": "https://api.cloudflare.com/client/v4/user/tokens/verify",
		"bunny":      "https://api.bunny.net/pullzone/pz1",
		"fastly":     "https://api.fastly.com/service/svc1",
	}
	for provider, want := range cases {
		f := newFake(t, map[string]any{"provider": provider})
		s, _ := loadSettings()
		if status, _ := testConnection(s); status != 200 {
			t.Fatalf("%s: status %d", provider, status)
		}
		if fe := f.fetches()[0]; fe["method"] != "GET" || fe["url"] != want {
			t.Fatalf("%s: %v %v", provider, fe["method"], fe["url"])
		}
	}
}

func serveRoute(t *testing.T, method, pattern string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Path: pattern, Pattern: pattern})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatal(err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRoutes(t *testing.T) {
	f := newFake(t, nil)
	f.setEntity("e1", "published", "/x", "https://example.com/x")
	f.fire(sdk.EntityPublished, "e1")

	res := serveRoute(t, "GET", "/log")
	var log struct {
		Provider, Target string
		TokenSet         bool `json:"token_set"`
		Configured       bool
		Rows             []logRow
	}
	if res.Status != 200 || json.Unmarshal([]byte(res.Body), &log) != nil {
		t.Fatalf("GET /log: %d %s", res.Status, res.Body)
	}
	if log.Provider != "cloudflare" || log.Target != "z1" || !log.TokenSet || !log.Configured || len(log.Rows) != 1 {
		t.Fatalf("log = %+v", log)
	}

	if res = serveRoute(t, "POST", "/test"); res.Status != 200 || !strings.Contains(res.Body, `"ok":true`) {
		t.Fatalf("POST /test: %d %s", res.Status, res.Body)
	}
	before := len(f.fetches())
	if res = serveRoute(t, "POST", "/purge-all"); res.Status != 200 || !strings.Contains(res.Body, `"ok":true`) {
		t.Fatalf("POST /purge-all: %d %s", res.Status, res.Body)
	}
	if fe := f.fetches()[before]; fe["body"] != `{"purge_everything":true}` {
		t.Fatalf("purge-all body %v", fe["body"])
	}
	if last := f.rows[len(f.rows)-1]; last.Trigger != "purge-all" {
		t.Fatalf("last row %+v", last)
	}
	if res = serveRoute(t, "GET", "/nope"); res.Status != 404 {
		t.Fatalf("unknown route: %d", res.Status)
	}

	f = newFake(t, map[string]any{"provider": ""})
	for _, r := range [][2]string{{"POST", "/purge-all"}, {"POST", "/test"}} {
		if res = serveRoute(t, r[0], r[1]); res.Status != 422 || !strings.Contains(res.Body, "Choose a provider") {
			t.Fatalf("%s %s unconfigured: %d %s", r[0], r[1], res.Status, res.Body)
		}
	}
	if res = serveRoute(t, "GET", "/log"); !strings.Contains(res.Body, `"configured":false`) {
		t.Fatalf("GET /log unconfigured: %s", res.Body)
	}
}

func TestTokenNeverLeaks(t *testing.T) {
	f := newFake(t, nil)
	f.fetchStatus = 403
	f.fetchBody = "bad key " + testToken // a provider that echoes the credential
	f.setEntity("e1", "published", "/x", "https://example.com/x")
	f.fire(sdk.EntityPublished, "e1")
	f.fire(sdk.EntityDeleted, "e1")
	var responses []string
	for _, r := range [][2]string{{"GET", "/log"}, {"POST", "/test"}, {"POST", "/purge-all"}} {
		responses = append(responses, serveRoute(t, r[0], r[1]).Body)
	}

	for _, c := range f.calls {
		b, _ := json.Marshal(c.args)
		switch c.fn {
		case "settings.get": // returns the token, takes no args
		case "http.fetch":
			a := c.args.(map[string]any)
			for _, part := range []string{a["url"].(string), a["body"].(string)} {
				if strings.Contains(part, testToken) {
					t.Fatalf("token in fetch url/body: %s", part)
				}
			}
		default:
			if strings.Contains(string(b), testToken) {
				t.Fatalf("token in %s args: %s", c.fn, b)
			}
		}
	}
	for k, v := range f.kv {
		if strings.Contains(k+v, testToken) {
			t.Fatal("token in kv")
		}
	}
	for _, r := range f.rows {
		if b, _ := json.Marshal(r); strings.Contains(string(b), testToken) {
			t.Fatalf("token in a log row: %s", b)
		}
	}
	for _, body := range responses {
		if strings.Contains(body, testToken) {
			t.Fatalf("token in a route response: %s", body)
		}
	}
	if len(f.rows) == 0 || !strings.Contains(f.rows[0].Error, "***") {
		t.Fatalf("echoed token not masked: %+v", f.rows)
	}
}

func TestLogRetentionAndErrorLength(t *testing.T) {
	f := newFake(t, nil)
	insertLog("published", "cloudflare", []string{"https://example.com/"}, 500, strings.Repeat("é", 500))
	var sqls []string
	for _, c := range f.named("db.exec") {
		sqls = append(sqls, c.args.(map[string]any)["sql"].(string))
	}
	if len(sqls) != 2 || !strings.HasPrefix(sqls[0], "INSERT") || !strings.Contains(sqls[1], "DELETE FROM purge_log") || !strings.Contains(sqls[1], "14 days") {
		t.Fatalf("statements %v", sqls)
	}
	if n := len([]rune(f.rows[0].Error)); n != maxErrChars {
		t.Fatalf("error text has %d chars", n)
	}
}

type manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	SDK         string   `json:"sdk"`
	Permissions []string `json:"permissions"`
	Hosts       []string `json:"allowed_hosts"`
	Routes      []struct {
		Method string   `json:"method"`
		Path   string   `json:"path"`
		Public bool     `json:"public"`
		Roles  []string `json:"roles"`
	} `json:"routes"`
	UI     map[string]any `json:"ui"`
	Limits struct {
		HookTimeouts map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
	Schema struct {
		Properties map[string]struct {
			WriteOnly bool `json:"writeOnly"`
			MaxItems  int  `json:"maxItems"`
		} `json:"properties"`
	} `json:"settings_schema"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifest(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "cdn-purge" || m.Version != "0.1.0" {
		t.Fatalf("name %q version %q", m.Name, m.Version)
	}
	wantPerms := []string{"entities.read", "settings", "http.fetch", "kv", "db", "log"}
	if !slices.Equal(m.Permissions, wantPerms) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	if !slices.Equal(m.Hosts, []string{"api.cloudflare.com", "api.bunny.net", "api.fastly.com"}) {
		t.Fatalf("allowed_hosts %v", m.Hosts)
	}
	var routes []string
	for _, r := range m.Routes {
		if r.Public || len(r.Roles) != 0 {
			t.Fatalf("route %s %s must be owner-only", r.Method, r.Path)
		}
		routes = append(routes, r.Method+" "+r.Path)
	}
	slices.Sort(routes)
	if !slices.Equal(routes, []string{"GET /log", "POST /purge-all", "POST /test"}) {
		t.Fatalf("routes %v", routes)
	}
	if _, ok := m.UI["roles"]; ok || m.UI["title"] != "CDN purge" {
		t.Fatalf("ui %v", m.UI)
	}
	for _, h := range []string{"entity.published", "entity.updated", "entity.deleted"} {
		if m.Limits.HookTimeouts[h] != 10000 {
			t.Fatalf("hook timeout %s = %d", h, m.Limits.HookTimeouts[h])
		}
	}
	if !m.Schema.Properties["api_token"].WriteOnly || m.Schema.Properties["also_purge"].MaxItems != 20 {
		t.Fatalf("schema %+v", m.Schema.Properties)
	}
	// The sdk range matches the module's sdk version (^0.8 with v0.11).
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`hatamoto\.dev/sdk v0\.(8|9|1\d)\.`).Match(mod) || m.SDK != "^0.8" {
		t.Fatalf("sdk range %q does not fit go.mod:\n%s", m.SDK, mod)
	}
	if strings.Contains(string(mod), "replace") {
		t.Fatal("go.mod has a replace")
	}
}
