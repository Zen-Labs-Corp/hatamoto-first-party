package accounts

import (
	"bytes"
	"encoding/json"
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

var sectionTypes = []string{"hn_userbar", "hn_login", "hn_user"}

func parseSections(t *testing.T) *template.Template {
	t.Helper()
	files := []string{"../../partials/style.html"}
	for _, typ := range sectionTypes {
		files = append(files, "../../sections/"+typ+".html")
	}
	return template.Must(template.ParseFiles(files...))
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

func TestPagesEscaping(t *testing.T) {
	const evil = "<script>alert(1)</script>"
	f := installFake(t)
	bob := f.add("k1", evil)
	bob.About = "<script>x</script> https://example.com/a?b=<c>"
	f.add("k2", "amy")
	tpl := parseSections(t)

	views := []struct{ path, key string }{
		{"/news", "k1"}, {"/login", "k1"}, {"/user", "k1"}, {"/user", "k2"},
	}
	for _, v := range views {
		ctx := sdk.PageContext{Path: v.path, CommenterKey: v.key, Query: map[string]string{"id": evil}}
		for _, b := range renderPage(t, ctx).Blocks {
			out := renderBlock(t, tpl, b)
			if strings.Contains(out, "<script") {
				t.Errorf("%s as %s, %s: raw <script in %s", v.path, v.key, b.Type, out)
			}
			if b.Type != "hn_login" && !(v.key == "k2" && b.Type == "hn_userbar") && !strings.Contains(out, "&lt;script&gt;") {
				t.Errorf("%s as %s, %s: escaped name missing in %s", v.path, v.key, b.Type, out)
			}
		}
	}
}

func TestPagesSectionStates(t *testing.T) {
	f := installFake(t)
	f.add("k1", "bob").About = "see https://example.com/x"
	tpl := parseSections(t)
	render := func(path, key, typ string) string {
		ctx := sdk.PageContext{Path: path, CommenterKey: key, Query: map[string]string{"id": "bob"}}
		for _, b := range renderPage(t, ctx).Blocks {
			if b.Type == typ {
				return renderBlock(t, tpl, b)
			}
		}
		t.Fatalf("no %s block", typ)
		return ""
	}
	has := func(label, out string, wants ...string) {
		t.Helper()
		for _, w := range wants {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q in %s", label, w, out)
			}
		}
	}

	has("bar named", render("/news", "k1", "hn_userbar"), `<a href="/user?id=bob">bob</a> (1)`, "logout")
	has("bar noname", render("/news", "k2", "hn_userbar"), "pick a username", "logout")
	has("bar out", render("/news", "", "hn_userbar"), ">login</a>")

	out := render("/login", "", "hn_login")
	has("login out", out, `action="/-/comments/signin"`, `name="email"`)
	if strings.Contains(out, `type="password"`) {
		t.Error("login has a password field")
	}
	has("login noname", render("/login", "k2", "hn_login"), `action="/p/hn/username"`, `name="username"`, `maxlength="15"`)
	has("login named", render("/login", "k1", "hn_login"), "signed in as bob")

	has("user own", render("/user", "k1", "hn_user"), `action="/p/hn/profile"`, `name="about"`, `name="maxvisit"`, `<option value="no" selected>`)
	other := render("/user", "", "hn_user")
	has("user other", other, `<a href="https://example.com/x" rel="nofollow noopener">`)
	if strings.Contains(other, "<form") || strings.Contains(other, "<input") {
		t.Errorf("user page shows a form to others: %s", other)
	}
	ctx := sdk.PageContext{Path: "/user", Query: map[string]string{"id": "ghost"}}
	has("user unknown", renderBlock(t, tpl, block(t, renderPage(t, ctx), "hn_user")), "No such user.")
}

var (
	actionInTag = regexp.MustCompile(`<[^>]*\{\{\s*(if|else|range|end|with|template)`)
	onAttr      = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
)

func TestPagesSectionFiles(t *testing.T) {
	tpl := parseSections(t)
	for _, typ := range sectionTypes {
		raw, err := os.ReadFile("../../sections/" + typ + ".html")
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		name := "section-" + typ
		if tpl.Lookup(name) == nil {
			t.Errorf("%s defines no %s", typ, name)
		}
		if !strings.HasPrefix(src, `{{define "`+name+`"}}{{template "plugin/hn/style" .}}`) {
			t.Errorf("%s does not start with its define and the style partial", typ)
		}
		if n := strings.Count(src, "{{define"); n != 1 {
			t.Errorf("%s has %d defines, want 1", typ, n)
		}
		for _, banned := range []string{"<textarea", "<script", "<iframe", "<svg", "formaction"} {
			if strings.Contains(strings.ToLower(src), banned) {
				t.Errorf("%s contains %s", typ, banned)
			}
		}
		if onAttr.MatchString(src) {
			t.Errorf("%s has an on* attribute", typ)
		}
		if m := actionInTag.FindString(src); m != "" {
			t.Errorf("%s has a control action inside a tag: %q", typ, m)
		}
	}
}
