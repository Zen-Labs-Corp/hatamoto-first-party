package main

import (
	"strings"
	"testing"
)

func wrap(i, t string) string {
	return `<span translate="no" data-hm-g="` + i + `">` + t + `</span>`
}

func TestProtectWrapsTermInParagraph(t *testing.T) {
	g := []glossaryEntry{{"Hatamoto", ""}}
	out, n := protect(`<p>Use hatamoto today.</p>`, g)
	want := `<p>Use ` + wrap("0", "Hatamoto") + ` today.</p>`
	if out != want || n != 1 {
		t.Fatalf("got %q (%d)", out, n)
	}
}

func TestProtectLeavesMarkupAlone(t *testing.T) {
	g := []glossaryEntry{{"Hatamoto", ""}, {"page", ""}}
	for _, in := range []string{
		`<a href="https://example.org/Hatamoto">x</a>`,
		`<img alt="Hatamoto logo" src="/Hatamoto.png">`,
		`<a title='a > Hatamoto'>x</a>`,
		`<script>var s = "Hatamoto";</script>`,
		`<SCRIPT type="x">Hatamoto</SCRIPT>`,
		`<style>.Hatamoto { color: red }</style>`,
		`<!-- Hatamoto page -->`,
		`<p class="page">x</p>`,
	} {
		out, n := protect(in, g)
		if out != in || n != 0 {
			t.Fatalf("%q changed to %q (%d)", in, out, n)
		}
	}
}

func TestProtectResumesAfterScriptAndComment(t *testing.T) {
	g := []glossaryEntry{{"Hatamoto", ""}}
	out, n := protect(`<script>Hatamoto</script><!-- Hatamoto --><p>Hatamoto</p>`, g)
	want := `<script>Hatamoto</script><!-- Hatamoto --><p>` + wrap("0", "Hatamoto") + `</p>`
	if out != want || n != 1 {
		t.Fatalf("got %q (%d)", out, n)
	}
}

func TestProtectLongestWinsAndNoOverlap(t *testing.T) {
	g := []glossaryEntry{{"Hatamoto", ""}, {"Hatamoto Cloud", "Hatamoto Cloud"}}
	out, n := protect(`Hatamoto Cloud and Hatamoto`, g)
	want := wrap("1", "Hatamoto Cloud") + ` and ` + wrap("0", "Hatamoto")
	if out != want || n != 2 {
		t.Fatalf("got %q (%d)", out, n)
	}
}

func TestProtectWordBoundary(t *testing.T) {
	g := []glossaryEntry{{"Go", ""}}
	for _, in := range []string{"Gopher", "Mango", "Go2", "_Go"} {
		if out, n := protect(in, g); out != in || n != 0 {
			t.Fatalf("%q matched: %q", in, out)
		}
	}
	if _, n := protect("Learn Go, now", g); n != 1 {
		t.Fatal("Go not matched at a boundary")
	}
}

func TestProtectSkipsEntities(t *testing.T) {
	g := []glossaryEntry{{"amp", ""}, {"nbsp", ""}}
	in := `<p>a &amp; b&nbsp;c &#38; d</p>`
	if out, n := protect(in, g); out != in || n != 0 {
		t.Fatalf("entity changed: %q", out)
	}
}

func TestProtectEmptyGlossaryUnchanged(t *testing.T) {
	in := `<p>Hatamoto</p>`
	if out, n := protect(in, nil); out != in || n != 0 {
		t.Fatal("changed")
	}
	if out, n := protect(in, []glossaryEntry{{"", "x"}}); out != in || n != 0 {
		t.Fatal("empty source matched")
	}
}

func TestProtectTargetReplacesTerm(t *testing.T) {
	g := []glossaryEntry{{"cms", "CMS"}}
	out, _ := protect(`<p>my cms</p>`, g)
	if out != `<p>my `+wrap("0", "CMS")+`</p>` {
		t.Fatalf("got %q", out)
	}
}

func TestProtectUnicodeTerms(t *testing.T) {
	g := []glossaryEntry{{"Café Zürich", ""}, {"日本", "Nihon"}}
	out, n := protect(`<p>Im café zürich und 日本 !</p>`, g)
	want := `<p>Im ` + wrap("0", "Café Zürich") + ` und ` + wrap("1", "Nihon") + ` !</p>`
	if out != want || n != 2 {
		t.Fatalf("got %q (%d)", out, n)
	}
	back, lost := restore(out, g)
	if back != `<p>Im Café Zürich und Nihon !</p>` || lost != 0 {
		t.Fatalf("restore %q", back)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	g := []glossaryEntry{{"Hatamoto", ""}, {"cms", "CMS"}}
	in := `<h1>The cms Hatamoto</h1><p><a href="/x">Hatamoto</a> rocks</p>`
	prot, n := protect(in, g)
	if n != 3 {
		t.Fatalf("n=%d", n)
	}
	out, lost := restore(prot, g)
	if lost != 0 || out != `<h1>The CMS Hatamoto</h1><p><a href="/x">Hatamoto</a> rocks</p>` {
		t.Fatalf("got %q (%d)", out, lost)
	}
}

func TestRestoreTolerant(t *testing.T) {
	g := []glossaryEntry{{"Hatamoto", ""}, {"cms", "CMS"}}
	for _, in := range []string{
		`<span data-hm-g="0" translate="no">Hatamoto-xx</span>`,
		`<span  translate='no'  data-hm-g='0' class="notranslate">Hatamoto</span>`,
		`<SPAN data-hm-g = "0">Hatamoto</SPAN>`,
		`<span data-hm-g=0 translate="no">anything <b>here</b></span>`,
	} {
		out, lost := restore("A "+in+" B", g)
		if out != "A Hatamoto B" || lost != 0 {
			t.Fatalf("%q -> %q (%d)", in, out, lost)
		}
	}
	// Other spans are untouched.
	other := `<span class="x" data-other="data-hm-g">keep</span>`
	if out, _ := restore(other, g); out != other {
		t.Fatalf("plain span changed: %q", out)
	}
	if out, _ := restore(`<span data-hm-g="1">x</span>`, g); out != "CMS" {
		t.Fatalf("got %q", out)
	}
}

func TestRestoreNeverLeavesWrapper(t *testing.T) {
	g := []glossaryEntry{{"Hatamoto", ""}}
	for _, in := range []string{
		`<span translate="no" data-hm-g="0">Hatamoto`,                    // closing tag lost
		`<span data-hm-g="9">unknown</span>`,                             // bad index
		`<span data-hm-g="0"><span data-hm-g="0">Hatamoto</span></span>`, // nested
		`<span data-hm-g="x">odd</span>`,                                 // not a number
	} {
		out, _ := restore(in, g)
		if strings.Contains(out, "data-hm-g") || strings.Contains(out, "<span") {
			t.Fatalf("%q left a wrapper: %q", in, out)
		}
	}
	if _, lost := restore(`<span data-hm-g="0">Hatamoto`, g); lost != 1 {
		t.Fatalf("lost=%d", lost)
	}
	if out, _ := restore(`<span data-hm-g="9">unknown</span>`, g); out != "unknown" {
		t.Fatalf("got %q", out)
	}
}

func TestUnwrapCountsFound(t *testing.T) {
	g := []glossaryEntry{{"a", ""}}
	_, found, broken := unwrap(wrap("0", "a")+" "+wrap("0", "a"), g)
	if found != 2 || broken != 0 {
		t.Fatalf("found=%d broken=%d", found, broken)
	}
}

func TestPlainText(t *testing.T) {
	if got := plainText(`<b>Tom &amp; Jerry</b> &lt;3`); got != "Tom & Jerry <3" {
		t.Fatalf("got %q", got)
	}
}
