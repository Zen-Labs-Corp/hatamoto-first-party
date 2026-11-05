package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const (
	testID    = "123e4567-e89b-12d3-a456-426614174000"
	testVoter = "0123456789abcdef0123456789abcdef"
)

func form(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v.Encode()
}

func okForm(extra ...string) string {
	base := []string{"entity_id", testID, "stars", "4", "schema", "post", "return_to", "/blog/a", "path", "/blog/a"}
	return form(append(base, extra...)...)
}

func post(body string, hdr map[string]string, cookies map[string]string) sdk.HTTPRequest {
	return sdk.HTTPRequest{Method: "POST", Pattern: "/rate", Path: "/rate", Body: body, Headers: hdr, Cookies: cookies}
}

func votes(f *fakeHost) []hostCall {
	var out []hostCall
	for _, c := range f.calls {
		if c.fn == "db.exec" && strings.Contains(c.sql, "INTO votes") {
			out = append(out, c)
		}
	}
	return out
}

func assertSeeOther(t *testing.T, r sdk.HTTPResponse, loc string) {
	t.Helper()
	if r.Status != 303 || r.Headers["Location"] != loc {
		t.Fatalf("response %d %q, want 303 %q", r.Status, r.Headers["Location"], loc)
	}
	if r.Headers["Cache-Control"] != "no-store" {
		t.Fatalf("cache-control %q", r.Headers["Cache-Control"])
	}
}

func TestRateInvalidInputIsIgnoredWith303(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		hdr  map[string]string
		loc  string
	}{
		{"empty body", "", nil, "/"},
		{"malformed body", "%zz=1&entity_id", nil, "/"},
		{"no stars", form("entity_id", testID, "schema", "post"), nil, "/"},
		{"stars 0", okForm("stars", "0"), nil, "/blog/a"},
		{"stars 6", form("entity_id", testID, "stars", "6", "schema", "post", "return_to", "/a"), nil, "/a"},
		{"stars x", form("entity_id", testID, "stars", "x", "schema", "post"), nil, "/"},
		{"stars 3.5", form("entity_id", testID, "stars", "3.5", "schema", "post"), nil, "/"},
		{"stars 44", form("entity_id", testID, "stars", "44", "schema", "post"), nil, "/"},
		{"stars empty", form("entity_id", testID, "stars", "", "schema", "post"), nil, "/"},
		{"bad uuid", form("entity_id", "nope", "stars", "3", "schema", "post"), nil, "/"},
		{"missing uuid", form("stars", "3", "schema", "post"), nil, "/"},
		{"schema not allowed", form("entity_id", testID, "stars", "3", "schema", "event", "return_to", "/e"), nil, "/e"},
		{"schema missing", form("entity_id", testID, "stars", "3"), nil, "/"},
		{"cross-site", okForm(), map[string]string{"sec-fetch-site": "cross-site"}, "/blog/a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeHost{settings: `{}`}
			f.install(t)
			assertSeeOther(t, handleRate(post(tc.body, tc.hdr, nil)), tc.loc)
			if len(votes(f)) != 0 {
				t.Fatalf("a vote was written: %+v", f.calls)
			}
		})
	}
}

func TestRateOversizedBody(t *testing.T) {
	(&fakeHost{settings: `{}`}).install(t)
	r := handleRate(post(okForm()+"&x="+strings.Repeat("a", maxBody), nil, nil))
	if r.Status != 413 {
		t.Fatalf("status %d", r.Status)
	}
	if r := handleRate(sdk.HTTPRequest{Method: "POST", Pattern: "/rate", BodyB64: "/w=="}); r.Status != 413 {
		t.Fatalf("binary status %d", r.Status)
	}
}

func TestRateFetchSiteHeader(t *testing.T) {
	for site, want := range map[string]int{"same-origin": 1, "same-site": 1, "none": 1, "": 1, "cross-site": 0} {
		f := &fakeHost{settings: `{}`}
		f.install(t)
		var hdr map[string]string
		if site != "" {
			hdr = map[string]string{"sec-fetch-site": site}
		}
		assertSeeOther(t, handleRate(post(okForm(), hdr, nil)), "/blog/a")
		if got := len(votes(f)); got != want {
			t.Errorf("sec-fetch-site %q: %d votes, want %d", site, got, want)
		}
	}
}

func TestRateReturnToIsAlwaysAHostSafeLocation(t *testing.T) {
	for in, want := range map[string]string{
		"//evil.com":  "/",
		"/\\x":        "/",
		"https://x":   "/",
		"":            "/",
		"javascript:": "/",
		"/ok?a=1":     "/ok?a=1",
		"/a b":        "/",
		"/é":          "/%C3%A9",
	} {
		(&fakeHost{settings: `{}`}).install(t)
		r := handleRate(post(okForm("return_to", in), nil, nil))
		assertSeeOther(t, r, want)
		loc := r.Headers["Location"]
		if !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") || strings.Contains(loc, "\\") {
			t.Errorf("unsafe location %q for %q", loc, in)
		}
	}
}

func TestRateVoterCookie(t *testing.T) {
	t.Run("first vote sets the cookie", func(t *testing.T) {
		f := &fakeHost{settings: `{}`}
		f.install(t)
		r := handleRate(post(okForm(), nil, nil))
		if len(r.SetCookies) != 1 {
			t.Fatalf("cookies %+v", r.SetCookies)
		}
		c := r.SetCookies[0]
		if c.Name != "voter" || c.MaxAge != 31536000 || !validVoter(c.Value) || c.Path != "/" {
			t.Fatalf("cookie %+v", c)
		}
		if v := votes(f); len(v) != 1 || v[0].args[1] != c.Value {
			t.Fatalf("vote %+v", v)
		}
	})
	t.Run("existing cookie is kept", func(t *testing.T) {
		f := &fakeHost{settings: `{}`}
		f.install(t)
		r := handleRate(post(okForm(), nil, map[string]string{"voter": testVoter}))
		if len(r.SetCookies) != 0 {
			t.Fatalf("cookies %+v", r.SetCookies)
		}
		if v := votes(f); len(v) != 1 || v[0].args[1] != testVoter {
			t.Fatalf("vote %+v", v)
		}
	})
	t.Run("invalid cookie is replaced", func(t *testing.T) {
		(&fakeHost{settings: `{}`}).install(t)
		r := handleRate(post(okForm(), nil, map[string]string{"voter": "short"}))
		if len(r.SetCookies) != 1 || !validVoter(r.SetCookies[0].Value) {
			t.Fatalf("cookies %+v", r.SetCookies)
		}
	})
}

func TestRateAllowChange(t *testing.T) {
	for settings, want := range map[string]string{
		`{"allow_change":false}`: "DO NOTHING",
		`{}`:                     "DO NOTHING",
		`{"allow_change":true}`:  "DO UPDATE SET stars",
	} {
		f := &fakeHost{settings: settings}
		f.install(t)
		handleRate(post(okForm(), nil, map[string]string{"voter": testVoter}))
		if v := votes(f); len(v) != 1 || !strings.Contains(v[0].sql, want) {
			t.Errorf("settings %s: %+v, want %q", settings, v, want)
		}
	}
}

func TestRateStoresOnlyIdVoterStarsAndPath(t *testing.T) {
	f := &fakeHost{settings: `{}`}
	f.install(t)
	hdr := map[string]string{"sec-fetch-site": "same-origin", "user-agent": "UA-secret", "x-forwarded-for": "203.0.113.9", "referer": "https://r.example/"}
	handleRate(post(okForm(), hdr, map[string]string{"voter": testVoter}))
	var writes int
	for _, c := range f.calls {
		if c.fn != "db.exec" {
			continue
		}
		writes++
		for _, a := range c.args {
			switch v := a.(type) {
			case int:
				if v != 4 {
					t.Errorf("arg %v", v)
				}
			case string:
				if v != testID && v != testVoter && v != "/blog/a" {
					t.Errorf("unexpected arg %q in %s", v, c.sql)
				}
			default:
				t.Errorf("unexpected arg %T", a)
			}
		}
	}
	if writes != 2 {
		t.Fatalf("%d writes", writes)
	}
}

func TestRateStoresPathOnlyWhenClean(t *testing.T) {
	for path, want := range map[string]int{"/blog/a": 1, "//evil": 0, "": 0, "x": 0} {
		f := &fakeHost{settings: `{}`}
		f.install(t)
		handleRate(post(okForm("path", path), nil, nil))
		var n int
		for _, c := range f.calls {
			if strings.Contains(c.sql, "INTO pages") {
				n++
			}
		}
		if n != want {
			t.Errorf("path %q: %d page writes, want %d", path, n, want)
		}
	}
}

func pageWrites(f *fakeHost) (n int) {
	for _, c := range f.calls {
		if strings.Contains(c.sql, "INTO pages") {
			n++
		}
	}
	return n
}

func TestRatePathIsFirstWriteWins(t *testing.T) {
	if !strings.Contains(sqlPage, "DO NOTHING") || strings.Contains(sqlPage, "DO UPDATE") {
		t.Fatalf("sqlPage must not overwrite: %s", sqlPage)
	}
}

func TestRateDuplicateVoteStoresNoPath(t *testing.T) {
	f := &fakeHost{settings: `{}`, db: func(c hostCall, out any) error {
		if strings.Contains(c.sql, "INTO votes") {
			return json.Unmarshal([]byte(`{"rows_affected":0}`), out)
		}
		return nil
	}}
	f.install(t)
	r := handleRate(post(okForm("path", "/pay-here"), nil, map[string]string{"voter": testVoter}))
	assertSeeOther(t, r, "/blog/a")
	if n := pageWrites(f); n != 0 {
		t.Fatalf("%d page writes after a duplicate vote", n)
	}
}

func TestRateDatabaseErrorStillRedirects(t *testing.T) {
	f := &fakeHost{settings: `{}`, db: func(hostCall, any) error { return errors.New("unavailable") }}
	f.install(t)
	r := handleRate(post(okForm(), nil, nil))
	assertSeeOther(t, r, "/blog/a")
	if len(r.SetCookies) != 1 {
		t.Fatalf("cookie not set: %+v", r.SetCookies)
	}
	for _, c := range f.calls {
		if strings.Contains(c.sql, "INTO pages") {
			t.Fatal("page written after a failed vote")
		}
	}
}

func TestRateSettingsErrorUsesDefaults(t *testing.T) {
	f := &fakeHost{settingsErr: errors.New("denied")}
	f.install(t)
	handleRate(post(okForm(), nil, nil))
	if len(votes(f)) != 1 {
		t.Fatal("default schema post should be allowed")
	}
}

func TestServeRoutes(t *testing.T) {
	(&fakeHost{settings: `{}`}).install(t)
	r, err := serve(sdk.HTTPRequest{Method: "POST", Pattern: "/rate", Body: okForm()})
	if err != nil || r.Status != 303 {
		t.Fatalf("%v %d", err, r.Status)
	}
	r, err = serve(sdk.HTTPRequest{Method: "GET", Pattern: "/nope"})
	if err != nil || r.Status != 404 {
		t.Fatalf("%v %d", err, r.Status)
	}
}
