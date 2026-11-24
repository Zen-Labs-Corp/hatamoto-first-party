package accounts

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// newRegistry registers the accounts phase and, when gate is set, a signup gate.
func newRegistry(gate func(*core.GateReq) error) *core.Registry {
	r := core.NewRegistry()
	r.Register("accounts", Register)
	r.Register("stories", func(*core.Registry) {})
	r.Register("comments", func(*core.Registry) {})
	r.Register("moderation", func(r *core.Registry) {
		if gate != nil {
			r.Gate(core.GateSignup, gate)
		}
	})
	return r
}

func post(r *core.Registry, route, key, body string) sdk.HTTPResponse {
	resp, _ := r.ServeHTTP(sdk.HTTPRequest{
		Method: "POST", Pattern: route, Body: body, CommenterKey: key,
		Headers: map[string]string{"sec-fetch-site": "same-origin"},
	})
	return resp
}

func claim(r *core.Registry, key, name string) sdk.HTTPResponse {
	return post(r, "/username", key, url.Values{"username": {name}, "next": {"/login"}}.Encode())
}

type gateRefusal struct{}

func (gateRefusal) Error() string       { return "closed" }
func (gateRefusal) Notice() core.Notice { return noticeNameReserved }

func TestClaimUsername(t *testing.T) {
	expect := func(t *testing.T, resp sdk.HTTPResponse, loc string) {
		t.Helper()
		if resp.Status != 303 || resp.Headers["Location"] != loc {
			t.Fatalf("got %d %q, want 303 %q", resp.Status, resp.Headers["Location"], loc)
		}
		if strings.Contains(resp.Headers["Location"]+resp.Body, "k1") {
			t.Fatalf("response leaks the commenter key: %+v", resp)
		}
	}

	t.Run("accepted", func(t *testing.T) {
		for i, name := range []string{"ab", "abcdefghijklmno", "a-b_9"} {
			f := installFake(t)
			resp := claim(newRegistry(nil), "k1", name)
			expect(t, resp, "/user?id="+name+"&m=welcome")
			if f.byKey["k1"] == nil || f.byKey["k1"].Username != name || f.inserts != 1 {
				t.Fatalf("case %d: row not stored: %+v", i, f.byKey)
			}
		}
	})

	refused := map[string]struct{ name, code string }{
		"one char":      {"a", "name_bad"},
		"sixteen chars": {"abcdefghijklmnop", "name_bad"},
		"space inside":  {"a b", "name_bad"},
		"leading space": {" ab", "name_bad"},
		"dot":           {"a.b", "name_bad"},
		"empty":         {"", "name_bad"},
		"admin":         {"Admin", "name_reserved"},
		"page segment":  {"NEWS", "name_reserved"},
		"one letter p":  {"p", "name_bad"},
		"item":          {"item", "name_reserved"},
	}
	for label, c := range refused {
		t.Run("refused "+label, func(t *testing.T) {
			f := installFake(t)
			expect(t, claim(newRegistry(nil), "k1", c.name), "/login?m="+c.code)
			if f.inserts != 0 {
				t.Fatalf("INSERT ran for %q", c.name)
			}
		})
	}

	t.Run("taken in another case", func(t *testing.T) {
		f := installFake(t)
		r := newRegistry(nil)
		expect(t, claim(r, "k1", "Bob"), "/user?id=Bob&m=welcome")
		resp := claim(r, "k2", "bob")
		if resp.Headers["Location"] != "/login?m=name_taken" || f.byKey["k2"] != nil {
			t.Fatalf("got %+v", resp)
		}
	})

	t.Run("second claim by the same key", func(t *testing.T) {
		f := installFake(t)
		r := newRegistry(nil)
		claim(r, "k1", "bob")
		expect(t, claim(r, "k1", "bob2"), "/login?m=name_has")
		if f.inserts != 1 {
			t.Fatalf("inserts = %d", f.inserts)
		}
	})

	t.Run("no commenter key", func(t *testing.T) {
		f := installFake(t)
		expect(t, claim(newRegistry(nil), "", "bob"), "/login?m=signin_first")
		if len(f.host.Calls) != 0 {
			t.Fatalf("host called: %v", f.host.Calls)
		}
	})

	t.Run("host error", func(t *testing.T) {
		f := installFake(t)
		f.queryErr = errors.New("down")
		expect(t, claim(newRegistry(nil), "k1", "bob"), "/login?m=try_again")
	})

	t.Run("gate refuses", func(t *testing.T) {
		f := installFake(t)
		var target string
		r := newRegistry(func(g *core.GateReq) error { target = g.Target; return errors.New("closed") })
		expect(t, claim(r, "k1", "bob"), "/login?m=signup_refused")
		if f.inserts != 0 || target != "bob" {
			t.Fatalf("inserts=%d target=%q", f.inserts, target)
		}
	})

	t.Run("gate picks the notice", func(t *testing.T) {
		f := installFake(t)
		r := newRegistry(func(*core.GateReq) error { return gateRefusal{} })
		expect(t, claim(r, "k1", "bob"), "/login?m=name_reserved")
		if f.inserts != 0 {
			t.Fatal("INSERT ran")
		}
	})
}

type secretGateErr struct{}

func (secretGateErr) Error() string { return "blocked by rule 7 for 10.0.0.9" }

func TestClaimJSONGateRefusalIsGeneric(t *testing.T) {
	f := installFake(t)
	r := newRegistry(func(*core.GateReq) error { return secretGateErr{} })
	resp, _ := r.ServeHTTP(sdk.HTTPRequest{
		Method: "POST", Pattern: "/username", CommenterKey: "k1",
		Body:    url.Values{"username": {"bob"}}.Encode(),
		Headers: map[string]string{"sec-fetch-site": "same-origin", "accept": "application/json"},
	})
	if resp.Status != 403 || strings.Contains(resp.Body, "rule 7") || strings.Contains(resp.Body, "10.0.0.9") {
		t.Fatalf("got %d %q", resp.Status, resp.Body)
	}
	if f.inserts != 0 {
		t.Fatal("user inserted")
	}
}
