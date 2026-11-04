package main

import (
	"encoding/json"
	"slices"
	"testing"
)

func urls(refs []linkRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Kind[:1]+" "+r.URL)
	}
	return out
}

func TestExtractLinks(t *testing.T) {
	const origin = "https://example.org"
	cases := []struct {
		name    string
		attrs   string
		origin  string
		exclude []string
		want    []string
	}{
		{"anchor with quotes", `{"content":"<p><a href=\"https://example.com/a\">a</a> <a class=x href='/about#team'>b</a> <a href=/bare>c</a></p>"}`, origin,
			nil, []string{"o https://example.com/a", "i /about", "i /bare"}},
		{"nested objects and arrays", `{"a":{"b":[{"c":"<a href=\"https://example.com/deep\">x</a>"}]}}`, origin, nil, []string{"o https://example.com/deep"}},
		{"page_doc like props", `{"v":2,"nodes":[{"p":{"href":"/about","html":"<a href='https://example.org/x'>x</a>"}}]}`, origin,
			nil, []string{"i /about", "i /x"}},
		{"link keys", `{"url":"https://example.com/u","href":"/h?x=1","link":"HTTPS://Example.COM/L#f","other":"https://example.com/not"}`, origin,
			nil, []string{"i /h?x=1", "o https://example.com/L", "o https://example.com/u"}},
		{"key values that are not links", `{"url":"example.com","href":"#top","link":"mailto:a@example.org"}`, origin, nil, nil},
		{"skipped schemes", `{"t":"<a href=\"mailto:a@example.org\">m</a><a href=\"tel:+123\">t</a><a href=\"#x\">f</a><a href=\"javascript:void(0)\">j</a><a href=\"data:text/plain,hi\">d</a><a href=\"\">e</a><a href=\"ftp://example.org/f\">f</a><a href=\"relative/page\">r</a><a href=\"JavaScript:alert(1)\">j</a>"}`, origin, nil, nil},
		{"same origin absolute is internal", `{"t":"<a href=\"https://EXAMPLE.org/pricing?a=1#x\">p</a><a href=\"https://example.org\">h</a><a href=\"http://example.org/other\">other scheme</a>"}`, origin,
			nil, []string{"i /pricing?a=1", "i /", "o http://example.org/other"}},
		{"no origin: absolute stays outbound", `{"t":"<a href=\"https://example.org/pricing\">p</a><a href=\"/x\">x</a>"}`, "", nil,
			[]string{"o https://example.org/pricing", "i /x"}},
		{"protocol relative", `{"t":"<a href=\"//cdn.example.com/x\">x</a>"}`, origin, nil, []string{"o https://cdn.example.com/x"}},
		{"duplicates collapse", `{"a":"<a href=\"/a\">1</a><a href=\"/a#f\">2</a>","href":"/a","b":["<a href=\"https://example.com/z\">z</a>","<a href=\"https://example.com/z\">z</a>"]}`, origin,
			nil, []string{"i /a", "o https://example.com/z"}},
		{"entities in href", `{"t":"<a href=\"/s?a=1&amp;b=2\">s</a>"}`, origin, nil, []string{"i /s?a=1&b=2"}},
		{"exclude path and absolute prefixes", `{"t":"<a href=\"/members/x\">1</a><a href=\"https://example.com/private/y\">2</a><a href=\"https://example.org/members/z\">3</a><a href=\"/ok\">4</a><a href=\"https://example.com/public\">5</a>"}`, origin,
			[]string{"/members", "https://example.com/private"}, []string{"i /ok", "o https://example.com/public"}},
		{"exclude absolute own origin prefix", `{"t":"<a href=\"/members/x\">1</a>"}`, origin, []string{"https://example.org/members"}, nil},
		{"images are not links", `{"t":"<img src=\"/a.png\"><a href=\"/p\"><img src=\"https://example.com/i.png\"></a>","src":"/b.png","image":{"src":"https://example.com/c.png"}}`, origin, nil, []string{"i /p"}},
		{"uppercase tag and unclosed quote", `{"t":"<A HREF=\"/up\">x</A><a href=\"/broken"}`, origin, nil, []string{"i /up"}},
		{"a-like tags are not anchors", `{"t":"<abbr href=\"/no\">x</abbr><article href=\"/no2\">"}`, origin, nil, nil},
		{"not json", `not json`, origin, nil, nil},
		{"empty", ``, origin, nil, nil},
		{"scalars", `[1,true,null,"plain text"]`, origin, nil, nil},
	}
	for _, c := range cases {
		got := urls(extractLinks(json.RawMessage(c.attrs), c.origin, c.exclude))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestExtractLinksIsStable(t *testing.T) {
	attrs := json.RawMessage(`{"z":"/z","a":"<a href=\"/a\">","m":{"href":"/m","url":"/u"}}`)
	first := urls(extractLinks(attrs, "", nil))
	for range 20 {
		if got := urls(extractLinks(attrs, "", nil)); !slices.Equal(got, first) {
			t.Fatalf("order changed: %v vs %v", got, first)
		}
	}
}

func TestSiteOrigin(t *testing.T) {
	for _, c := range []struct{ url, path, want string }{
		{"https://example.org/about", "/about", "https://example.org"},
		{"https://Example.org/", "/", "https://example.org"},
		{"https://example.org/about/", "/about", "https://example.org"},
		{"https://example.org:8443/a/b", "/a/b", "https://example.org:8443"},
		{"/about", "/about", ""},
		{"", "/about", ""},
		{"https://example.org/x", "/y", ""},
		{"https://example.org/sub/about", "/about", ""},
		{"ftp://example.org/about", "/about", ""},
	} {
		if got := siteOrigin(c.url, c.path); got != c.want {
			t.Errorf("siteOrigin(%q, %q) = %q, want %q", c.url, c.path, got, c.want)
		}
	}
}
