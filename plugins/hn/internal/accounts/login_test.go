package accounts

import (
	"errors"
	"net/url"
	"reflect"
	"sync"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"golang.org/x/crypto/bcrypt"
	"hatamoto.dev/sdk"
)

const annPw = "correct horse"

var (
	annHashOnce sync.Once
	annHash     string
)

// annHashAtCost is a hash of annPw at hashCost, made once per test binary.
func annHashAtCost(t *testing.T) string {
	t.Helper()
	annHashOnce.Do(func() {
		h, err := hashPassword(annPw)
		if err != nil {
			t.Fatal(err)
		}
		annHash = h
	})
	return annHash
}

// loginFixture has Ann (password member, account "acc-ann", key "ck-acc-ann") and Eve
// (an email-link member with no login).
func loginFixture(t *testing.T) *fakeUsers {
	t.Helper()
	f := installFake(t)
	f.addLogin(f.add("ck-acc-ann", "Ann"), "acc-ann", annHashAtCost(t))
	f.add("k-eve", "Eve")
	return f
}

func doLogin(r *core.Registry, name, pw string) sdk.HTTPResponse {
	return post(r, "/login", "", url.Values{"username": {name}, "password": {pw}}.Encode())
}

func TestLoginNoEnumeration(t *testing.T) {
	var hashes []string
	orig := compareHash
	compareHash = func(h, p []byte) error { hashes = append(hashes, string(h)); return orig(h, p) }
	t.Cleanup(func() { compareHash = orig })

	f := loginFixture(t)
	cases := map[string]struct{ name, pw string }{
		"unknown user":      {"nobody", "wrong horse!"},
		"wrong password":    {"Ann", "wrong horse!"},
		"member no login":   {"Eve", "wrong horse!"},
		"invalid name":      {"a b", "wrong horse!"},
		"long password":     {"Ann", "wrong horse! wrong horse! wrong horse! wrong horse! wrong horse! wrong horse! wrong"},
		"empty credentials": {"", ""},
	}
	var first *sdk.HTTPResponse
	for label, c := range cases {
		hashes = nil
		resp := doLogin(newRegistry(nil), c.name, c.pw)
		if len(hashes) != 1 {
			t.Errorf("%s: %d compares", label, len(hashes))
		}
		if label != "wrong password" && label != "long password" && hashes[0] != dummyHash {
			t.Errorf("%s: compared against a real hash", label)
		}
		if resp.Status != 303 || resp.Headers["Location"] != "/login?m=bad_login" || resp.CommenterSession != nil {
			t.Errorf("%s: %+v", label, resp)
		}
		if first == nil {
			first = &resp
		} else if !reflect.DeepEqual(*first, resp) {
			t.Errorf("%s: response differs: %+v vs %+v", label, resp, *first)
		}
	}
	if len(f.ensured) != 0 {
		t.Errorf("ensure called on failed logins: %v", f.ensured)
	}
	if c, err := bcrypt.Cost([]byte(dummyHash)); err != nil || c != hashCost {
		t.Errorf("dummy cost %d, %v", c, err)
	}
	if c, _ := bcrypt.Cost([]byte(annHashAtCost(t))); c != hashCost {
		t.Errorf("stored cost %d", c)
	}
}

func TestLoginSuccess(t *testing.T) {
	f := loginFixture(t)
	resp := doLogin(newRegistry(nil), "aNN", annPw)
	if resp.Status != 303 || resp.Headers["Location"] != "/" {
		t.Fatalf("got %d %q", resp.Status, resp.Headers["Location"])
	}
	if want := (&sdk.CommenterSession{Account: "acc-ann"}); resp.CommenterSession == nil || *resp.CommenterSession != *want {
		t.Fatalf("session %+v", resp.CommenterSession)
	}
	if len(f.ensured) != 1 || f.ensured[0] != "acc-ann" {
		t.Fatalf("ensured %v", f.ensured)
	}
	// next is honoured when safe
	resp = post(newRegistry(nil), "/login", "", url.Values{"username": {"Ann"}, "password": {annPw}, "next": {"/submit"}}.Encode())
	if resp.Headers["Location"] != "/submit" {
		t.Errorf("next: %q", resp.Headers["Location"])
	}
}

func TestLoginReattach(t *testing.T) {
	t.Run("different key", func(t *testing.T) {
		f := loginFixture(t)
		f.ensureKeys = map[string]string{"acc-ann": "ck-new"}
		resp := doLogin(newRegistry(nil), "Ann", annPw)
		if resp.CommenterSession == nil || f.byKey["ck-new"] == nil || f.byKey["ck-acc-ann"] != nil || f.byKey["ck-new"].Username != "Ann" {
			t.Fatalf("not re-attached: %+v %+v", resp, f.byKey)
		}
	})
	t.Run("same key", func(t *testing.T) {
		f := loginFixture(t)
		doLogin(newRegistry(nil), "Ann", annPw)
		if f.updates != 0 || f.byKey["ck-acc-ann"] == nil {
			t.Fatalf("key changed: %+v", f.byKey)
		}
	})
	t.Run("ensure error", func(t *testing.T) {
		f := loginFixture(t)
		f.ensureErr = errors.New("rate_limited")
		resp := doLogin(newRegistry(nil), "Ann", annPw)
		if resp.Status != 303 || resp.CommenterSession == nil || f.byKey["ck-acc-ann"] == nil {
			t.Fatalf("got %+v", resp)
		}
	})
	t.Run("update conflict", func(t *testing.T) {
		f := loginFixture(t)
		f.add("ck-taken", "Zed")
		f.ensureKeys = map[string]string{"acc-ann": "ck-taken"}
		resp := doLogin(newRegistry(nil), "Ann", annPw)
		if resp.Status != 303 || resp.CommenterSession == nil || f.byKey["ck-acc-ann"] == nil {
			t.Fatalf("got %+v", resp)
		}
	})
}

func TestLoginGateRefusal(t *testing.T) {
	f := loginFixture(t)
	n := countCompares(t)
	var targets []string
	r := newGateRegistry(core.GateLogin, func(g *core.GateReq) error { targets = append(targets, g.Target); return gateRefusal{} })
	for _, name := range []string{"Bob", "a b"} {
		resp := doLogin(r, name, annPw)
		if resp.Headers["Location"] != "/login?m=name_reserved" || resp.CommenterSession != nil {
			t.Fatalf("%q: %+v", name, resp)
		}
	}
	if len(targets) != 2 || targets[0] != "bob" || targets[1] != "" {
		t.Errorf("targets %q", targets)
	}
	if *n != 0 || len(f.ensured) != 0 {
		t.Errorf("hashed or ensured behind a refusing gate: %d %v", *n, f.ensured)
	}
	// JSON gets 429, no hash
	resp, _ := r.ServeHTTP(sdk.HTTPRequest{Method: "POST", Pattern: "/login", Body: "username=Ann&password=x",
		Headers: map[string]string{"sec-fetch-site": "same-origin", "accept": "application/json"}})
	if resp.Status != 429 || *n != 0 {
		t.Errorf("json refusal: %d", resp.Status)
	}
}

func TestLoginRehash(t *testing.T) {
	f := installFake(t)
	old, err := bcrypt.GenerateFromPassword([]byte(annPw), 10)
	if err != nil {
		t.Fatal(err)
	}
	f.addLogin(f.add("ck-acc-ann", "Ann"), "acc-ann", string(old))
	if resp := doLogin(newRegistry(nil), "Ann", annPw); resp.CommenterSession == nil {
		t.Fatalf("got %+v", resp)
	}
	l, _ := loginByName("Ann")
	if c, _ := bcrypt.Cost([]byte(l.PwHash)); c != hashCost || !checkPassword(l.PwHash, annPw) {
		t.Fatalf("hash not upgraded: cost %d", c)
	}
}

func TestLoginHostError(t *testing.T) {
	f := loginFixture(t)
	f.queryErr = errors.New("boom")
	resp := doLogin(newRegistry(nil), "Ann", annPw)
	if resp.Headers["Location"] != "/login?m=try_again" || resp.CommenterSession != nil {
		t.Fatalf("got %+v", resp)
	}
}

func TestLoginRefusesOtherOrigins(t *testing.T) {
	f := loginFixture(t)
	n := countCompares(t)
	for _, site := range []string{"same-site", "", "cross-site", "none"} {
		resp := postFrom(newRegistry(nil), "/login", "", site, url.Values{"username": {"Ann"}, "password": {annPw}})
		if resp.Status != 403 || resp.CommenterSession != nil {
			t.Errorf("site %q: %d", site, resp.Status)
		}
	}
	if *n != 0 || len(f.ensured) != 0 {
		t.Errorf("side effects: %d %v", *n, f.ensured)
	}
}

// Only a wrong password is reported back to the login gate (it feeds the caps that a real login must not use up).
func TestLoginReportsOnlyFailures(t *testing.T) {
	loginFixture(t)
	failed := 0
	r := newGateRegistry(core.GateLogin, func(g *core.GateReq) error { g.Failed = func() { failed++ }; return nil })
	if resp := doLogin(r, "Ann", annPw); resp.CommenterSession == nil || failed != 0 {
		t.Fatalf("good password: %+v failed=%d", resp, failed)
	}
	for _, name := range []string{"Ann", "nobody"} {
		doLogin(r, name, "wrong horse!")
	}
	if failed != 2 {
		t.Fatalf("failed = %d, want 2", failed)
	}
}
