package comments

import (
	"strings"
	"testing"
)

// shape renders blocks compactly: kind[part|part], a part is its text with
// marks (e em, s strong, c code, b br) and "->href" for a link.
func shape(bs []map[string]any) string {
	var out []string
	for _, b := range bs {
		kind := "p"
		if b["pre"] == true {
			kind = "pre"
		} else if b["quote"] == true {
			kind = "quote"
		}
		var ps []string
		for _, p := range b["parts"].([]map[string]any) {
			s := p["text"].(string)
			for _, m := range []struct{ k, c string }{{"em", "e"}, {"strong", "s"}, {"code", "c"}, {"br", "b"}} {
				if p[m.k] == true {
					s += "#" + m.c
				}
			}
			if h := p["href"].(string); h != "" {
				s += "->" + h
			}
			ps = append(ps, s)
		}
		out = append(out, kind+"["+strings.Join(ps, "|")+"]")
	}
	return strings.Join(out, " ")
}

func TestBodyBlocks(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"two paragraphs", "<p>a</p><p>b</p>", "p[a] p[b]"},
		{"marks", "<p>x <em>y</em> <strong>z</strong> <code>c</code></p>", "p[x |y#e| |z#s| |c#c]"},
		{"link", `<p><a href="https://e.com/a?b=1&amp;c=2" rel="ugc nofollow noopener">l</a></p>`, "p[l->https://e.com/a?b=1&c=2]"},
		{"javascript link", `<a href="javascript:alert(1)">x</a>`, "p[x]"},
		{"relative link", `<a href="/x">x</a>`, "p[x]"},
		{"pre", "<pre><code>a &lt;b&gt;</code></pre>", "pre[a <b>]"},
		{"quote", "<blockquote><p>q</p></blockquote>", "quote[q]"},
		{"quote paragraphs", "<blockquote><p>q</p><p>r</p></blockquote>", "quote[q|#b|r]"},
		{"br", "line<br>two", "p[line|#b|two]"},
		{"plain", "just text", "p[just text]"},
		{"whitespace between blocks", "<p>a</p>\n<p>b</p>", "p[a] p[b]"},
		{"script", "<script>x</script>", "p[x]"},
		{"unknown tag", "<p>a <u>b</u></p>", "p[a |b]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shape(bodyBlocks(c.in)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}

	t.Run("empty is a non-nil empty slice", func(t *testing.T) {
		if got := bodyBlocks(""); got == nil || len(got) != 0 {
			t.Errorf("got %#v", got)
		}
	})

	t.Run("caps", func(t *testing.T) {
		got := bodyBlocks(strings.Repeat("<p>x</p>", 5000))
		if len(got) != maxBlocks {
			t.Errorf("%d blocks, want %d", len(got), maxBlocks)
		}
		got = bodyBlocks("<p>" + strings.Repeat("a<br>", 5000) + "</p>")
		n := 0
		for _, b := range got {
			n += len(b["parts"].([]map[string]any))
		}
		if n != maxParts {
			t.Errorf("%d parts, want %d", n, maxParts)
		}
	})
}

func TestPageBodyBudget(t *testing.T) {
	heavy := strings.Repeat("<em>a</em>", maxParts)
	r := newRegistry(nil)
	vv := newVoteView(nil, nil)
	total, last := 0, 0
	for i := range 20 {
		out := row(r, rowIn{ID: cid(i + 1), Author: "ann", Body: heavy}, vv, "/")
		last = 0
		for _, b := range out["body"].([]map[string]any) {
			last += len(b["parts"].([]map[string]any))
		}
		total += last
	}
	if total > pageParts || total < pageParts-maxParts || last != 0 {
		t.Fatalf("total parts %d (last row %d), want just under %d with the late rows cut", total, last, pageParts)
	}
}
