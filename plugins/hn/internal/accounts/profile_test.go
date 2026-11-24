package accounts

import (
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

func saveProfile(f *fakeUsers, key string, v url.Values) sdk.HTTPResponse {
	v.Set("next", "/user?id=bob")
	return post(newRegistry(nil), "/profile", key, v.Encode())
}

func TestProfile(t *testing.T) {
	t.Run("about is capped on a rune boundary", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		about := strings.Repeat("a", 1999) + "é" + "tail" // é spans bytes 1999-2000
		resp := saveProfile(f, "k1", url.Values{"about": {about}})
		got := f.byKey["k1"].About
		if len(got) > 2000 || !utf8.ValidString(got) || got != strings.Repeat("a", 1999) {
			t.Fatalf("stored %d bytes valid=%v", len(got), utf8.ValidString(got))
		}
		if resp.Status != 303 || resp.Headers["Location"] != "/user?id=bob&m=profile_saved" {
			t.Fatalf("got %+v", resp)
		}
	})

	t.Run("about is stored as text", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		saveProfile(f, "k1", url.Values{"about": {"<b>x</b>\x00"}})
		if got := f.byKey["k1"].About; got != "<b>x</b>" {
			t.Fatalf("stored %q", got)
		}
	})

	t.Run("booleans and clamps", func(t *testing.T) {
		cases := []struct {
			name       string
			form       url.Values
			sd, np     bool
			maxv, minA int
		}{
			{"yes no", url.Values{"showdead": {"yes"}, "noprocrast": {"no"}, "maxvisit": {"30"}, "minaway": {"60"}}, true, false, 30, 60},
			{"low", url.Values{"maxvisit": {"0"}, "minaway": {"0"}}, false, false, 1, 1},
			{"high", url.Values{"maxvisit": {"500"}, "minaway": {"99999"}}, false, false, 120, 1440},
			{"unparsable keeps", url.Values{"maxvisit": {"abc"}, "minaway": {""}}, false, false, 20, 180},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				f := installFake(t)
				u := f.add("k1", "bob")
				saveProfile(f, "k1", c.form)
				if u.Showdead != c.sd || u.Noprocrast != c.np || u.Maxvisit != c.maxv || u.Minaway != c.minA {
					t.Fatalf("got %+v", u)
				}
			})
		}
	})

	t.Run("set_viewer follows showdead", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		saveProfile(f, "k1", url.Values{"showdead": {"yes"}})
		if len(f.setViewer) != 1 || !f.setViewer[0].ShowDead || f.setViewer[0].CommenterKey != "k1" {
			t.Fatalf("set_viewer calls: %+v", f.setViewer)
		}
	})

	t.Run("set_viewer unavailable still saves", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		f.setViewerErr = &sdk.HostError{Code: sdk.ErrUnavailable, Message: "comments.set_viewer is not available"}
		resp := saveProfile(f, "k1", url.Values{"showdead": {"yes"}, "about": {"hi"}})
		if !f.byKey["k1"].Showdead || f.byKey["k1"].About != "hi" {
			t.Fatalf("not saved: %+v", f.byKey["k1"])
		}
		if resp.Status != 303 || resp.Headers["Location"] != "/user?id=bob&m=profile_saved" {
			t.Fatalf("got %+v", resp)
		}
	})

	for name, key := range map[string]string{"signed out": "", "no row": "ghost"} {
		t.Run(name, func(t *testing.T) {
			f := installFake(t)
			f.add("k1", "bob")
			resp := saveProfile(f, key, url.Values{"about": {"x"}})
			if resp.Headers["Location"] != "/login?m=signin_first" || f.updates != 0 || len(f.setViewer) != 0 {
				t.Fatalf("got %+v updates=%d viewer=%d", resp, f.updates, len(f.setViewer))
			}
		})
	}
}

func TestCapAboutInvalidUTF8(t *testing.T) {
	long := "\xff" + strings.Repeat("a", 3000)
	if got := capAbout(long); len(got) != maxAbout || !utf8.ValidString(got) {
		t.Fatalf("long: len %d valid %v", len(got), utf8.ValidString(got))
	}
	short := strings.Repeat("\xff", 900) + "ok"
	if got := capAbout(short); got != "ok" {
		t.Fatalf("short: %q", got)
	}
}
