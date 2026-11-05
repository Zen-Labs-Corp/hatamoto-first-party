package main

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const testKey = "k3y-secret-value"

// fakeFetch installs a callHost answering http.fetch with a canned response
// and returns the recorded request.
func fakeFetch(t *testing.T, resp fetchResp, hostErr error) *fetchReq {
	t.Helper()
	var got fetchReq
	callHost = func(fn string, args, out any) error {
		if fn != "http.fetch" {
			t.Fatalf("unexpected host call %q", fn)
		}
		got = args.(fetchReq)
		if hostErr != nil {
			return hostErr
		}
		*(out.(*fetchResp)) = resp
		return nil
	}
	return &got
}

const pexelsBody = `{"page":1,"next_page":"https://api.pexels.com/v1/search?page=2","photos":[
 {"id":123,"url":"https://www.pexels.com/photo/123/","photographer":"Ann Lee","alt":"A red barn","src":{"large2x":"https://images.pexels.com/photos/123/p.jpeg?w=1880","large":"https://images.pexels.com/photos/123/l.jpeg"}},
 {"id":0,"photographer":"x","src":{}},
 {"id":124,"url":"https://www.pexels.com/photo/124/","photographer":"Bob","alt":"","src":{"large":"https://images.pexels.com/photos/124/l.png"}}]}`

func TestSearchPexels(t *testing.T) {
	req := fakeFetch(t, fetchResp{Status: 200, Body: pexelsBody}, nil)
	photos, more, raw, err := searchPexels(testKey, searchParams{Query: "red barn", Orientation: "landscape", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(req.URL)
	q := u.Query()
	if u.Host != "api.pexels.com" || q.Get("query") != "red barn" || q.Get("per_page") != "30" || q.Get("page") != "2" || q.Get("orientation") != "landscape" {
		t.Fatalf("url = %s", req.URL)
	}
	if req.Headers["Authorization"] != testKey {
		t.Fatalf("headers = %v", req.Headers)
	}
	if !more || raw != 3 || len(photos) != 3 {
		t.Fatalf("more=%v raw=%d len=%d", more, raw, len(photos))
	}
	p := photos[0]
	if p.Skip || p.ID != "123" || p.Filename != "pexels-123.jpeg" || p.Alt != "A red barn" ||
		p.Credit != "Photo by Ann Lee on Pexels (Pexels License) https://www.pexels.com/photo/123/" ||
		p.Caption != "Ann Lee / Pexels" || !strings.Contains(p.ImageURL, "p.jpeg") {
		t.Fatalf("photo = %+v", p)
	}
	if !photos[1].Skip {
		t.Fatalf("incomplete result kept: %+v", photos[1])
	}
	if photos[2].Alt != "red barn" || photos[2].Filename != "pexels-124.png" {
		t.Fatalf("fallback photo = %+v", photos[2])
	}
}

func TestSearchPexelsAnyOrientationOmitted(t *testing.T) {
	req := fakeFetch(t, fetchResp{Status: 200, Body: `{"photos":[]}`}, nil)
	if _, _, _, err := searchPexels(testKey, searchParams{Query: "x", Orientation: "any", Page: 1}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(req.URL, "orientation") {
		t.Fatalf("url = %s", req.URL)
	}
}

const openverseBody = `{"page":1,"page_count":3,"results":[
 {"id":"a1","title":"Lake Michigan","creator":"VV Nincic","url":"https://live.staticflickr.com/1/a_b.jpg","foreign_landing_url":"https://www.flickr.com/photos/1/2","license":"by","license_version":"2.0","source":"flickr","filetype":null},
 {"id":"a2","title":"NC one","creator":"c","url":"https://x.example/a.jpg","license":"by-nc","license_version":"2.0"},
 {"id":"a3","title":"ND one","creator":"c","url":"https://x.example/a.jpg","license":"by-nd","license_version":"2.0"},
 {"id":"a4","title":"NC SA","creator":"c","url":"https://x.example/a.jpg","license":"by-nc-sa","license_version":"2.0"},
 {"id":"a5","title":"","creator":"","url":"https://upload.wikimedia.org/x/y.png","foreign_landing_url":"https://commons.example/5","license":"by-sa","license_version":"4.0","provider":"wikimedia","filetype":"png"},
 {"id":"a6","title":"Zero","creator":"Z","url":"https://upload.wikimedia.org/x/z.webp","foreign_landing_url":"https://commons.example/6","license":"cc0","license_version":"1.0","source":"wikimedia"},
 {"id":"a7","title":"Mark","creator":"M","url":"https://upload.wikimedia.org/x/m.gif","foreign_landing_url":"https://commons.example/7","license":"pdm","license_version":"1.0","source":"wikimedia"},
 {"id":"a8","title":"Audio","creator":"c","url":"https://x.example/a.mp3","license":"by","license_version":"2.0","filetype":"mp3"},
 {"id":"a9","title":"No ext","creator":"c","url":"https://x.example/blob","license":"by","license_version":"2.0"}]}`

func TestSearchOpenverse(t *testing.T) {
	req := fakeFetch(t, fetchResp{Status: 200, Body: openverseBody}, nil)
	photos, more, raw, err := searchOpenverse(searchParams{Query: "lake", Orientation: "portrait", Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL, "license_type=commercial,modification") || !strings.Contains(req.URL, "aspect_ratio=tall") ||
		!strings.Contains(req.URL, "page_size=20") || !strings.HasPrefix(req.URL, "https://api.openverse.org/v1/images/?q=lake") {
		t.Fatalf("url = %s", req.URL)
	}
	if len(req.Headers) != 0 {
		t.Fatalf("anonymous request sent headers %v", req.Headers)
	}
	if !more || raw != 9 || len(photos) != 9 {
		t.Fatalf("more=%v raw=%d len=%d", more, raw, len(photos))
	}
	// by-nc, by-nd, by-nc-sa, mp3 and extension-less results are dropped.
	skipped := map[int]bool{1: true, 2: true, 3: true, 7: true, 8: true}
	for i, p := range photos {
		if p.Skip != skipped[i] {
			t.Errorf("photo %d skip=%v (%q)", i, p.Skip, p.SkipReason)
		}
	}
	want := map[int][4]string{ // file, alt, credit, caption
		0: {"openverse-a1.jpg", "Lake Michigan", "Lake Michigan by VV Nincic, CC BY 2.0, https://www.flickr.com/photos/1/2", "VV Nincic / flickr"},
		4: {"openverse-a5.png", "lake", "Untitled by unknown author, CC BY-SA 4.0, https://commons.example/5", "unknown author / wikimedia"},
		5: {"openverse-a6.webp", "Zero", "Zero by Z, CC0 1.0, https://commons.example/6", "Z / wikimedia"},
		6: {"openverse-a7.gif", "Mark", "Mark by M, Public Domain Mark 1.0, https://commons.example/7", "M / wikimedia"},
	}
	for i, w := range want {
		p := photos[i]
		if got := [4]string{p.Filename, p.Alt, p.Credit, p.Caption}; got != w {
			t.Errorf("photo %d = %q, want %q", i, got, w)
		}
	}
}

func TestOpenverseLastPage(t *testing.T) {
	fakeFetch(t, fetchResp{Status: 200, Body: `{"page":3,"page_count":3,"results":[]}`}, nil)
	_, more, raw, err := searchOpenverse(searchParams{Query: "x", Page: 3})
	if err != nil || more || raw != 0 {
		t.Fatalf("more=%v raw=%d err=%v", more, raw, err)
	}
}

func TestProviderFailures(t *testing.T) {
	cases := []struct {
		name string
		resp fetchResp
		kind string
	}{
		{"401", fetchResp{Status: 401}, "auth"},
		{"403", fetchResp{Status: 403}, "auth"},
		{"429", fetchResp{Status: 429}, "rate"},
		{"500", fetchResp{Status: 500}, "status"},
		{"truncated", fetchResp{Status: 200, Body: `{"photos":[`, Truncated: true}, "truncated"},
		{"bad json", fetchResp{Status: 200, Body: `not json`}, "decode"},
		{"bad b64", fetchResp{Status: 200, BodyB64: "!!!"}, "decode"},
	}
	for _, c := range cases {
		fakeFetch(t, c.resp, nil)
		_, _, _, err := searchPexels(testKey, searchParams{Query: "x", Page: 1})
		var pe *providerError
		if !errors.As(err, &pe) || pe.Kind != c.kind {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: key in error %q", c.name, err)
		}
	}
	fakeFetch(t, fetchResp{Status: 403}, nil)
	if _, _, _, err := searchOpenverse(searchParams{Query: "x", Page: 1}); err == nil || strings.Contains(err.Error(), "API key") {
		t.Errorf("openverse 403 err = %v", err)
	}
}

func TestMissingKey(t *testing.T) {
	callHost = func(fn string, _, _ any) error { t.Fatalf("host call %q", fn); return nil }
	_, _, _, err := searchPexels("", searchParams{Query: "x", Page: 1})
	var pe *providerError
	if !errors.As(err, &pe) || pe.Kind != "key" {
		t.Fatalf("err = %v", err)
	}
}

func TestHostErrorPassesThrough(t *testing.T) {
	fakeFetch(t, fetchResp{}, &sdk.HostError{Code: sdk.ErrBlocked, Message: "host not allowed"})
	_, _, _, err := searchOpenverse(searchParams{Query: "x", Page: 1})
	if !errors.As(err, new(*sdk.HostError)) {
		t.Fatalf("err = %v", err)
	}
}

func TestBodyB64(t *testing.T) {
	fakeFetch(t, fetchResp{Status: 200, BodyB64: base64.StdEncoding.EncodeToString([]byte(`{"photos":[]}`))}, nil)
	if _, _, _, err := searchPexels(testKey, searchParams{Query: "x", Page: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestFilenameFor(t *testing.T) {
	cases := []struct{ provider, id, url, ft, want string }{
		{"pexels", "1", "https://i.example/a.JPG?x=1", "", "pexels-1.jpg"},
		{"openverse", "AB/c d", "https://i.example/a", "PNG", "openverse-ab-c-d.png"},
		{"openverse", "1", "https://i.example/a.tiff", "", "openverse-1.jpg"},
		{"openverse", "1", "https://i.example/a", ".webp", "openverse-1.webp"},
	}
	for _, c := range cases {
		if got := filenameFor(c.provider, c.id, c.url, c.ft); got != c.want {
			t.Errorf("filenameFor(%v) = %q, want %q", c, got, c.want)
		}
	}
	if got := filenameFor("openverse", strings.Repeat("a", 300), "", "gif"); len(got) != maxName || !strings.HasSuffix(got, ".gif") {
		t.Errorf("long name len=%d %q", len(got), got)
	}
}

func TestCleanText(t *testing.T) {
	if got := cleanText("  a\x00b\t\n c\u0007 ", 50); got != "ab c" {
		t.Errorf("got %q", got)
	}
	if got := cleanText("héllo wörld", 5); got != "héllo" {
		t.Errorf("got %q", got)
	}
	if got := cleanText(strings.Repeat("é", 600), maxAlt); len([]rune(got)) != maxAlt {
		t.Errorf("rune len %d", len([]rune(got)))
	}
}
