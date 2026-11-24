package stories

import (
	"bytes"
	"encoding/json"
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

var sectionFiles = []string{"../../sections/hn_feed.html", "../../sections/hn_item.html", "../../sections/hn_submit.html", "../../partials/story-row.html"}

func TestSectionsAvoidBannedMarkup(t *testing.T) {
	banned := []string{"<svg", "<script", "<iframe", "<object", "<embed", "<base", "<meta", "<link", "<math", "<template", "<noscript", "<dialog", "<title",
		"formaction", "srcdoc", "http-equiv", "xlink:href", "javascript:", "data:", "vbscript:"}
	for _, file := range sectionFiles {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src, low := string(raw), strings.ToLower(string(raw))
		for _, bad := range banned {
			if strings.Contains(low, bad) {
				t.Errorf("%s contains %q", file, bad)
			}
		}
		if strings.Contains(low, "<textarea") != strings.HasSuffix(file, "hn_submit.html") {
			t.Errorf("%s: <textarea only belongs in hn_submit.html", file)
		}
		if regexp.MustCompile(`(?i)\son\w+\s*=`).MatchString(src) {
			t.Errorf("%s has an on* attribute", file)
		}
		if regexp.MustCompile(`<[a-z][^>"']*\{\{`).MatchString(src) {
			t.Errorf("%s: an action sits inside a tag outside quotes", file)
		}
		if regexp.MustCompile(`(?is)<textarea[^>]*\{\{|<textarea.*?\{\{.*?</textarea>`).MatchString(src) {
			t.Errorf("%s: an action sits inside a textarea", file)
		}
		for _, f := range regexp.MustCompile(`<form[^>]*>`).FindAllString(src, -1) {
			if !strings.Contains(f, `method="post"`) {
				t.Errorf("%s: form without method=post: %s", file, f)
			}
		}
	}
}

func sectionTemplates(t *testing.T) *template.Template {
	t.Helper()
	tpl, err := template.New("").ParseFiles("../../partials/style.html", "../../partials/story-row.html",
		"../../sections/hn_feed.html", "../../sections/hn_item.html", "../../sections/hn_submit.html")
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

func TestSectionsDefineNames(t *testing.T) {
	tpl := sectionTemplates(t)
	want := map[string]bool{"section-hn_feed": true, "section-hn_item": true, "section-hn_submit": true, "plugin/hn/story-row": true, "plugin/hn/style": true, "": true}
	for _, x := range tpl.Templates() {
		if strings.HasSuffix(x.Name(), ".html") { // ParseFiles names each file's own (empty) body
			continue
		}
		if !want[x.Name()] {
			t.Errorf("unexpected template %q", x.Name())
		}
		delete(want, x.Name())
	}
	delete(want, "")
	if len(want) != 0 {
		t.Errorf("missing templates %v", want)
	}
}

// run renders a section over content that went through JSON, as the host sends it.
func run(t *testing.T, name string, content map[string]any) string {
	t.Helper()
	b, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := sectionTemplates(t).ExecuteTemplate(&out, name, map[string]any{"Content": back}); err != nil {
		t.Fatal(err)
	}
	return plainStyleRe.ReplaceAllString(out.String(), "")
}

// plainStyleRe drops the <style> blocks (the shared partial names classes the
// assertions count in the markup).
var plainStyleRe = regexp.MustCompile(`(?s)<style>.*?</style>`)

func TestSectionsRenderFeed(t *testing.T) {
	pinNow(t, feedNow)
	install(t, &fakeDB{})
	r := newRegistry(t, nil)
	st := core.DefaultSettings()
	at := feedNow.Add(-5 * time.Minute)
	base := storyRow{EntityID: entityID, ItemNo: 42, AuthorID: 2, By: "bob", Title: "<script>alert(1)</script>", URL: "https://example.com/a", Domain: "example.com", Kind: "story", Points: 3, CreatedAt: feedNow.Add(-3 * time.Hour)}
	voted := base
	voted.ItemNo, voted.VotedAt, voted.Title = 43, &at, "voted"
	job := base
	job.ItemNo, job.Kind, job.URL, job.Title = 44, "job", "", "hiring"
	evil := base
	evil.ItemNo, evil.URL, evil.Title = 45, "javascript:alert(1)", "evil"
	var rows []map[string]any
	for i, s := range []storyRow{base, voted, job, evil} {
		rows = append(rows, rowView(r, &core.User{ID: 1, Username: "alice"}, s, i+1, "/news", st))
	}
	html := run(t, "section-hn_feed", map[string]any{"notice": "Saved.", "heading": "", "rows": rows, "more": "/news?p=2", "empty": false})
	for _, want := range []string{
		`&lt;script&gt;alert(1)&lt;/script&gt;`, `action="/p/hn/vote/story/42"`, `href="https://example.com/a" rel="nofollow noopener"`,
		`class="hn-vote hn-voted"`, `href="/news?p=2" rel="next"`, `<p class="hn-notice" role="status">Saved.</p>`, `action="/p/hn/vote/story/43?how=un"`,
		`href="/from?site=example.com"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("feed lacks %q", want)
		}
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "javascript:") {
		t.Errorf("unescaped output: %s", html)
	}
	if n := strings.Count(html, "hn-vote-form"); n != 2 { // base and evil; voted and job have none
		t.Errorf("%d vote forms, want 2", n)
	}
	if !strings.Contains(html, "ZgotmplZ") {
		t.Error("an injected javascript: url was not neutralised")
	}
	empty := run(t, "section-hn_feed", map[string]any{"rows": []map[string]any{}, "empty": true})
	if !strings.Contains(empty, "No stories yet.") || strings.Contains(empty, "hn-more") {
		t.Errorf("empty feed: %s", empty)
	}
}

func TestSectionsRenderItem(t *testing.T) {
	pinNow(t, feedNow)
	install(t, &fakeDB{})
	r := newRegistry(t, nil)
	s := storyRow{EntityID: entityID, ItemNo: 42, AuthorID: 2, By: "bob", Title: "T", Kind: "story", CreatedAt: feedNow.Add(-3 * time.Hour)}
	row := rowView(r, nil, s, 0, "/item/42", core.DefaultSettings())
	html := run(t, "section-hn_item", map[string]any{"row": row, "paras": []string{"one <b>", "two"}})
	if !strings.Contains(html, "<p>one &lt;b&gt;</p>") || !strings.Contains(html, "<p>two</p>") || !strings.Contains(html, "hn-item-head") {
		t.Errorf("item: %s", html)
	}
	if gone := run(t, "section-hn_item", map[string]any{"gone": true}); !strings.Contains(gone, "This story is dead.") || strings.Contains(gone, "hn-item-head") {
		t.Errorf("gone: %s", gone)
	}
}

func TestSectionsRenderSubmit(t *testing.T) {
	if h := run(t, "section-hn_submit", map[string]any{"state": "login", "login_href": "/login?m=story-login"}); !strings.Contains(h, `href="/login?m=story-login"`) || strings.Contains(h, "<form") {
		t.Errorf("login: %s", h)
	}
	if h := run(t, "section-hn_submit", map[string]any{"state": "denied", "reason": "No <way>."}); !strings.Contains(h, "No &lt;way&gt;.") || strings.Contains(h, "<form") {
		t.Errorf("denied: %s", h)
	}
	h := run(t, "section-hn_submit", map[string]any{"state": "new", "action": "/p/hn/submit"})
	if !strings.Contains(h, `action="/p/hn/submit"`) || !strings.Contains(h, "Leave url blank") || strings.Contains(h, "keep_text") {
		t.Errorf("new: %s", h)
	}
	h = run(t, "section-hn_submit", map[string]any{"state": "edit", "action": "/p/hn/story/42/edit", "delete_action": "/p/hn/story/42/delete",
		"title": `a "q"`, "url": "https://example.com/", "text": "old <i>text</i>", "can_delete": true})
	for _, want := range []string{`action="/p/hn/story/42/edit"`, `action="/p/hn/story/42/delete"`, "old &lt;i&gt;text&lt;/i&gt;", `name="keep_text"`, `<textarea id="hn-text" name="text" rows="4" cols="49"></textarea>`} {
		if !strings.Contains(h, want) {
			t.Errorf("edit lacks %q: %s", want, h)
		}
	}
	if h = run(t, "section-hn_submit", map[string]any{"state": "edit", "action": "/a", "can_delete": false}); strings.Contains(h, "delete") {
		t.Errorf("edit with comments offers delete: %s", h)
	}
}
