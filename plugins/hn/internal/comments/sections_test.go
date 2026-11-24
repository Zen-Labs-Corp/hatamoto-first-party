package comments

import (
	"bytes"
	"encoding/json"
	"html/template"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func parseSections(t *testing.T) *template.Template {
	t.Helper()
	return template.Must(template.ParseFiles("../../partials/style.html", "../../sections/hn_threads.html"))
}

// renderBlock renders a block like the host: its content goes through JSON first.
func renderBlock(t *testing.T, tpl *template.Template, b sdk.Block) string {
	t.Helper()
	raw, err := json.Marshal(b.Content)
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "section-"+b.Type, map[string]any{"Content": c}); err != nil {
		t.Fatalf("%s: %v", b.Type, err)
	}
	return buf.String()
}

// threadsHTML renders the hn_threads block of a page.
func threadsHTML(t *testing.T, ctx sdk.PageContext) string {
	t.Helper()
	tpl := parseSections(t)
	for _, b := range ctx.Blocks {
		if b.Type == "hn_threads" {
			return renderBlock(t, tpl, b)
		}
	}
	t.Fatalf("no hn_threads block in %+v", ctx.Blocks)
	return ""
}

func page(t *testing.T, path, key string, q map[string]string, blocks ...sdk.Block) sdk.PageContext {
	t.Helper()
	return renderPage(t, sdk.PageContext{Path: path, CommenterKey: key, Query: q, Blocks: blocks})
}

func TestThreadsPageSections(t *testing.T) {
	t.Run("newcomments rows", func(t *testing.T) {
		f, _, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		f.comment(2, bob, 2).Parent = cid(1)
		out := threadsHTML(t, page(t, "/newcomments", "", nil))
		for _, want := range []string{
			`<a href="/user?id=bob">bob</a>`,
			`on: <a href="/item/7">Seven</a>`,
			`<a href="/item/7?id=` + cid(1) + `">parent</a>`,
			`id="c-` + cid(2) + `"`, `<p>text 2</p>`, `<a href="/item/7?id=` + cid(2) + `">reply</a>`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %s in %s", want, out)
			}
		}
		if n := strings.Count(out, ">parent</a>"); n != 1 {
			t.Errorf("%d parent links, want 1 (only the reply has a parent)", n)
		}
		if strings.Index(out, cid(2)) > strings.Index(out, `id="c-`+cid(1)) {
			t.Error("newest comment must come first")
		}
	})

	t.Run("threads nest replies after their comment", func(t *testing.T) {
		threadsFake(t, 2)
		out := threadsHTML(t, page(t, "/threads", "", map[string]string{"id": "bob"}))
		parent, reply := strings.Index(out, `id="c-`+cid(2)+`"`), strings.Index(out, `id="c-`+cid(1021)+`"`)
		next := strings.Index(out, `id="c-`+cid(1)+`"`)
		if !(parent >= 0 && parent < reply && reply < next) {
			t.Errorf("order: comment %d, reply %d, next comment %d", parent, reply, next)
		}
		for _, want := range []string{`<h2>bob&#39;s comments</h2>`, `style="margin-left:40px"`, `style="margin-left:0px"`} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %s", want)
			}
		}
	})

	t.Run("focus", func(t *testing.T) {
		focusFake(t)
		out := threadsHTML(t, page(t, "/item/7", "", map[string]string{"id": cid(3)}, sdk.Block{ID: "it", Type: "hn_item"}))
		if !strings.Contains(out, `style="margin-left:160px"`) || strings.Contains(out, "on: ") {
			t.Errorf("focus: %s", out)
		}
	})

	t.Run("notices and the empty list", func(t *testing.T) {
		f := threadsFake(t, 1)
		if out := threadsHTML(t, page(t, "/threads", "", map[string]string{"id": "nobody"})); !strings.Contains(out, "No such user.") || strings.Contains(out, "No comments yet.") {
			t.Errorf("unknown user: %s", out)
		}
		f.byAuthorErr = &sdk.HostError{Code: sdk.ErrUnavailable}
		if out := threadsHTML(t, page(t, "/threads", "", map[string]string{"id": "bob"})); !strings.Contains(out, "Not available yet.") {
			t.Errorf("unavailable: %s", out)
		}
		f.meta = map[string]*metaRow{}
		if out := threadsHTML(t, page(t, "/newcomments", "", nil)); !strings.Contains(out, "No comments yet.") {
			t.Errorf("empty: %s", out)
		}
	})

	t.Run("hostile text is escaped and tags are rebuilt", func(t *testing.T) {
		const evil = "<script>alert(1)</script>"
		f, _, bob := newcommentsFake(t)
		bob.Username = evil
		f.stories[eid1].Title = evil
		m := f.comment(1, bob, 1)
		m.Name = evil
		m.Body = `<p><em>x</em> ` + evil + ` <a href="javascript:alert(1)">j</a> &lt;b&gt;</p>`
		ctx := page(t, "/newcomments", "", nil)
		for i, b := range ctx.Blocks {
			if b.Type == "hn_threads" {
				b.Content["notice"] = evil
				b.Content["heading"] = evil
				ctx.Blocks[i] = b
			}
		}
		out := threadsHTML(t, ctx)
		if strings.Contains(out, "<script") || strings.Contains(out, "javascript:") || strings.Contains(out, "<b>") {
			t.Errorf("unescaped output: %s", out)
		}
		for _, want := range []string{"<em>x</em>", "&lt;b&gt;"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %s in %s", want, out)
			}
		}
		if n := strings.Count(out, "&lt;script&gt;"); n < 4 {
			t.Errorf("only %d escaped scripts (author, title, notice, heading): %s", n, out)
		}
	})
}
