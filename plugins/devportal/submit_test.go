package main

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const releaseURL = "https://github.com/acme/acme-seo/releases/download/v1.2.0/acme-seo-1.2.0.hmp"

func TestReleaseURLOK(t *testing.T) {
	for u, want := range map[string]bool{
		releaseURL: true,
		"https://github.com/acme/acme-seo/releases/download/v1.2.0/theme.hmp":                  true,
		"http://github.com/acme/acme-seo/releases/download/v1/a.hmp":                           false,
		"https://github.com.evil.example/acme/x/releases/download/v1/a.hmp":                    false,
		"https://user@github.com/acme/x/releases/download/v1/a.hmp":                            false,
		"https://github.com:444/acme/x/releases/download/v1/a.hmp":                             false,
		"https://github.com/acme/x/releases/download/v1/a.zip":                                 false,
		"https://github.com/acme/x/releases/latest/download/a.hmp":                             false,
		"https://github.com/acme/x/releases/download/v1/a.hmp?x=1":                             false,
		"https://github.com/acme/x/releases/download/v1/a.hmp#f":                               false,
		"https://github.com/acme/x/releases/download/v1/../a.hmp":                              false,
		"https://github.com/acme/x/raw/main/a.hmp":                                             false,
		"https://github.com/acme/x/releases/download/v1/a.hmp/extra":                           false,
		"https://github.com/../x/releases/download/v1/a.hmp":                                   false,
		"https://github.com/acme/x/releases/download/v1/" + strings.Repeat("a", 3000) + ".hmp": false,
		"": false,
	} {
		if got := releaseURLOK(u); got != want {
			t.Errorf("releaseURLOK(%.80q) = %v", u, got)
		}
	}
}

func TestSourceURLOK(t *testing.T) {
	for u, want := range map[string]bool{
		"https://github.com/acme/acme-seo":    true,
		"https://github.com/acme/acme-seo/":   true,
		"https://github.com/acme":             false,
		"https://github.com/acme/x/tree/main": false,
		"http://github.com/acme/x":            false,
		"https://gitlab.com/acme/x":           false,
		"https://github.com/acme/x?a=b":       false,
		"https://github.com/acme/x//":         false,
	} {
		if got := sourceURLOK(u); got != want {
			t.Errorf("sourceURLOK(%q) = %v", u, got)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.2.0", "1.2.0", 0}, {"1.10.0", "1.9.9", 1}, {"1.0.0", "1.0.1", -1}, {"v2.0.0", "1.9.0", 1},
		{"1.0.0-rc.1", "1.0.0", -1}, {"1.0.0", "1.0.0-rc.1", 1}, {"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1}, {"1.0.0+b1", "1.0.0+b2", 0}, {"1.0.0-1", "1.0.0-alpha", -1},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// subEnv is a portal plus answers for the submission queries.
type subEnv struct {
	*portal
	x          *hostX
	keyRows    string
	owner      string // jsonb rows of the names query
	listed     string // jsonb rows of the listings query
	recent     int
	revoked    int
	insertID   string // jsonb rows answering the insert; "" = account inactive
	inserts    []hostCall
	pub, keyID string
}

func newSubEnv(t *testing.T, status string) *subEnv {
	t.Helper()
	p := newPortal(t)
	pub, keyID, err := parseKey(goodKey)
	if err != nil {
		t.Fatal(err)
	}
	e := &subEnv{portal: p, pub: pub, keyID: keyID, owner: `[]`, listed: `[]`, insertID: `[{"id":9}]`}
	e.keyRows = `[{"pubkey":"` + pub + `","key_id":"` + keyID + `"}]`
	p.login("tok", accountRow("csrf-1", status, keyID))
	inner := callHost
	callHost = func(fn string, args, out any) error {
		m, _ := args.(map[string]any)
		sql, _ := m["sql"].(string)
		switch {
		case fn == "db.query" && strings.Contains(sql, "FROM keys WHERE account"):
			return cell(out, e.keyRows)
		case fn == "db.query" && strings.Contains(sql, "FROM names"):
			return cell(out, e.owner)
		case fn == "db.query" && strings.Contains(sql, "FROM listings"):
			return cell(out, e.listed)
		case fn == "db.query" && strings.Contains(sql, "FROM submissions WHERE account"):
			return cell(out, `{"n":`+itoa(e.recent)+`}`)
		case fn == "db.query" && strings.Contains(sql, "FROM revocations"):
			return cell(out, `{"n":`+itoa(e.revoked)+`}`)
		case fn == "db.query" && strings.Contains(sql, "INSERT INTO submissions"):
			p.calls = append(p.calls, hostCall{fn: fn, sql: sql, args: m["args"].([]any)})
			e.inserts = append(e.inserts, p.calls[len(p.calls)-1])
			return cell(out, e.insertID)
		}
		return inner(fn, args, out)
	}
	e.x = &hostX{}
	e.x.inspect = sdk.InspectResult{Kind: "plugin", Name: "acme-seo", Version: "1.2.0", SHA256: "ab12", Size: 2 << 20,
		SignerKeyID: keyID, SignatureOK: true, Permissions: []string{"db"}}
	e.x.install(t)
	return e
}

func itoa(n int) string { return strconv.Itoa(n) }

func submitBody(kv ...string) string {
	base := []string{"csrf", "csrf-1", "kind", "plugin", "url", releaseURL, "about", "Fast SEO checks", "category", "SEO"}
	return form(append(base, kv...)...)
}

func (e *subEnv) send(body string) sdk.HTTPResponse {
	e.t.Helper()
	return handleSubmit(post("/submit", "https://hatamoto.dev", urlenc, body, map[string]string{"sid": "tok"}))
}

func TestSubmitSuccessStoresAWaitingRowAndMailsTheReviewer(t *testing.T) {
	e := newSubEnv(t, "active")
	wantRedirect(t, e.send(submitBody("title", "Acme SEO")), "s9")
	if len(e.x.inspected) != 1 || e.x.inspected[0].SignerKey != e.pub || e.x.inspected[0].URL != releaseURL {
		t.Fatalf("inspect calls %+v", e.x.inspected)
	}
	if len(e.inserts) != 1 {
		t.Fatalf("inserts %d", len(e.inserts))
	}
	a := e.inserts[0].args
	if a[0] != int64(7) || a[2] != "acme-seo" || a[3] != "1.2.0" || a[18] != "waiting" {
		t.Errorf("insert args %v", a)
	}
	if mails := e.execs("INSERT INTO mail_queue"); len(mails) != 1 || mails[0].args[0] != "review@example.org" {
		t.Errorf("review mail %+v", mails)
	}
	requireGuard(t, &fakeHost{calls: e.calls})
}

func TestSubmitRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(e *subEnv)
		want   string // text of the failing check
	}{
		"wrong signer": {func(e *subEnv) { e.x.inspect.SignerKeyID = "0123456789abcdef"; e.x.inspect.SignatureOK = false }, "Signed by 0123456789abcdef, not by your key"},
		"unsigned":     {func(e *subEnv) { e.x.inspect.SignerKeyID = ""; e.x.inspect.SignatureOK = false }, "not signed"},
		"wrong kind":   {func(e *subEnv) { e.x.inspect.Kind = "theme" }, "You chose plugin"},
		"reserved":     {func(e *subEnv) { e.x.inspect.Name = "seo" }, "reserved"},
		"first party":  {func(e *subEnv) { e.x.inspect.Name = "searchconsole" }, "reserved"},
		"bad name":     {func(e *subEnv) { e.x.inspect.Name = "Acme_SEO" }, "not a valid name"},
		"name owned":   {func(e *subEnv) { e.owner = `[{"owner":99}]` }, "belongs to another developer"},
		"not newer":    {func(e *subEnv) { e.listed = `[{"version":"1.2.0"}]` }, "not newer"},
		"older":        {func(e *subEnv) { e.listed = `[{"version":"1.3.0"}]` }, "not newer"},
		"problems":     {func(e *subEnv) { e.x.inspect.Problems = []string{"manifest is broken"} }, "manifest is broken"},
		"key revoked":  {func(e *subEnv) { e.revoked = 1 }, "revoked"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newSubEnv(t, "active")
			c.mutate(e)
			wantRedirect(t, e.send(submitBody()), "s9")
			if len(e.inserts) != 1 || e.inserts[0].args[18] != "refused" {
				t.Fatalf("inserts %+v", e.inserts)
			}
			checks := e.inserts[0].args[17].(string)
			if !strings.Contains(checks, c.want) {
				t.Errorf("checks %s lack %q", checks, c.want)
			}
			if len(e.execs("INSERT INTO mail_queue")) != 0 {
				t.Error("a refused submission must not mail the reviewer")
			}
		})
	}
}

func TestSubmitThemeAndPluginMayShareAnOwnedName(t *testing.T) {
	e := newSubEnv(t, "active")
	e.owner = `[{"owner":7}]`
	e.x.inspect.Kind = "theme"
	wantRedirect(t, e.send(submitBody("kind", "theme", "site_type", "blog", "screenshot", "https://example.org/s.png")), "s9")
	if e.inserts[0].args[18] != "waiting" {
		t.Errorf("status %v", e.inserts[0].args[18])
	}
}

func TestSubmitInspectErrorsAreFriendly(t *testing.T) {
	for _, code := range []string{sdk.ErrTimeout, sdk.ErrBlocked, sdk.ErrRateLimited, sdk.ErrUnavailable, sdk.ErrCapabilityDenied, sdk.ErrInvalidArgument} {
		e := newSubEnv(t, "active")
		e.x.inspectErr = hostErr(code)
		wantRedirect(t, e.send(submitBody()), "sub-busy")
		if len(e.inserts) != 0 {
			t.Errorf("%s: stored a row", code)
		}
	}
	e := newSubEnv(t, "active")
	e.x.inspectErr = errors.New("plain")
	wantRedirect(t, e.send(submitBody()), "sub-busy")
}

func TestSubmitUnreadableFileStoresNothingAndNamesTheDownloadProblem(t *testing.T) {
	e := newSubEnv(t, "active")
	e.x.inspect = sdk.InspectResult{Problems: []string{"download answered 404"}}
	wantRedirect(t, e.send(submitBody()), "sub-download-404")
	if len(e.inserts) != 0 {
		t.Fatalf("stored a row: %+v", e.inserts)
	}
	if got := flashText("sub-download-404"); got != "We could not download that file: download answered 404. Check the release URL." {
		t.Errorf("text %q", got)
	}
	e = newSubEnv(t, "active")
	e.x.inspect = sdk.InspectResult{}
	wantRedirect(t, e.send(submitBody()), "sub-download")
	if len(e.inserts) != 0 || flashText("sub-download") == "" {
		t.Errorf("inserts %d", len(e.inserts))
	}
}

func TestSubmitBadInputNeverInspects(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		code string
	}{
		"http url":      {submitBody("url", "http://github.com/a/b/releases/download/v1/a.hmp"), "sub-url"},
		"other host":    {submitBody("url", "https://example.org/a.hmp"), "sub-url"},
		"bad source":    {submitBody("source", "https://example.org/acme"), "sub-source"},
		"no about":      {submitBody("about", " "), "sub-about"},
		"long about":    {submitBody("about", strings.Repeat("x", 601)), "sub-about"},
		"long title":    {submitBody("title", strings.Repeat("x", 81)), "sub-title"},
		"bad category":  {submitBody("category", "Nope"), "sub-category"},
		"plugin shot":   {submitBody("screenshot", "https://example.org/s.png"), "sub-screenshot"},
		"theme http":    {submitBody("kind", "theme", "site_type", "blog", "screenshot", "http://example.org/s.png"), "sub-screenshot"},
		"theme no type": {submitBody("kind", "theme"), "sub-category"},
		"unknown kind":  {submitBody("kind", "widget"), "sub-kind"},
	} {
		e := newSubEnv(t, "active")
		wantRedirect(t, e.send(c.body), c.code)
		if len(e.x.inspected) != 0 || len(e.inserts) != 0 {
			t.Errorf("%s: inspected or stored", name)
		}
	}
}

func TestSubmitAccountRules(t *testing.T) {
	e := newSubEnv(t, "suspended")
	wantRedirect(t, e.send(submitBody()), "suspended")
	if len(e.x.inspected) != 0 {
		t.Error("suspended account inspected")
	}

	e = newSubEnv(t, "active")
	e.recent = 10
	wantRedirect(t, e.send(submitBody()), "sub-limit")
	if len(e.x.inspected) != 0 {
		t.Error("over the cap inspected")
	}

	e = newSubEnv(t, "active")
	e.insertID = `[]`
	wantRedirect(t, e.send(submitBody()), "suspended")

	e = newSubEnv(t, "active")
	e.portal.login("tok", accountRow("csrf-1", "active", ""))
	wantRedirect(t, e.send(submitBody()), "sub-nokey")

	e = newSubEnv(t, "active")
	e.portal.login("nosess", signupRow("csrf-1"))
	r := handleSubmit(post("/submit", "https://hatamoto.dev", urlenc, submitBody(), map[string]string{"sid": "nosess"}))
	wantRedirect(t, r, "")
}

func TestSubmitGuardRefusesOversizedAndForeignRequests(t *testing.T) {
	e := newSubEnv(t, "active")
	ck := map[string]string{"sid": "tok"}
	big := submitBody("about", strings.Repeat("x", 9*1024))
	for name, r := range map[string]sdk.HTTPRequest{
		"too large":    post("/submit", "https://hatamoto.dev", urlenc, big, ck),
		"wrong origin": post("/submit", "https://evil.example", urlenc, submitBody(), ck),
		"bad csrf":     post("/submit", "https://hatamoto.dev", urlenc, strings.Replace(submitBody(), "csrf-1", "nope", 1), ck),
		"wrong type":   post("/submit", "https://hatamoto.dev", "application/json", submitBody(), ck),
	} {
		if resp := handleSubmit(r); resp.Status < 400 {
			t.Errorf("%s: status %d", name, resp.Status)
		}
	}
	if len(e.x.inspected) != 0 {
		t.Error("guarded request reached inspect")
	}
}
