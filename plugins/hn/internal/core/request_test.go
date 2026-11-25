package core_test

import (
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

func req(headers map[string]string, body string) *core.Request {
	return core.NewRequest(sdk.HTTPRequest{Method: "POST", Headers: headers, Body: body})
}

func TestSafePath(t *testing.T) {
	for p, want := range map[string]bool{
		"/x": true, "/": true, "/a?b=c": true,
		"//x": false, `/\x`: false, "http://x": false, "x": false, "": false,
		"/a\nb": false,
		"/a b":  false, "/\t/evil.example": false, "/a\x7fb": false, "/%zz": false, "/a%0d": true, "/" + strings.Repeat("a", 2048): false,
	} {
		if got := core.SafePath(p); got != want {
			t.Errorf("SafePath(%q) = %v", p, got)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for name, tc := range map[string]struct {
		h    map[string]string
		body string
		want string
	}{
		"form next":              {nil, "next=%2Fnews%3Fp%3D2", "/news?p=2"},
		"bad next, no referer":   {nil, "next=http%3A%2F%2Fx", "/"},
		"referer same-origin":    {map[string]string{"sec-fetch-site": "same-origin", "referer": "https://example.com/item/4?id=2"}, "", "/item/4?id=2"},
		"referer cross-site":     {map[string]string{"sec-fetch-site": "cross-site", "referer": "https://example.com/item/4"}, "", "/"},
		"referer without header": {map[string]string{"referer": "https://example.com/item/4"}, "", "/"},
		"form next wins":         {map[string]string{"sec-fetch-site": "same-origin", "referer": "https://example.com/a"}, "next=%2Fb", "/b"},
	} {
		if got := core.SafeNext(req(tc.h, tc.body)); got != tc.want {
			t.Errorf("%s: SafeNext = %q, want %q", name, got, tc.want)
		}
	}
}

func TestSameSite(t *testing.T) {
	for v, want := range map[string]bool{"": true, "same-origin": true, "none": true, "cross-site": false, "same-site": false} {
		h := map[string]string{}
		if v != "" {
			h["sec-fetch-site"] = v
		}
		if got := core.SameSite(req(h, "")); got != want {
			t.Errorf("SameSite(%q) = %v", v, got)
		}
	}
}

func TestSameSiteOrigin(t *testing.T) {
	for name, c := range map[string]struct {
		h    map[string]string
		want bool
	}{
		"no headers":           {nil, true},
		"foreign origin only":  {map[string]string{"origin": "https://evil.example"}, false},
		"null origin only":     {map[string]string{"origin": "null"}, false},
		"same-origin + origin": {map[string]string{"sec-fetch-site": "same-origin", "origin": "https://site.example"}, true},
		"cross-site + origin":  {map[string]string{"sec-fetch-site": "cross-site", "origin": "https://evil.example"}, false},
		"same-site + origin":   {map[string]string{"sec-fetch-site": "same-site", "origin": "https://evil.example"}, false},
	} {
		if got := core.SameSite(req(c.h, "")); got != c.want {
			t.Errorf("%s: SameSite = %v", name, got)
		}
	}
}

func TestRespond(t *testing.T) {
	r := req(map[string]string{"accept": "application/json"}, "")
	got := core.Respond(r, "/x", map[string]int{"points": 3})
	if got.Status != 200 || got.Body != `{"points":3}` || got.Headers["Cache-Control"] != "no-store" {
		t.Errorf("json: %+v", got)
	}
	got = core.Respond(req(nil, ""), "/x?m=a", nil)
	if got.Status != 303 || got.Headers["Location"] != "/x?m=a" {
		t.Errorf("redirect: %+v", got)
	}
	got = core.Respond(req(nil, ""), "//evil.example.com", nil)
	if got.Headers["Location"] != "/" {
		t.Errorf("unsafe redirect kept: %+v", got)
	}
}

func TestWithNoticeAndDefineNotice(t *testing.T) {
	if got := core.WithNotice("/a", "bad title"); got != "/a?m=bad+title" {
		t.Errorf("got %q", got)
	}
	if got := core.WithNotice("/a?p=2", "x"); got != "/a?m=x&p=2" {
		t.Errorf("got %q", got)
	}
	if got := core.WithNotice("/a?m=old&p=2#top", "new"); got != "/a?m=new&p=2#top" {
		t.Errorf("existing m and fragment: got %q", got)
	}
	if got := core.WithNotice("/a#top", "x"); got != "/a?m=x#top" {
		t.Errorf("fragment: got %q", got)
	}
	if got := core.WithNotice("/a", ""); got != "/a" {
		t.Errorf("got %q", got)
	}
	core.DefineNotice("test-code", "Text.")
	if core.NoticeText("test-code") != "Text." || core.NoticeText("unknown") != "" {
		t.Error("NoticeText")
	}
	core.DefineNotice("test-code", "Text.") // same text again: no-op
	if core.NoticeText("test-code") != "Text." {
		t.Error("NoticeText after a repeated definition")
	}
	defer func() {
		if recover() == nil {
			t.Error("conflicting DefineNotice did not panic")
		}
	}()
	core.DefineNotice("test-code", "Again.")
}
