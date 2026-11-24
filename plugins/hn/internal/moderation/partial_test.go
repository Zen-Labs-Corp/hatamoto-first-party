package moderation

import (
	"bytes"
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"
)

const partialFile = "../../partials/comment-actions.html"

func partialTemplates(t *testing.T) *template.Template {
	t.Helper()
	dict := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	tpl, err := template.New("").Funcs(template.FuncMap{"dict": dict}).ParseFiles(partialFile)
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

func TestCommentActionsPartial(t *testing.T) {
	raw, err := os.ReadFile(partialFile)
	if err != nil {
		t.Fatal(err)
	}
	src, low := string(raw), strings.ToLower(string(raw))
	banned := []string{"<svg", "<script", "<iframe", "<object", "<embed", "<base", "<meta", "<link", "<math", "<template", "<noscript", "<dialog", "<title", "<textarea",
		"formaction", "srcdoc", "http-equiv", "xlink:href", "javascript:", "data:", "vbscript:", "style="}
	for _, bad := range banned {
		if strings.Contains(low, bad) {
			t.Errorf("partial contains %q", bad)
		}
	}
	if regexp.MustCompile(`(?i)\son[a-z]+\s*=`).MatchString(src) {
		t.Error("partial has an on* attribute")
	}
	if regexp.MustCompile(`<[a-z][^>"']*\{\{`).MatchString(src) {
		t.Error("an action sits inside a tag outside quotes")
	}

	tpl := partialTemplates(t)
	for _, x := range tpl.Templates() {
		if x.Name() == "" || strings.HasSuffix(x.Name(), ".html") { // the roots and the file's own (empty) body
			continue
		}
		if x.Name() != "plugin/hn/comment-actions" {
			t.Errorf("unexpected template %q", x.Name())
		}
	}
	if tpl.Lookup("plugin/hn/comment-actions") == nil {
		t.Fatal("plugin/hn/comment-actions is not defined")
	}

	render := func(actions []map[string]any) string {
		var b bytes.Buffer
		row := map[string]any{"actions": actions, "next": "/item/7"}
		data := map[string]any{"Item": row}
		if err := tpl.ExecuteTemplate(&b, "plugin/hn/comment-actions", data); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := render([]map[string]any{{"label": "<b>flag</b>", "href": "/p/hn/flag/comment/" + commentID + "?next=/item/7", "class": "hn-flag", "post": true}})
	for _, want := range []string{`&lt;b&gt;flag&lt;/b&gt;`, `action="/p/hn/flag/comment/`, `name="next" value="/item/7"`, `method="post"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "<b>") {
		t.Errorf("label not escaped: %s", out)
	}
	if got := render(nil); got != "" {
		t.Errorf("no actions rendered %q", got)
	}
	link := render([]map[string]any{{"label": "x", "href": "/y", "class": "c", "post": false}})
	if !strings.Contains(link, `<a class="c" href="/y">x</a>`) || strings.Contains(link, "<form") {
		t.Errorf("link action: %s", link)
	}
}
