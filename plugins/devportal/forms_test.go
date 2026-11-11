package main

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const goodKey = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=" // 32 bytes 01..20

func post(pattern, origin, ctype, body string, cookies map[string]string) sdk.HTTPRequest {
	h := map[string]string{}
	if origin != "" {
		h["origin"] = origin
	}
	if ctype != "" {
		h["content-type"] = ctype
	}
	return sdk.HTTPRequest{Method: "POST", Path: pattern, Pattern: pattern, Headers: h, Body: body, Cookies: cookies}
}

const urlenc = "application/x-www-form-urlencoded"

func send(t *testing.T, r sdk.HTTPRequest) sdk.HTTPResponse {
	t.Helper()
	resp, err := serve(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func wantRedirect(t *testing.T, r sdk.HTTPResponse, flashCode string) {
	t.Helper()
	if r.Status != http.StatusSeeOther || r.Headers["Location"] != "/developers" {
		t.Fatalf("response %+v", r)
	}
	got := ""
	for _, c := range r.SetCookies {
		if c.Name == "fl" {
			got = c.Value
		}
	}
	if got != flashCode {
		t.Fatalf("flash %q, want %q", got, flashCode)
	}
}

func TestEveryPostRefusesBadOriginTokenTypeAndSize(t *testing.T) {
	for _, path := range []string{"/account", "/key", "/logout"} {
		h := newPortal(t)
		h.login("tok", signupRow("csrf-1"))
		ck := map[string]string{"sid": "tok"}
		body := form("csrf", "csrf-1", "display_name", "Acme", "email", "dev@acme.io", "terms", "1", "key", goodKey)
		cases := map[string]sdk.HTTPRequest{
			"wrong origin":     post(path, "https://evil.example", urlenc, body, ck),
			"no origin":        post(path, "", urlenc, body, ck),
			"origin with path": post(path, "https://hatamoto.dev/", urlenc, body, ck),
			"null origin":      post(path, "null", urlenc, body, ck),
			"wrong csrf":       post(path, "https://hatamoto.dev", urlenc, form("csrf", "nope"), ck),
			"missing csrf":     post(path, "https://hatamoto.dev", urlenc, form("terms", "1"), ck),
			"json body":        post(path, "https://hatamoto.dev", "application/json", `{}`, ck),
			"multipart":        post(path, "https://hatamoto.dev", "multipart/form-data; boundary=x", body, ck),
			"no type":          post(path, "https://hatamoto.dev", "", body, ck),
			"huge body":        post(path, "https://hatamoto.dev", urlenc, "csrf=csrf-1&x="+strings.Repeat("a", maxFormBody), ck),
		}
		for name, r := range cases {
			resp := send(t, r)
			if resp.Status < 400 || resp.Headers["Location"] != "" || len(resp.SetCookies) != 0 {
				t.Errorf("%s %s: %+v", path, name, resp)
			}
		}
		if n := len(h.execs("INSERT")) + len(h.execs("DELETE")); n != 0 {
			t.Errorf("%s: a refused request changed data", path)
		}
	}
}

func TestPostStatusCodes(t *testing.T) {
	h := newPortal(t)
	h.login("tok", signupRow("csrf-1"))
	ck := map[string]string{"sid": "tok"}
	for name, tc := range map[string]struct {
		r    sdk.HTTPRequest
		want int
	}{
		"origin": {post("/key", "https://evil.example", urlenc, "", ck), 403},
		"type":   {post("/key", "https://hatamoto.dev", "text/plain", "", ck), 415},
		"size":   {post("/key", "https://hatamoto.dev", urlenc, strings.Repeat("a", maxFormBody+1), ck), 413},
		"csrf":   {post("/key", "https://hatamoto.dev", urlenc, form("csrf", "x"), ck), 403},
	} {
		if got := send(t, tc.r).Status; got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
	// No origin check reads settings before the Origin is known to be wrong.
	h.settings = `{"public_url":""}`
	if got := send(t, post("/key", "https://hatamoto.dev", urlenc, "", ck)).Status; got != 403 {
		t.Errorf("empty public_url accepted an origin: %d", got)
	}
}

func TestPostWithoutASessionGoesBackToThePage(t *testing.T) {
	h := newPortal(t)
	for _, path := range []string{"/account", "/key", "/logout"} {
		wantRedirect(t, send(t, post(path, "https://hatamoto.dev", urlenc, form("csrf", "x"), nil)), "")
		wantRedirect(t, send(t, post(path, "https://hatamoto.dev", urlenc, form("csrf", "x"), map[string]string{"sid": "stale"})), "")
	}
	if len(h.execs("INSERT")) != 0 {
		t.Error("a session-less POST wrote data")
	}
}

func accountPost(extra ...string) sdk.HTTPRequest {
	kv := append([]string{"csrf", "csrf-1", "display_name", " Acme Ltd ", "email", "Dev@Acme.io", "terms", "1"}, extra...)
	return post("/account", "https://hatamoto.dev", urlenc, form(kv...), map[string]string{"sid": "tok"})
}

func TestAccountRoute(t *testing.T) {
	h := newPortal(t)
	h.login("tok", signupRow("csrf-1"))
	h.account = `[{"id":11,"status":"active","login":"acme"}]`
	wantRedirect(t, send(t, accountPost()), "")
	ins := h.execs("INSERT INTO accounts")
	if len(ins) != 1 || ins[0].args[0] != int64(42) || ins[0].args[2] != "Acme Ltd" || ins[0].args[3] != "dev@acme.io" {
		t.Fatalf("insert %+v", ins)
	}
	if att := h.execs("UPDATE sessions SET account"); len(att) != 1 || att[0].args[1] != int64(11) {
		t.Fatalf("attach %+v", att)
	}
}

func TestAccountRouteValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		r    sdk.HTTPRequest
		code string
	}{
		"no terms":      {accountPost("terms", ""), "terms"},
		"terms not 1":   {accountPost("terms", "on"), "terms"},
		"empty name":    {accountPost("display_name", "   "), "name"},
		"long name":     {accountPost("display_name", strings.Repeat("a", 81)), "name"},
		"control chars": {accountPost("display_name", "bad\x07name"), "name"},
		"foreign email": {accountPost("email", "someone@else.io"), "email"},
		"no email":      {accountPost("email", ""), "email"},
	} {
		h := newPortal(t)
		h.login("tok", signupRow("csrf-1"))
		wantRedirect(t, send(t, tc.r), tc.code)
		if len(h.execs("INSERT")) != 0 {
			t.Errorf("%s: account created", name)
		}
	}
}

func TestAccountRouteNeedsASignupSession(t *testing.T) {
	h := newPortal(t)
	h.login("tok", accountRow("csrf-1", "active", ""))
	wantRedirect(t, send(t, accountPost()), "")
	if len(h.execs("INSERT")) != 0 {
		t.Error("an account session created a second account")
	}
}

func keyPost(key string) sdk.HTTPRequest {
	return post("/key", "https://hatamoto.dev", urlenc, form("csrf", "csrf-1", "key", key), map[string]string{"sid": "tok"})
}

func TestKeyRoute(t *testing.T) {
	h := newPortal(t)
	h.login("tok", accountRow("csrf-1", "active", ""))
	wantRedirect(t, send(t, keyPost("  "+goodKey+"\n")), "")
	ins := h.execs("INSERT INTO keys")
	if len(ins) != 1 || ins[0].args[0] != int64(7) || ins[0].args[1] != goodKey || len(ins[0].args[2].(string)) != 16 {
		t.Fatalf("insert %+v", ins)
	}
}

func TestKeyRouteRefusals(t *testing.T) {
	priv := strings.Repeat("A", 86) + "=="
	for name, tc := range map[string]struct {
		key              string
		mine, taken, ins int
		status, code     string
	}{
		"private key":                {priv, 0, 0, 1, "active", "privatekey"},
		"key file with private part": {`{"key_id":"x","public":"` + goodKey + `","private":"` + priv + `"}`, 0, 0, 1, "active", "privatekey"},
		"all zero key":               {base64.StdEncoding.EncodeToString(make([]byte, 32)), 0, 0, 1, "active", "key"},
		"garbage":                    {"nope", 0, 0, 1, "active", "key"},
		"second key":                 {goodKey, 1, 0, 1, "active", "haskey"},
		"taken":                      {goodKey, 0, 1, 1, "active", "keytaken"},
		"suspended":                  {goodKey, 0, 0, 1, "suspended", "suspended"},
		"lost to race":               {goodKey, 0, 0, 0, "active", "suspended"},
	} {
		h := newPortal(t)
		h.login("tok", accountRow("csrf-1", tc.status, ""))
		h.keyMine, h.keyTaken, h.insertN = tc.mine, tc.taken, tc.ins
		wantRedirect(t, send(t, keyPost(tc.key)), tc.code)
		if tc.status == "suspended" && len(h.execs("INSERT")) != 0 {
			t.Errorf("%s: suspended account wrote a key", name)
		}
	}
	// A signup session has no account to attach a key to.
	h := newPortal(t)
	h.login("tok", signupRow("csrf-1"))
	wantRedirect(t, send(t, keyPost(goodKey)), "")
	if len(h.execs("INSERT")) != 0 {
		t.Error("key added without an account")
	}
}

func TestLogoutDeletesTheSessionAndOverwritesTheCookie(t *testing.T) {
	h := newPortal(t)
	h.login("tok", accountRow("csrf-1", "active", ""))
	r := send(t, post("/logout", "https://hatamoto.dev", urlenc, form("csrf", "csrf-1"), map[string]string{"sid": "tok"}))
	wantRedirect(t, r, "")
	if len(r.SetCookies) != 1 || r.SetCookies[0].Name != "sid" || r.SetCookies[0].Value != "" || r.SetCookies[0].MaxAge != 1 {
		t.Fatalf("cookies %+v", r.SetCookies)
	}
	if _, ok := h.sessions[hashToken("tok")]; ok {
		t.Fatal("session row survived")
	}
	s, err := lookupSession("tok")
	if s != nil || err != nil {
		t.Fatalf("old cookie still authenticates: %+v %v", s, err)
	}
}

func TestSuspendedAccountCanStillSignOut(t *testing.T) {
	h := newPortal(t)
	h.login("tok", accountRow("csrf-1", "suspended", ""))
	wantRedirect(t, send(t, post("/logout", "https://hatamoto.dev", urlenc, form("csrf", "csrf-1"), map[string]string{"sid": "tok"})), "")
	if len(h.sessions) != 0 {
		t.Error("session kept")
	}
}
