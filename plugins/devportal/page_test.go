package main

import (
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestOtherPathsReturnAtOnceWithoutAHostCall(t *testing.T) {
	old := callHost
	t.Cleanup(func() { callHost = old })
	callHost = func(fn string, _, _ any) error {
		t.Fatalf("host call %q on an unrelated page", fn)
		return nil
	}
	in := sdk.PageContext{Path: "/blog/hello", Cookies: map[string]string{"sid": "x"}, Blocks: []sdk.Block{{ID: "a", Type: "text"}}}
	out, err := pageFilter(in)
	if err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("%+v %v", out, err)
	}
	for _, p := range []string{"/", "/developers/", "/developers/x", "/Developers"} {
		in.Path = p
		if out, _ := pageFilter(in); !reflect.DeepEqual(in, out) {
			t.Errorf("%s changed", p)
		}
	}
}

func content(t *testing.T, p sdk.PageContext) map[string]any {
	t.Helper()
	b := p.Blocks[0]
	if b.Type != "devportal" || b.ID != "b1" || b.Title != "Developers" {
		t.Fatalf("block %+v", b)
	}
	return b.Content
}

func TestSignedOutSetsTheStateCookieOnce(t *testing.T) {
	h := newPortal(t)
	out := mustFilter(t, devPage(nil))
	c := content(t, out)
	if c["state"] != "signed_out" || len(out.SetCookies) != 1 || out.SetCookies[0].Name != "st" || out.SetCookies[0].MaxAge != 600 {
		t.Fatalf("%v %+v", c, out.SetCookies)
	}
	st := out.SetCookies[0].Value
	u, err := url.Parse(c["authorize_url"].(string))
	if err != nil || u.Query().Get("state") != st || u.Query().Get("scope") != "read:user user:email" {
		t.Fatalf("authorize url %v", c["authorize_url"])
	}
	// A returning visitor keeps the state they have.
	out = mustFilter(t, devPage(map[string]string{"st": "kept"}))
	u, _ = url.Parse(content(t, out)["authorize_url"].(string))
	if len(out.SetCookies) != 0 || u.Query().Get("state") != "kept" {
		t.Fatalf("%+v %v", out.SetCookies, u)
	}
	// The minted state is recorded once; the returning visitor added nothing.
	sets := 0
	for _, call := range h.calls {
		if call.fn == "kv.set" {
			sets++
		}
	}
	if sets != 1 {
		t.Errorf("kv.set calls = %d, want 1 (only the first view mints)", sets)
	}
}

func TestSignedOutWithoutClientIDIsANotice(t *testing.T) {
	h := newPortal(t)
	h.settings = `{"github_client_id":"","public_url":"https://hatamoto.dev"}`
	out := mustFilter(t, devPage(nil))
	if c := content(t, out); c["state"] != "notice" || len(out.SetCookies) != 0 {
		t.Fatalf("%v %+v", c, out.SetCookies)
	}
}

func TestSignupState(t *testing.T) {
	h := newPortal(t)
	h.login("tok", signupRow("csrf-1"))
	c := content(t, mustFilter(t, devPage(map[string]string{"sid": "tok"})))
	if c["state"] != "signup" || c["csrf"] != "csrf-1" || c["login"] != "acme" || c["email1"] != "dev@acme.io" || c["email2"] != "b@acme.io" || c["email3"] != nil {
		t.Fatalf("%v", c)
	}
	if emails, _ := c["emails"].([]any); len(emails) != 2 || emails[0] != "dev@acme.io" {
		t.Fatalf("emails %v", c["emails"])
	}
}

func TestDashboardStates(t *testing.T) {
	h := newPortal(t)
	h.login("with", accountRow("c1", "active", "3f9a1c2b7d4e5f60"))
	h.login("without", accountRow("c2", "active", ""))
	c := content(t, mustFilter(t, devPage(map[string]string{"sid": "with"})))
	if c["state"] != "dashboard" || c["has_key"] != true || c["key_id"] != "3f9a1c2b7d4e5f60" || c["key_added"] != "9 Nov 2026" ||
		c["plan"] != "Free" || c["login"] != "acme" || c["csrf"] != "c1" {
		t.Fatalf("%v", c)
	}
	c = content(t, mustFilter(t, devPage(map[string]string{"sid": "without"})))
	if c["state"] != "dashboard" || c["has_key"] != false {
		t.Fatalf("%v", c)
	}
}

func TestSuspendedAccountSeesNoActions(t *testing.T) {
	h := newPortal(t)
	h.login("tok", accountRow("c1", "suspended", "3f9a1c2b7d4e5f60"))
	c := content(t, mustFilter(t, devPage(map[string]string{"sid": "tok"})))
	if c["state"] != "suspended" || c["csrf"] != nil || c["key_id"] != nil {
		t.Fatalf("%v", c)
	}
}

func TestStaleSessionCookieIsClearedAndFlashShownOnce(t *testing.T) {
	newPortal(t)
	out := mustFilter(t, devPage(map[string]string{"sid": "unknown", "st": "s", "fl": "denied"}))
	c := content(t, out)
	if c["state"] != "signed_out" || c["flash"] != flashMessages["denied"] || c["flash"] == "" {
		t.Fatalf("%v", c)
	}
	cleared := map[string]sdk.SetCookie{}
	for _, sc := range out.SetCookies {
		cleared[sc.Name] = sc
	}
	if cleared["sid"].Value != "" || cleared["sid"].MaxAge != 1 || cleared["fl"].MaxAge != 1 || len(cleared) != 2 {
		t.Fatalf("%+v", out.SetCookies)
	}
}

func TestEveryFlashCodeHasAMessage(t *testing.T) {
	for _, code := range []string{"state", "denied", "noemail", "suspended", "failed", "name", "email", "terms", "key", "keytaken", "haskey"} {
		if flashMessages[code] == "" {
			t.Errorf("no message for %q", code)
		}
	}
}

func TestFailsOpenWhenTheHostFails(t *testing.T) {
	h := newPortal(t)
	h.dbErr = errDB
	in := devPage(map[string]string{"sid": "tok"})
	if out := mustFilter(t, in); !reflect.DeepEqual(in, out) {
		t.Errorf("db error changed the page: %+v", out)
	}
	h.dbErr = nil
	h.settings = `{"github_client_id":`
	if out := mustFilter(t, devPage(nil)); out.Blocks[0].Type != "text" {
		t.Errorf("settings error changed the page")
	}
}

func TestPicksTheAnchoredBlockAndToleratesNone(t *testing.T) {
	newPortal(t)
	in := devPage(nil)
	in.Blocks = []sdk.Block{{ID: "x", Type: "text"}, {ID: "b1", Type: "text", Fields: map[string]any{"anchor": "devportal"}}}
	out := mustFilter(t, in)
	if out.Blocks[0].Type != "text" || out.Blocks[1].Type != "devportal" {
		t.Fatalf("%+v", out.Blocks)
	}
	in.Blocks = []sdk.Block{{ID: "only", Type: "text"}}
	if out := mustFilter(t, in); out.Blocks[0].Type != "devportal" || out.Blocks[0].ID != "only" {
		t.Fatalf("no anchor: %+v", out.Blocks)
	}
	in.Blocks = nil
	if out := mustFilter(t, in); len(out.Blocks) != 0 {
		t.Fatal("blocks invented")
	}
}

func TestSlidingSessionRenewsTheSidCookie(t *testing.T) {
	fixedNow(t, t0)
	h := newPortal(t)
	h.login("tok", strings.Replace(accountRow("c", "active", ""), farFuture, `"expires_at":"2026-11-12T09:00:00+00:00"`, 1))
	out := mustFilter(t, devPage(map[string]string{"sid": "tok"}))
	want := sdk.SetCookie{Name: "sid", Value: "tok", MaxAge: 2592000}
	if !slices.Contains(out.SetCookies, want) {
		t.Fatalf("sid not re-sent when the row slid: %+v", out.SetCookies)
	}
	// A fresh row did not slide: no cookie.
	h.login("tok", accountRow("c", "active", ""))
	out = mustFilter(t, devPage(map[string]string{"sid": "tok"}))
	if len(out.SetCookies) != 0 {
		t.Fatalf("cookie sent without a slide: %+v", out.SetCookies)
	}
}
