package comments

import (
	"bytes"
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func parseSections(t *testing.T) *template.Template {
	t.Helper()
	return template.Must(template.ParseFiles("../../partials/style.html", "../../partials/comment_vote.html", "../../partials/comment_row.html", "../../sections/hn_threads.html", "../../sections/hn_reply.html", "../../partials/login_forms.html"))
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
			`id="c-` + cid(2) + `"`, `<p>text 2</p>`, `<a href="/reply?id=` + cid(2) + `">reply</a>`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %s in %s", want, out)
			}
		}
		if strings.Contains(out, ">context</a>") {
			t.Errorf("context link outside the reply page: %s", out)
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
		for _, want := range []string{`<h2>bob&#39;s comments</h2>`, `style="--hn-d:1"`, `style="--hn-d:0"`} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %s", want)
			}
		}
	})

	t.Run("focus", func(t *testing.T) {
		focusFake(t)
		out := threadsHTML(t, page(t, "/item/7", "", map[string]string{"id": cid(3)}, sdk.Block{ID: "it", Type: "hn_item"}))
		if !strings.Contains(out, `style="--hn-d:4"`) || strings.Contains(out, "on: ") {
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

func voteForms(out, id string) int {
	return strings.Count(out, `<form class="hn-vote-form" method="post" action="/p/hn/vote/comment/`+id+`">`)
}

func TestCommentVotePartial(t *testing.T) {
	t.Run("not voted shows the up arrow only", func(t *testing.T) {
		f, _, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		out := threadsHTML(t, page(t, "/newcomments", "k1", nil))
		for _, want := range []string{
			`<span class="hn-vote" data-hn-comment="` + cid(1) + `">`,
			`<input type="hidden" name="how" value="up">`, `<input type="hidden" name="next" value="/newcomments">`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %s in %s", want, out)
			}
		}
		if voteForms(out, cid(1)) != 1 || strings.Contains(out, `value="down"`) || strings.Contains(out, `value="un"`) {
			t.Errorf("forms: %s", out)
		}
	})

	t.Run("karma bar opens the down arrow", func(t *testing.T) {
		f, ann, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		ann.Karma = 500
		out := threadsHTML(t, page(t, "/newcomments", "k1", nil))
		if voteForms(out, cid(1)) != 2 || !strings.Contains(out, `name="how" value="down"`) {
			t.Errorf("forms: %s", out)
		}
	})

	t.Run("voted shows unvote only", func(t *testing.T) {
		f, ann, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		f.votes[cid(1)] = map[int64]*voteRow{ann.ID: {Value: 1}}
		out := threadsHTML(t, page(t, "/newcomments", "k1", nil))
		if !strings.Contains(out, `class="hn-vote hn-voted" data-hn-comment="`+cid(1)+`"`) || voteForms(out, cid(1)) != 1 ||
			!strings.Contains(out, `name="how" value="un"`) || strings.Contains(out, `value="up"`) {
			t.Errorf("voted: %s", out)
		}
	})

	t.Run("dead and own rows print nothing", func(t *testing.T) {
		f := threadsFake(t, 1)
		reply := cid(1011)
		f.subtrees[cid(1)].Items[1].Status = "dead"
		out := threadsHTML(t, page(t, "/threads", "k2", map[string]string{"id": "bob"}))
		for _, id := range []string{reply, cid(1)} {
			if strings.Contains(out, `data-hn-comment="`+id+`"`) || strings.Contains(out, "/p/hn/vote/comment/"+id) {
				t.Errorf("vote markup for %s: %s", id, out)
			}
		}
		if !strings.Contains(out, `data-hn-comment="`+cid(1012)+`"`) {
			t.Errorf("the live reply of another member should be votable: %s", out)
		}
	})

	t.Run("next is escaped", func(t *testing.T) {
		threadsFake(t, 20)
		out := threadsHTML(t, page(t, "/threads", "k1", map[string]string{"id": "bob", "p": "2"}))
		if !strings.Contains(out, `name="next" value="/threads?id=bob&amp;p=2"`) {
			t.Errorf("next: %s", out)
		}
	})
}

var (
	tagSpanRe = regexp.MustCompile(`<[a-zA-Z/][^<>]*>`)
	actionRe  = regexp.MustCompile(`\{\{.*?\}\}`)
	badTagRe  = regexp.MustCompile(`(?i)<(script|iframe|object|embed|base|meta|link|svg|math|template|noscript|dialog|textarea|title)\b`)
	onAttrRe  = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	badURLRe  = regexp.MustCompile(`(?i)(javascript|vbscript|data):`)
	urlAttrRe = regexp.MustCompile(`(?i)\b(?:action|href)="([^"]*)"`)
	styleRe   = regexp.MustCompile(`(?s)<style>.*?</style>`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	// every action a plugin template here may use: printing a field, control
	// flow over a field, template calls and definitions; no function calls.
	okActionRe = regexp.MustCompile(`^\{\{\s*(?:[.$][\w.]*|(?:if|else if|range|with)\s+[.$][\w.]*|else|end|define "[^"]+"|template "[^"]+" [.$]\w*|\$c := \.Content)\s*\}\}$`)
	printRe    = regexp.MustCompile(`^\{\{\s*[.$][\w.]*\s*\}\}$`)
)

// TestCommentVotePartialMarkupRules checks the template source against the
// plugin markup rules the host enforces at load time.
func TestCommentVotePartialMarkupRules(t *testing.T) {
	for _, file := range []string{"../../partials/comment_vote.html", "../../partials/comment_row.html", "../../sections/hn_threads.html"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		name := filepath.Base(file)
		if badTagRe.MatchString(src) || onAttrRe.MatchString(src) || badURLRe.MatchString(src) {
			t.Errorf("%s: script-capable markup", name)
		}
		for _, a := range actionRe.FindAllString(src, -1) {
			if !okActionRe.MatchString(a) {
				t.Errorf("%s: action %s is not a plain field, control or template call", name, a)
			}
		}
		for _, m := range urlAttrRe.FindAllStringSubmatch(src, -1) {
			if !strings.HasPrefix(m[1], "/") && !strings.HasPrefix(m[1], "{{") {
				t.Errorf("%s: URL attribute %q must start with / or an action", name, m[1])
			}
		}
		for _, re := range []*regexp.Regexp{styleRe, commentRe} {
			for _, m := range re.FindAllString(src, -1) {
				if strings.Contains(m, "{{") {
					t.Errorf("%s: action inside %.20s...", name, m)
				}
			}
		}
		for _, span := range tagSpanRe.FindAllString(src, -1) {
			if err := actionsInQuotes(span); err != "" {
				t.Errorf("%s: %s in %s", name, err, span)
			}
		}
	}
}

// actionsInQuotes reports the first action of a tag that is not a printing
// action wholly inside a quoted attribute value.
func actionsInQuotes(tag string) string {
	quote := false
	for i := 0; i < len(tag); i++ {
		switch {
		case strings.HasPrefix(tag[i:], "{{"):
			end := strings.Index(tag[i:], "}}")
			if end < 0 {
				return "unterminated action"
			}
			a := tag[i : i+end+2]
			if !quote || !printRe.MatchString(a) {
				return "action " + a + " outside a quoted value or not a print"
			}
			i += end + 1
		case tag[i] == '"':
			quote = !quote
		}
	}
	return ""
}
