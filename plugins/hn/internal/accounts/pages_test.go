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
			if b.Content["state"] != want || b.Content["signin_action"] != "/-/comments/signin" {
				t.Fatalf("%s: %v", want, b.Content)
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
			if other["found"] != true || other["own"] != false {
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
		{"", nil},
	}
	for _, c := range cases {
		if got := aboutSegments(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("aboutSegments(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
