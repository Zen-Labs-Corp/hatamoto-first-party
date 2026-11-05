package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const goldenBody = `<h1>Title &amp; more</h1>
<p>Intro with a <a href="https://example.org/a?x=1&amp;y=>2" title="a > b">link</a> and an entity &copy; here.<br>
Next line after a break.</p>

<h2 id="s2">Section</h2>
<ul><li>One <strong>bold</strong></li><li>Two</li></ul>
<table><tr><td>A</td><td>B</td></tr></table>
<div class="x"><div class="y"><p>Nested <em>text</em></p></div></div>
<blockquote><p>Quote</p></blockquote>
<script>if (a < b && c > d) { x = "</p>"; }</script>
<p>The end.</p>`

// noCutTag reports whether no tag in the segment is cut short, quotes
// honoured.
func noCutTag(seg string) bool {
	for _, s := range scanHTML(seg) {
		if s.markup && !s.code && !strings.HasSuffix(s.text, ">") {
			return false
		}
	}
	return true
}

func TestSplitHTMLRoundTrip(t *testing.T) {
	for _, max := range []int{1, 10, 40, 64, 120, 400, 5000} {
		segs := splitHTML(goldenBody, max)
		if joinSegs(segs) != goldenBody {
			t.Fatalf("max %d: join differs", max)
		}
		for _, s := range segs {
			if !noCutTag(s) {
				t.Fatalf("max %d: segment cuts a tag: %q", max, s)
			}
		}
	}
}

func TestSplitHTMLPrefersBlockBoundaries(t *testing.T) {
	body := "<p>" + strings.Repeat("a", 30) + "</p><p>" + strings.Repeat("b", 30) + "</p><p>" + strings.Repeat("c", 30) + "</p>"
	segs := splitHTML(body, 80)
	if len(segs) != 2 || !strings.HasSuffix(segs[0], "</p>") || len(segs[0]) > 80 {
		t.Fatalf("segments %q", segs)
	}
	for _, s := range splitHTML(goldenBody, 150) {
		if len(s) > 150 {
			t.Fatalf("segment of %d bytes", len(s))
		}
	}
}

func TestSplitHTMLLongParagraph(t *testing.T) {
	words := strings.Repeat(`word <a href="/some/long/path?with=query&and=more">link</a> `, 60)
	body := "<p>" + words + "</p>"
	segs := splitHTML(body, 200)
	if joinSegs(segs) != body || len(segs) < 5 {
		t.Fatalf("%d segments", len(segs))
	}
	for _, s := range segs {
		if len(s) > 200 || !noCutTag(s) {
			t.Fatalf("bad segment (%d bytes): %q", len(s), s)
		}
	}
	// Text with no spaces and no tags is cut on a rune boundary.
	cjk := strings.Repeat("日本語", 50)
	segs = splitHTML(cjk, 20)
	if joinSegs(segs) != cjk {
		t.Fatal("cjk join differs")
	}
	for _, s := range segs {
		if len(s) > 20 || strings.ContainsRune(s, '\uFFFD') {
			t.Fatalf("bad cjk segment %q", s)
		}
	}
}

func TestSplitHTMLHugeTagKeptWhole(t *testing.T) {
	tag := `<a href="` + strings.Repeat("x", 100) + `">`
	segs := splitHTML("<p>a</p>"+tag+"t</a>", 50)
	if joinSegs(segs) != "<p>a</p>"+tag+"t</a>" {
		t.Fatal("join differs")
	}
	for _, s := range segs {
		if !noCutTag(s) {
			t.Fatalf("cut tag: %q", s)
		}
	}
}

func TestSplitHTMLEmptyAndSmall(t *testing.T) {
	if got := splitHTML("", 100); len(got) != 0 || joinSegs(got) != "" {
		t.Fatalf("got %q", got)
	}
	if got := splitHTML("<p>x</p>", 100); len(got) != 1 || got[0] != "<p>x</p>" {
		t.Fatalf("got %q", got)
	}
}

func TestLimitsFor(t *testing.T) {
	if l := limitsFor("deepl"); l != (limits{50, 100 << 10, 20 << 10}) {
		t.Fatalf("%+v", l)
	}
	if l := limitsFor("google"); l != (limits{100, 60 << 10, 8 << 10}) {
		t.Fatalf("%+v", l)
	}
}

func TestFirstBatchCountLimit(t *testing.T) {
	l := limits{MaxCount: 5, MaxRequestBytes: 1 << 20}
	units := [][]string{{"a", "b"}, {"c", "d"}, {"e"}, {"f"}}
	n, err := firstBatch(units, l)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// A unit is never split: the third unit has 2 texts, which exceed 5.
	n, err = firstBatch([][]string{{"a", "b"}, {"c", "d"}, {"e", "f"}}, l)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestFirstBatchByteLimitUsesJSONSize(t *testing.T) {
	// Quotes and newlines grow under JSON escaping: 40 raw bytes become 80+.
	esc := strings.Repeat("\"\n", 20)
	raw, _ := json.Marshal([]string{esc, esc})
	if len(raw) <= 2*len(esc) {
		t.Fatal("test text does not grow")
	}
	l := limits{MaxCount: 50, MaxRequestBytes: len(raw) - 1}
	n, err := firstBatch([][]string{{esc}, {esc}}, l)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	l.MaxRequestBytes = len(raw)
	n, err = firstBatch([][]string{{esc}, {esc}, {esc}}, l)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestFirstBatchOversizedFirstUnit(t *testing.T) {
	l := limits{MaxCount: 2, MaxRequestBytes: 100}
	if _, err := firstBatch([][]string{{strings.Repeat("x", 200)}}, l); !errors.Is(err, ErrUnitTooLarge) {
		t.Fatalf("err=%v", err)
	}
	if _, err := firstBatch([][]string{{"a", "b", "c"}}, l); !errors.Is(err, ErrUnitTooLarge) {
		t.Fatalf("count: err=%v", err)
	}
	// A later oversized unit just ends the batch.
	n, err := firstBatch([][]string{{"a"}, {strings.Repeat("x", 200)}}, l)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestFirstBatchEmpty(t *testing.T) {
	if n, err := firstBatch(nil, limitsFor("deepl")); n != 0 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
