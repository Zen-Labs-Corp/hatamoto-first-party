package accounts

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func renderPage(t *testing.T, ctx sdk.PageContext) sdk.PageContext {
	t.Helper()
	out, err := newRegistry(nil).RenderPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func block(t *testing.T, ctx sdk.PageContext, typ string) sdk.Block {
	t.Helper()
	for _, b := range ctx.Blocks {
		if b.Type == typ {
			return b
		}
	}
	t.Fatalf("no %s block in %+v", typ, ctx.Blocks)
	return sdk.Block{}
}

var errBoom = errors.New("boom")

func TestPages(t *testing.T) {
	t.Run("userbar is first in every state", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		kit := sdk.Block{ID: "kit1", Type: "text"}
		for _, path := range []string{"/news", "/login", "/user", "/item/x"} {
			for key, want := range map[string]string{"k1": "named", "k2": "noname", "": "out"} {
				out := renderPage(t, sdk.PageContext{Path: path, CommenterKey: key, Blocks: []sdk.Block{kit}})
				// theme header contract (README "User bar and the theme header")
				if out.Blocks[0].Type != "hn_userbar" || out.Blocks[0].ID != "hn_userbar" {
					t.Fatalf("%s/%s: first block %+v", path, want, out.Blocks[0])
				}
				if path == "/news" && (len(out.Blocks) != 2 || out.Blocks[1].ID != "kit1") {
					t.Fatalf("kit block lost: %+v", out.Blocks)
				}
				c := out.Blocks[0].Content
				if c["state"] != want {
					t.Fatalf("%s: state %v, want %s", path, c["state"], want)
				}
				if c["logout_action"] != "/p/hn/logout" {
					t.Fatalf("%s: logout_action %v", want, c["logout_action"])
				}
				if _, ok := c["logout_href"]; ok {
					t.Fatalf("%s: logout_href still set", want)
				}
				if want == "named" && (c["name"] != "bob" || c["karma"] != 1 || c["user_href"] != "/user?id=bob") {
					t.Fatalf("named content: %v", c)
				}
				if want != "named" && (c["name"] != nil || c["user_href"] != nil) {
					t.Fatalf("%s content has a name: %v", want, c)
				}
			}
		}
	})

	t.Run("userbar without a key makes no host call", func(t *testing.T) {
		f := installFake(t)
		renderPage(t, sdk.PageContext{Path: "/news"})
		if len(f.host.Calls) != 0 {
			t.Fatalf("host called: %v", f.host.Calls)
		}
	})

	t.Run("login states", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		for key, want := range map[string]string{"": "out", "k2": "noname", "k1": "named"} {
			b := block(t, renderPage(t, sdk.PageContext{Path: "/login", CommenterKey: key}), "hn_login")
			c := b.Content
			if c["state"] != want || c["login_action"] != "/p/hn/login" || c["signup_action"] != "/p/hn/signup" ||
				c["logout_action"] != "/p/hn/logout" || c["email_signin_href"] != "/-/comments/signin" || c["next"] != "/login" {
				t.Fatalf("%s: %v", want, b.Content)
			}
			if _, ok := c["reset_href"]; ok {
				t.Fatalf("%s: reset_href set: %v", want, c)
			}
			if want == "named" && b.Content["name"] != "bob" {
				t.Fatalf("named: %v", b.Content)
			}
		}
	})

	t.Run("login keeps the placeholder id", func(t *testing.T) {
		installFake(t)
		out := renderPage(t, sdk.PageContext{Path: "/login", Blocks: []sdk.Block{{ID: "b7", Type: "hn_login"}}})
		if b := block(t, out, "hn_login"); b.ID != "b7" || len(out.Blocks) != 2 {
			t.Fatalf("got %+v", out.Blocks)
		}
	})

	t.Run("user page", func(t *testing.T) {
		f := installFake(t)
		bob := f.add("k1", "bob")
		bob.About, bob.Maxvisit = "hello", 33
		f.add("k2", "amy")
		view := func(key, id, m string) map[string]any {
			ctx := sdk.PageContext{Path: "/user", CommenterKey: key, Query: map[string]string{"id": id, "m": m}}
			return block(t, renderPage(t, ctx), "hn_user").Content
		}

		own := view("k1", "bob", "")
		if own["found"] != true || own["own"] != true || own["maxvisit"] != 33 || own["about_raw"] != "hello" ||
			own["created"] != "March 9, 2026" || own["next"] != "/user?id=bob" || own["comments_href"] != "/threads?id=bob" {
			t.Fatalf("own: %v", own)
		}
		for _, key := range []string{"k2", ""} {
			other := view(key, "bob", "")
			if other["found"] != true || other["own"] != false || other["comments_href"] != "/threads?id=bob" || other["submissions_href"] != "/newest?id=bob" {
				t.Fatalf("other: %v", other)
			}
			for _, k := range []string{"about_raw", "showdead", "noprocrast", "maxvisit", "minaway", "next"} {
				if _, ok := other[k]; ok {
					t.Fatalf("%s visible to %q", k, key)
				}
			}
		}
		if g := view("k1", "ghost", ""); g["found"] != false {
			t.Fatalf("ghost: %v", g)
		}
		if view("k1", "bob", "profile_saved")["notice"] != "Profile updated." || view("k1", "bob", "bogus")["notice"] != "" {
			t.Fatal("notice text wrong")
		}
	})

	t.Run("password keys on the profile", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		amy := f.add("k2", "amy")
		f.addLogin(amy, "acc-amy", "hash")
		f.logins[amy.ID].Email = "amy@example.com"
		view := func(key, id string) map[string]any {
			ctx := sdk.PageContext{Path: "/user", CommenterKey: key, Query: map[string]string{"id": id}}
			return block(t, renderPage(t, ctx), "hn_user").Content
		}
		has := func(c map[string]any, keys ...string) {
			t.Helper()
			for _, k := range keys {
				if _, ok := c[k]; !ok {
					t.Errorf("missing %s in %v", k, c)
				}
			}
		}
		none := func(c map[string]any, keys ...string) {
			t.Helper()
			for _, k := range keys {
				if _, ok := c[k]; ok {
					t.Errorf("unexpected %s in %v", k, c)
				}
			}
		}
		withPw := view("k2", "amy")
		if withPw["has_password"] != true || withPw["email"] != "amy@example.com" ||
			withPw["password_action"] != "/p/hn/password" || withPw["logout_all_action"] != "/p/hn/logout/all" {
			t.Fatalf("with password: %v", withPw)
		}
		none(withPw, "connect_action", "account", "pw_hash")
		without := view("k1", "bob")
		if without["has_password"] != false || without["connect_action"] != "/p/hn/password/connect" {
			t.Fatalf("without password: %v", without)
		}
		none(without, "email", "password_action", "logout_all_action")
		other := view("k1", "amy")
		none(other, "has_password", "email", "password_action", "connect_action", "logout_all_action")
		has(other, "submissions_href", "comments_href", "favorites_href")
	})

	t.Run("login read error offers no password forms", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		ctx := sdk.PageContext{Path: "/user", CommenterKey: "k1", Query: map[string]string{"id": "bob"}}
		real := f.host.Fns["db.query"]
		f.host.Fns["db.query"] = func(raw json.RawMessage) (any, error) {
			if strings.Contains(string(raw), "user_logins") {
				return nil, errBoom
			}
			return real(raw)
		}
		c := block(t, renderPage(t, ctx), "hn_user").Content
		if c["found"] != true || c["has_password"] != false {
			t.Fatalf("content: %v", c)
		}
		for _, k := range []string{"connect_action", "password_action", "logout_all_action", "email"} {
			if _, ok := c[k]; ok {
				t.Errorf("unexpected %s", k)
			}
		}
	})

	t.Run("host error leaves the placeholder", func(t *testing.T) {
		f := installFake(t)
		f.queryErr = errBoom
		out := renderPage(t, sdk.PageContext{Path: "/user", Query: map[string]string{"id": "bob"}, Blocks: []sdk.Block{{ID: "b1", Type: "hn_user"}}})
		if b := block(t, out, "hn_user"); b.ID != "b1" || b.Content != nil {
			t.Fatalf("got %+v", out.Blocks)
		}
	})

	t.Run("no content holds the commenter key", func(t *testing.T) {
		f := installFake(t)
		f.add("secret-key", "bob")
		for _, path := range []string{"/news", "/login", "/user"} {
			out := renderPage(t, sdk.PageContext{Path: path, CommenterKey: "secret-key", Query: map[string]string{"id": "bob"}})
			raw, _ := json.Marshal(out.Blocks)
			if strings.Contains(string(raw), "secret-key") {
				t.Fatalf("%s leaks the key: %s", path, raw)
			}
		}
	})
}

func TestPagesAboutSegments(t *testing.T) {
	seg := func(text, href string, br bool) map[string]any {
		return map[string]any{"text": text, "href": href, "br": br}
	}
	cases := []struct {
		in   string
		want []map[string]any
	}{
		{"see https://example.com/x.", []map[string]any{seg("see ", "", false), seg("https://example.com/x", "https://example.com/x", false), seg(".", "", false)}},
		{"a\nb", []map[string]any{seg("a", "", false), seg("", "", true), seg("b", "", false)}},
		{"a\r\nb", []map[string]any{seg("a", "", false), seg("", "", true), seg("b", "", false)}},
		{"javascript:alert(1)", []map[string]any{seg("javascript:alert(1)", "", false)}},
		{"(https://example.com/x)", []map[string]any{seg("(", "", false), seg("https://example.com/x", "https://example.com/x", false), seg(")", "", false)}},
		{"https://en.wikipedia.org/wiki/Foo_(bar)", []map[string]any{seg("https://en.wikipedia.org/wiki/Foo_(bar)", "https://en.wikipedia.org/wiki/Foo_(bar)", false)}},
		{"", nil},
	}
	for _, c := range cases {
		if got := aboutSegments(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("aboutSegments(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
