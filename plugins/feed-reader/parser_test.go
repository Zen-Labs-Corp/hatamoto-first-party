package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var htmlLike = regexp.MustCompile(`<[a-zA-Z/!?]`)

func TestParseRSS2(t *testing.T) {
	p, err := parseFeed(fixture(t, "rss2.xml"), "https://example.com/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Example News" || p.SiteURL != "https://example.com/" {
		t.Fatalf("feed %q %q", p.Title, p.SiteURL)
	}
	// The javascript: item is dropped.
	if len(p.Items) != 4 {
		t.Fatalf("items %d: %+v", len(p.Items), p.Items)
	}
	a := p.Items[0]
	if a.GUID != "post-1" || a.Link != "https://example.com/posts/1" {
		t.Fatalf("item 0 %+v", a)
	}
	if a.Title != "Tom & Jerry return" {
		t.Fatalf("title %q", a.Title)
	}
	if a.Excerpt != "Hello world . Second paragraph & more." {
		t.Fatalf("excerpt %q", a.Excerpt)
	}
	if want := time.Date(2026, 11, 3, 8, 30, 0, 0, time.UTC); !a.Published.Equal(want) {
		t.Fatalf("published %v", a.Published)
	}
	b := p.Items[1]
	if b.Excerpt != "Escaped markup here" {
		t.Fatalf("escaped html excerpt %q (description must win over content:encoded)", b.Excerpt)
	}
	if want := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC); !b.Published.Equal(want) {
		t.Fatalf("EST date %v", b.Published)
	}
	c := p.Items[2]
	if !c.Published.IsZero() || c.GUID != "https://example.com/posts/3" || c.Excerpt != "Only full content is available." {
		t.Fatalf("no date/guid item %+v", c)
	}
	if want := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC); !p.Items[3].Published.Equal(want) {
		t.Fatalf("dc:date %v", p.Items[3].Published)
	}
	for _, it := range p.Items {
		if htmlLike.MatchString(it.Excerpt) || htmlLike.MatchString(it.Title) {
			t.Fatalf("markup left in %+v", it)
		}
		if !strings.HasPrefix(it.Link, "https://") {
			t.Fatalf("link %q", it.Link)
		}
	}
}

func TestParseAtom(t *testing.T) {
	p, err := parseFeed(fixture(t, "atom.xml"), "https://example.org/blog/atom.xml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Example Atom" || p.SiteURL != "https://example.org/blog/" {
		t.Fatalf("feed %q %q", p.Title, p.SiteURL)
	}
	if len(p.Items) != 3 {
		t.Fatalf("items %d: %+v", len(p.Items), p.Items)
	}
	a := p.Items[0]
	if a.Link != "https://example.org/blog/relative-post" || a.GUID != "urn:example:1" {
		t.Fatalf("relative link %+v", a)
	}
	if a.Title != "Relative & linked" || a.Excerpt != "Short summary of the first entry." {
		t.Fatalf("title/excerpt %q / %q", a.Title, a.Excerpt)
	}
	if want := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC); !a.Published.Equal(want) {
		t.Fatalf("published wins over updated: %v", a.Published)
	}
	b := p.Items[1]
	if b.Excerpt != "Inline xhtml content." {
		t.Fatalf("xhtml excerpt %q", b.Excerpt)
	}
	if want := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC); !b.Published.Equal(want) {
		t.Fatalf("updated fallback %v", b.Published)
	}
	c := p.Items[2]
	if c.GUID != "https://example.org/blog/no-id" || !c.Published.IsZero() {
		t.Fatalf("no id / bad date %+v", c)
	}
}

func TestParseLatin1DeclaredEncoding(t *testing.T) {
	p, err := parseFeed(fixture(t, "rss-latin1.xml"), "https://example.net/feed")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Café News" || len(p.Items) != 1 {
		t.Fatalf("%q %+v", p.Title, p.Items)
	}
	it := p.Items[0]
	if it.Title != "Crème brûlée € 5" {
		t.Fatalf("title %q", it.Title)
	}
	if it.Excerpt != "à la carte “quoted”" {
		t.Fatalf("excerpt %q", it.Excerpt)
	}
}

func TestParseRDF(t *testing.T) {
	doc := `<?xml version="1.0"?><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel><title>RDF</title><link>https://example.com/</link></channel>
<item><title>One</title><link>https://example.com/1</link><description>First</description><dc:date>2026-01-02T03:04:05Z</dc:date></item></rdf:RDF>`
	p, err := parseFeed([]byte(doc), "https://example.com/rdf")
	if err != nil || len(p.Items) != 1 || p.Items[0].Title != "One" || p.Title != "RDF" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"malformed":  `<rss><channel><item><title>x</title><link>https://example.com/`,
		"not a feed": `<html><body>hello</body></html>`,
		"plain text": `hello`,
		"empty":      ``,
	}
	for name, doc := range cases {
		if _, err := parseFeed([]byte(doc), "https://example.com/"); !errors.Is(err, errInvalidFeed) {
			t.Fatalf("%s: err %v", name, err)
		}
	}
	_, err := parseFeed([]byte(`<?xml version="1.0" encoding="Shift_JIS"?><rss><channel></channel></rss>`), "https://example.com/")
	if !errors.Is(err, errUnsupportedCharset) {
		t.Fatalf("shift_jis: err %v", err)
	}
}

func TestParseCapsItems(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<rss><channel><title>Big</title>`)
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&sb, `<item><title>T%d</title><link>https://example.com/%d</link></item>`, i, i)
	}
	sb.WriteString(`</channel></rss>`)
	p, err := parseFeed([]byte(sb.String()), "https://example.com/")
	if err != nil || len(p.Items) != 200 || p.Items[199].Title != "T199" {
		t.Fatalf("%d items, err %v", len(p.Items), err)
	}
}

func TestParseDoesNotExpandEntities(t *testing.T) {
	doc := `<?xml version="1.0"?><!DOCTYPE rss [<!ENTITY a "AAAAAAAAAA"><!ENTITY b "&a;&a;&a;&a;&a;&a;">]>
<rss><channel><item><title>&b;</title><link>https://example.com/1</link></item></channel></rss>`
	p, err := parseFeed([]byte(doc), "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) == 1 && strings.Contains(p.Items[0].Title, "AAAA") {
		t.Fatalf("custom entity expanded: %q", p.Items[0].Title)
	}
}

func TestLongGUIDIsHashed(t *testing.T) {
	doc := `<rss><channel><item><title>x</title><link>https://example.com/1</link><guid>` + strings.Repeat("g", 5000) + `</guid></item></channel></rss>`
	p, _ := parseFeed([]byte(doc), "https://example.com/")
	if len(p.Items) != 1 || len(p.Items[0].GUID) != 64 {
		t.Fatalf("%+v", p.Items)
	}
}

func TestExcerptText(t *testing.T) {
	long := strings.Repeat("é", 400)
	got := excerptText("<p>" + long + "</p>")
	if !utf8.ValidString(got) || got != strings.Repeat("é", 280)+"…" {
		t.Fatalf("long excerpt: %d runes", utf8.RuneCountInString(got))
	}
	short := strings.Repeat("a", 280)
	if excerptText(short) != short {
		t.Fatal("exactly 280 runes must not be cut")
	}
	for in, want := range map[string]string{
		"  a \n\t b  ":                 "a b",
		"1 < 2 and 3 > 2":              "1 < 2 and 3 > 2",
		"<p>a</p><p>b</p>":             "a b",
		"x<script>evil()</script>y":    "x y",
		"x<SCRIPT>evil()":              "x",
		"&lt;b&gt;bold&lt;/b&gt; text": "bold text",
		"&amp;lt;i&amp;gt;x":           "&lt;i&gt;x", // double-escaped stays text
		"unterminated <a href=":        "unterminated",
		"a&nbsp;b":                     "a b",
		"":                             "",
	} {
		if got := excerptText(in); got != want {
			t.Fatalf("excerptText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDate(t *testing.T) {
	u := func(y int, m time.Month, d, h, mi, s int) time.Time { return time.Date(y, m, d, h, mi, s, 0, time.UTC) }
	for in, want := range map[string]time.Time{
		"Tue, 03 Nov 2026 09:30:00 +0100":     u(2026, 11, 3, 8, 30, 0),
		"Tue, 3 Nov 2026 09:30:00 GMT":        u(2026, 11, 3, 9, 30, 0),
		"Tue, 03 Nov 2026 09:30:00 UT":        u(2026, 11, 3, 9, 30, 0),
		"Tue, 03 Nov 2026 09:30:00 PST":       u(2026, 11, 3, 17, 30, 0),
		"Tue, 03 Nov 2026 09:30:00 EDT":       u(2026, 11, 3, 13, 30, 0),
		"03 Nov 26 09:30 +0000":               u(2026, 11, 3, 9, 30, 0),
		"Tue, 03 Nov 26 09:30:00 -0500":       u(2026, 11, 3, 14, 30, 0),
		"3 Nov 2026 09:30 GMT":                u(2026, 11, 3, 9, 30, 0),
		"2026-11-03T09:30:00Z":                u(2026, 11, 3, 9, 30, 0),
		"2026-11-03T09:30:00.123+02:00":       time.Date(2026, 11, 3, 7, 30, 0, 123000000, time.UTC),
		"2026-11-03T09:30:00":                 u(2026, 11, 3, 9, 30, 0),
		"2026-11-03":                          u(2026, 11, 3, 0, 0, 0),
		"  Tue,  03 Nov 2026   09:30:00 GMT ": u(2026, 11, 3, 9, 30, 0),
	} {
		if got := parseDate(in); !got.Equal(want) {
			t.Fatalf("parseDate(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "yesterday", "not a date", "32 Nov 2026"} {
		if got := parseDate(in); !got.IsZero() {
			t.Fatalf("parseDate(%q) = %v, want zero", in, got)
		}
	}
}

func TestResolveLinkOnlyHTTP(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/a":     "https://example.com/a",
		"http://example.com/a":      "http://example.com/a",
		"/rel":                      "https://example.com/rel",
		"javascript:alert(1)":       "",
		"data:text/html,x":          "",
		"ftp://example.com/a":       "",
		"mailto:a@example.com":      "",
		"https://u:p@example.com/a": "",
		"":                          "",
	} {
		base, _ := parseFeedBase("https://example.com/feed")
		if got := resolveLink(in, base); got != want {
			t.Fatalf("resolveLink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCharsetReader(t *testing.T) {
	for _, l := range []string{"utf-8", "US-ASCII", "ISO-8859-1", "windows-1252", "Latin1"} {
		if _, err := charsetReader(l, strings.NewReader("x")); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
	}
	for _, l := range []string{"shift_jis", "utf-16", "gb2312", ""} {
		if _, err := charsetReader(l, strings.NewReader("x")); !errors.Is(err, errUnsupportedCharset) {
			t.Fatalf("%s: %v", l, err)
		}
	}
}

func parseFeedBase(raw string) (*url.URL, error) { return url.Parse(raw) }

func TestStripTagsIsLinear(t *testing.T) {
	in := strings.Repeat("<style></style>", 70000) + "<SCRIPT>x</SCRIPT>end"
	start := time.Now()
	got := stripTags(in)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("stripTags took %v on 1 MiB", d)
	}
	if strings.TrimSpace(got) != "end" {
		t.Fatalf("got %q", got[len(got)-10:])
	}
}
