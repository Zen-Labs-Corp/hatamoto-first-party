package accounts

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const goodPw = "correct horse"

func signup(r *core.Registry, name, pw string) sdk.HTTPResponse {
	return post(r, "/signup", "", url.Values{"username": {name}, "password": {pw}}.Encode())
}

func TestSignupCreatesAccount(t *testing.T) {
	f := installFake(t)
	resp := signup(newRegistry(nil), "Ann", goodPw)
	if resp.Status != 303 || resp.Headers["Location"] != "/user?id=Ann&m=welcome" {
		t.Fatalf("got %d %q", resp.Status, resp.Headers["Location"])
	}
	if len(f.logins) != 1 || len(f.ensured) != 1 {
		t.Fatalf("logins %d ensured %v", len(f.logins), f.ensured)
	}
	var l *login
	for _, x := range f.logins {
		l = x
	}
	if f.byKey["ck-"+l.Account] == nil || f.byKey["ck-"+l.Account].Username != "Ann" || f.ensured[0] != l.Account {
		t.Fatalf("users row not stored under the ensure key: %+v", f.byKey)
	}
	if !checkPassword(l.PwHash, goodPw) {
		t.Error("stored hash does not verify")
	}
	if want := (&sdk.CommenterSession{Account: l.Account}); resp.CommenterSession == nil || *resp.CommenterSession != *want {
		t.Fatalf("session %+v, want %+v (no Link, no MaxAge, no SignOut)", resp.CommenterSession, want)
	}
	for _, secret := range []string{goodPw, l.PwHash, l.Account} {
		if strings.Contains(resp.Headers["Location"]+resp.Body, secret) {
			t.Errorf("response leaks %q", secret)
		}
	}
	for _, c := range f.host.Calls {
		if c == sdk.FnCommentsSetViewer || c == sdk.FnCommentsByAuthor {
			t.Errorf("signup called %s", c)
		}
	}
}

func TestSignupJSON(t *testing.T) {
	installFake(t)
	resp, _ := newRegistry(nil).ServeHTTP(sdk.HTTPRequest{Method: "POST", Pattern: "/signup",
		Body:    url.Values{"username": {"ann"}, "password": {goodPw}}.Encode(),
		Headers: map[string]string{"sec-fetch-site": "same-origin", "accept": "application/json"}})
	if resp.Status != 200 || resp.Body != `{"username":"ann"}` || resp.CommenterSession == nil || resp.CommenterSession.Account == "" {
		t.Fatalf("got %+v", resp)
	}
}

func TestSignupRefusals(t *testing.T) {
	counted := countCompares(t)
	cases := map[string]struct{ name, pw, code string }{
		"taken":        {"ann", goodPw, "name_taken"},
		"bad name":     {"a b", goodPw, "name_bad"},
		"reserved":     {"Admin", goodPw, "name_reserved"},
		"short pw":     {"bob", "1234567", "pw_bad"},
		"long pw":      {"bob", strings.Repeat("x", 73), "pw_bad"},
		"empty pw":     {"bob", "", "pw_bad"},
		"taken casing": {"ANN", goodPw, "name_taken"},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			f := installFake(t)
			f.add("k-ann", "ann")
			resp := signup(newRegistry(nil), c.name, c.pw)
			if resp.Status != 303 || resp.Headers["Location"] != "/login?m="+c.code {
				t.Fatalf("got %d %q", resp.Status, resp.Headers["Location"])
			}
			if len(f.ensured) != 0 || len(f.logins) != 0 || f.inserts != 0 || resp.CommenterSession != nil {
				t.Fatalf("side effects: %v %d", f.ensured, f.inserts)
			}
		})
	}
	if *counted != 0 {
		t.Errorf("%d bcrypt compares", *counted)
	}
}

func TestSignupGateTargetIsLowerCase(t *testing.T) {
	installFake(t)
	var targets []string
	r := newGateRegistry(core.GateSignup, func(g *core.GateReq) error { targets = append(targets, g.Target); return gateRefusal{} })
	signup(r, "AnN", goodPw)
	if len(targets) != 1 || targets[0] != "ann" {
		t.Fatalf("targets %q", targets)
	}
}

func TestSignupGateRefusal(t *testing.T) {
	f := installFake(t)
	r := newGateRegistry(core.GateSignup, func(*core.GateReq) error { return gateRefusal{} })
	resp := signup(r, "ann", goodPw)
	if resp.Headers["Location"] != "/login?m=name_reserved" || len(f.ensured) != 0 || f.inserts != 0 || resp.CommenterSession != nil {
		t.Fatalf("got %+v ensured %v", resp, f.ensured)
	}
	r = newGateRegistry(core.GateSignup, func(*core.GateReq) error { return errors.New("closed") })
	if resp := signup(r, "ann", goodPw); resp.Headers["Location"] != "/login?m=signup_refused" {
		t.Fatalf("plain refusal: %+v", resp)
	}
}

func TestSignupEnsureError(t *testing.T) {
	f := installFake(t)
	f.ensureErr = errors.New("rate_limited")
	resp := signup(newRegistry(nil), "ann", goodPw)
	if resp.Headers["Location"] != "/login?m=signup_refused" || f.inserts != 0 || len(f.logins) != 0 || resp.CommenterSession != nil {
		t.Fatalf("got %+v", resp)
	}
}

func TestSignupEmptyKeyRefused(t *testing.T) {
	f := installFake(t)
	r := newRegistry(nil)
	f.host.Fns[sdk.FnCommentsIdentityEnsure] = func(json.RawMessage) (any, error) {
		return sdk.CommentsIdentityEnsured{}, nil
	}
	resp := signup(r, "ann", goodPw)
	if resp.Headers["Location"] != "/login?m=signup_refused" || f.inserts != 0 {
		t.Fatalf("got %+v", resp)
	}
}
