package parity

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

var sectionFiles = []string{"../../sections/hn_saved.html"}

func TestFavoritesSectionAvoidsBannedMarkup(t *testing.T) {
	banned := []string{"<svg", "<script", "<iframe", "<object", "<embed", "<base", "<meta", "<link", "<math", "<template", "<noscript", "<dialog", "<title", "<textarea",
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
		if regexp.MustCompile(`(?i)\son\w+\s*=`).MatchString(src) {
			t.Errorf("%s has an on* attribute", file)
		}
		if regexp.MustCompile(`<[a-z][^>"']*\{\{`).MatchString(src) {
			t.Errorf("%s: an action sits inside a tag outside quotes", file)
		}
		if regexp.MustCompile(`(?is)<style[^>]*>.*?\{\{.*?</style>|<!--.*?\{\{.*?-->`).MatchString(src) {
			t.Errorf("%s: an action sits in a style block or a comment", file)
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
	tpl, err := template.New("").ParseFiles("../../partials/style.html", "../../partials/story-row.html", "../../sections/hn_saved.html")
	if err != nil {
		t.Fatal(err)
	}
	return tpl
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
	return out.String()
}

func TestFavoritesSectionRenders(t *testing.T) {
	pinNow(t, savedNow)
	install(t, favDB(`{"fav":false,"hid":false}`, false))
	r := newRegistry(t, nil)
	s := storyRow{EntityID: storyUUID, ItemNo: 42, AuthorID: 2, By: "bob", Title: "<script>alert(1)</script>", URL: "https://example.com/a", Domain: "example.com", Kind: "story", Points: 3, CreatedAt: savedNow.Add(-3 * time.Hour)}
	row := storyView(r, &core.User{ID: 1, Username: "alice"}, s, 1, "/favorites?id=bob")
	cm := commentView(commentRow{CommentID: commentUUID, ItemNo: 42, By: "carol", StoryTitle: "S <b>x</b>", Excerpt: "<img src=x>", CreatedAt: savedNow.Add(-time.Hour)})
	html := run(t, "section-hn_saved", map[string]any{"state": "list", "notice": "Saved.", "heading": "bob's favorites", "kind": "story",
		"stories_href": "/favorites?id=bob", "comments_href": "/favorites?id=bob&kind=comment", "rows": []map[string]any{row}, "comments": []map[string]any{cm}, "more": "/favorites?id=bob&p=2", "empty": false})
	for _, want := range []string{
		`&lt;script&gt;alert(1)&lt;/script&gt;`, `action="/p/hn/favorite/story/42"`, `action="/p/hn/hide/42"`, `href="/favorites?id=bob&amp;kind=comment"`,
		`href="/favorites?id=bob&amp;p=2" rel="next"`, `<p class="hn-notice" role="status">Saved.</p>`, `bob&#39;s favorites`, `href="/item/42#` + commentUUID + `"`,
		`S &lt;b&gt;x&lt;/b&gt;`, `&lt;img src=x&gt;`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("list lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "<img") {
		t.Errorf("unescaped output: %s", html)
	}
	if h := run(t, "section-hn_saved", map[string]any{"state": "login", "login_href": "/login?m=saved-login"}); !strings.Contains(h, `href="/login?m=saved-login"`) || strings.Contains(h, "hn-stories") {
		t.Errorf("login: %s", h)
	}
	if h := run(t, "section-hn_saved", map[string]any{"state": "nouser"}); !strings.Contains(h, "No such user.") {
		t.Errorf("nouser: %s", h)
	}
	if h := run(t, "section-hn_saved", map[string]any{"state": "list", "heading": "H", "rows": []map[string]any{}, "empty": true}); !strings.Contains(h, "Nothing here yet.") || strings.Contains(h, "hn-more") {
		t.Errorf("empty: %s", h)
	}
	h := run(t, "section-hn_saved", map[string]any{"procrast": true, "maxvisit": "20", "minaway": "179", "profile_href": "/user?id=al%20ice"})
	for _, want := range []string{"Get back to work!", "you have used your 20 minutes", "Come back in 179 minutes", `href="/user?id=al%20ice"`} {
		if !strings.Contains(h, want) {
			t.Errorf("procrast lacks %q: %s", want, h)
		}
	}
	if strings.Contains(h, "hn-stories") {
		t.Errorf("procrast shows a list: %s", h)
	}
}
