package main

import (
	"regexp"
	"strings"
	"testing"
)

func ident(b body) func(int) string { return func(i int) string { return b.Links[i] } }

func render(t *testing.T, format, src string) (htmlOut, textOut string) {
	t.Helper()
	b, msg := parseBody(format, src)
	if msg != "" {
		t.Fatalf("parseBody(%q): %s", src, msg)
	}
	return b.html(ident(b)), b.text(ident(b))
}

var allowedMarkup = regexp.MustCompile(`^(</?(p|b|strong|i|em|ul|ol|li|h2|h3|a)>|<br>|<a href="https?://[^"<>]*">)$`)

// markupOnlyAllowed fails when the output holds a tag outside the allowlist.
func markupOnlyAllowed(t *testing.T, out string) {
	t.Helper()
	for _, tag := range regexp.MustCompile(`<[^>]*>?`).FindAllString(out, -1) {
		if !allowedMarkup.MatchString(tag) {
			t.Errorf("tag %q is outside the allowlist in %q", tag, out)
		}
	}
	if strings.Count(out, "<") != len(regexp.MustCompile(`<[^>]*>`).FindAllString(out, -1)) {
		t.Errorf("a stray < in %q", out)
	}
}

func TestBodySanitiserVectors(t *testing.T) {
	vectors := map[string]string{
		"script":        `<script>alert(1)</script>`,
		"script in p":   `<p>hi<script>alert(1)</script>there</p>`,
		"upper script":  `<SCRIPT SRC=//x.example/a.js></SCRIPT>`,
		"img onerror":   `<img src=x onerror=alert(1)>`,
		"svg":           `<svg onload=alert(1)><circle/></svg>`,
		"iframe":        `<iframe src="https://example.com"></iframe>after`,
		"style":         `<style>body{background:url(javascript:alert(1))}</style>x`,
		"nested script": `<script><script>alert(1)</script></script>y`,
		"split tag":     `<scr<script>ipt>alert(1)</script>`,
		"comment":       `<!-- <script>alert(1)</script> --><p>ok</p>`,
		"onclick":       `<a href="https://example.com/a" onclick="alert(1)" style="x">go</a>`,
		"p attrs":       `<p style="x" onmouseover="alert(1)" class="c">t</p>`,
		"object":        `<object data="x"></object><template><b>t</b></template><noscript>n</noscript>`,
		"unterminated":  `<script>alert(1)`,
		"lone lt":       `1 < 2 <b`,
		"entity":        `&lt;script&gt;alert(1)&lt;/script&gt;`,
		"quote break":   `<a href="https://example.com/&quot; onclick=&quot;alert(1)">x</a>`,
		"tick":          "<a href='https://example.com/a' onclick='x'>x</a>",
	}
	for name, src := range vectors {
		b, msg := parseBody("html", src)
		if msg != "" {
			continue // refused outright is fine too
		}
		out := b.html(ident(b))
		markupOnlyAllowed(t, out)
		for _, bad := range []string{"<script", "<img", "<svg", "<iframe", "<style", "onerror", "onclick", "onload", "onmouseover", "<object", "<template"} {
			if strings.Contains(strings.ToLower(out), bad) {
				t.Errorf("%s: output has %q: %s", name, bad, out)
			}
		}
	}
	for _, name := range []string{"script", "iframe", "style", "nested script", "comment", "object", "unterminated"} {
		b, _ := parseBody("html", vectors[name])
		if out := b.html(ident(b)); strings.Contains(out, "alert") || strings.Contains(out, "background") {
			t.Errorf("%s: dropped content leaked: %s", name, out)
		}
	}
}

func TestBodyEntitiesStayEscaped(t *testing.T) {
	h, _ := render(t, "html", `&lt;script&gt;alert(1)&lt;/script&gt; &amp; fish`)
	if h != "&lt;script&gt;alert(1)&lt;/script&gt; &amp; fish" {
		t.Errorf("got %q", h)
	}
}

func TestBodyBadLinksRefuse(t *testing.T) {
	for _, src := range []string{
		`<a href="javascript:alert(1)">x</a>`,
		`<a href="JaVaScRiPt:alert(1)">x</a>`,
		`<a href="data:text/html,x">x</a>`,
		`<a href="//example.com/x">x</a>`,
		`<a href="/relative">x</a>`,
		`<a href="https://">x</a>`,
		`<a href="https://user:pw@example.com/">x</a>`,
		`<a href="https://exa mple.com/">x</a>`,
		`<a href=" javascript:alert(1)">x</a>`,
		`<a href="&#106;avascript:alert(1)">x</a>`,
	} {
		if _, msg := parseBody("html", src); msg != "bad link" {
			t.Errorf("%s: %q, want bad link", src, msg)
		}
	}
}

func TestBodyAnchorKeepsOnlyHref(t *testing.T) {
	b, msg := parseBody("html", `<a href="https://example.com/a" onclick="x" target="_blank">go</a>`)
	if msg != "" {
		t.Fatal(msg)
	}
	if got := b.html(ident(b)); got != `<a href="https://example.com/a">go</a>` {
		t.Errorf("got %s", got)
	}
	if h, _ := render(t, "html", `<a name="top">anchor</a>`); h != "anchor" {
		t.Errorf("an a without href is dropped: %q", h)
	}
	if h, _ := render(t, "html", `<a href="https://example.com/a?x=1&amp;y=2">go</a>`); h != `<a href="https://example.com/a?x=1&amp;y=2">go</a>` {
		t.Errorf("query: %q", h)
	}
}

func TestBodyBalancesTags(t *testing.T) {
	h, _ := render(t, "html", `<p><b>unclosed`)
	if h != "<p><b>unclosed</b></p>" {
		t.Errorf("got %q", h)
	}
	h, _ = render(t, "html", `text</b></p><i>x</em>y`) // stray closers are ignored
	if h != "text<i>xy</i>" {
		t.Errorf("got %q", h)
	}
	h, _ = render(t, "html", `<ul><li>one<li>two</ul>`)
	markupOnlyAllowed(t, h)
	if strings.Count(h, "<li>") != strings.Count(h, "</li>") || strings.Count(h, "<ul>") != 1 || strings.Count(h, "</ul>") != 1 {
		t.Errorf("unbalanced: %q", h)
	}
}

func TestBodyLimits(t *testing.T) {
	anchors := func(n int) string {
		var sb strings.Builder
		for i := range n {
			sb.WriteString(`<a href="https://example.com/` + string(rune('a'+i)) + `">x</a> `)
		}
		return sb.String()
	}
	if _, msg := parseBody("html", anchors(21)); msg != "too many links" {
		t.Errorf("21 links: %q", msg)
	}
	if _, msg := parseBody("html", anchors(20)); msg != "" {
		t.Errorf("20 links: %q", msg)
	}
	var text strings.Builder
	for i := range 21 {
		text.WriteString("https://example.com/" + string(rune('a'+i)) + " ")
	}
	if _, msg := parseBody("text", text.String()); msg != "too many links" {
		t.Errorf("21 text links: %q", msg)
	}
	if _, msg := parseBody("text", strings.Repeat("a", 8001)); msg != "too long" {
		t.Errorf("long: %q", msg)
	}
	if _, msg := parseBody("text", strings.Repeat("a", 8000)); msg != "" {
		t.Errorf("8000 bytes: %q", msg)
	}
	if _, msg := parseBody("text", "  \n "); msg != "empty" {
		t.Errorf("empty: %q", msg)
	}
	if _, msg := parseBody("markdown", "x"); msg != "bad format" {
		t.Errorf("format: %q", msg)
	}
	// the same address twice is one link
	b, _ := parseBody("text", "https://example.com/a and https://example.com/a")
	if len(b.Links) != 1 {
		t.Errorf("links %v", b.Links)
	}
}

func TestBodyTextFormat(t *testing.T) {
	h, txt := render(t, "text", "Hello <b>there</b> & welcome.\nSecond line.\n\nSee https://example.com/x. Also (https://example.com/y) done!")
	want := `<p>Hello &lt;b&gt;there&lt;/b&gt; &amp; welcome.<br>Second line.</p><p>See <a href="https://example.com/x">https://example.com/x</a>. Also (<a href="https://example.com/y">https://example.com/y</a>) done!</p>`
	if h != want {
		t.Errorf("html\n got %s\nwant %s", h, want)
	}
	if !strings.Contains(txt, "See https://example.com/x. Also") || strings.Contains(txt, "(https://example.com/x)") {
		t.Errorf("text %q", txt)
	}
	if !strings.Contains(txt, "Hello <b>there</b> & welcome.\nSecond line.\n\nSee") {
		t.Errorf("text paragraphs %q", txt)
	}
}

func TestBodyHrefMapping(t *testing.T) {
	b, _ := parseBody("html", `<p><a href="https://example.com/a">our page</a> and https://example.com/a</p>`)
	track := func(i int) string { return "https://site.example/p/event-tickets/c/TOKEN?l=" + string(rune('0'+i)) }
	h := b.html(track)
	if !strings.Contains(h, `<a href="https://site.example/p/event-tickets/c/TOKEN?l=0">our page</a>`) {
		t.Errorf("html %s", h)
	}
	if txt := b.text(track); txt != "our page (https://site.example/p/event-tickets/c/TOKEN?l=0) and https://example.com/a" {
		t.Errorf("text %q", txt)
	}
	bt, _ := parseBody("text", "go to https://example.com/a now")
	if txt := bt.text(track); txt != "go to https://site.example/p/event-tickets/c/TOKEN?l=0 now" {
		t.Errorf("text link is the tracked address: %q", txt)
	}
}

func TestBodyHTMLWhitespaceAndLists(t *testing.T) {
	_, txt := render(t, "html", "<h2>Title</h2>\n<p>One\n  two</p><ul><li>a</li><li>b</li></ul><p>x<br>y</p>")
	if txt != "Title\n\nOne two\n\n- a\n- b\n\nx\ny" {
		t.Errorf("text %q", txt)
	}
}

func TestCheckLinkLowercasesTheScheme(t *testing.T) {
	for raw, want := range map[string]string{
		"HTTPS://Example.com/Path?Q=A": "https://Example.com/Path?Q=A",
		"Http://example.com/x":         "http://example.com/x",
		"https://example.com/x":        "https://example.com/x",
	} {
		if got, ok := checkLink(raw); !ok || got != want {
			t.Errorf("checkLink(%q) = %q %v, want %q", raw, got, ok, want)
		}
	}
	b, msg := parseBody("html", `<a href="HTTPS://example.com/a">go</a>`)
	if msg != "" || len(b.Links) != 1 || b.Links[0] != "https://example.com/a" {
		t.Errorf("html link: %q %+v", msg, b)
	}
}
