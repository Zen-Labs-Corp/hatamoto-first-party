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
	amy := f.add("k3", "amy")
	f.addLogin(amy, "acc-amy", "hash")
	f.logins[amy.ID].Email = "amy@example.com"
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
	// hasLabels: every id is on an input and a <label for> names it.
	hasLabels := func(label, out string, ids ...string) {
		t.Helper()
		for _, id := range ids {
			if strings.Count(out, `id="`+id+`"`) != 1 || strings.Count(out, `<label for="`+id+`">`) != 1 {
				t.Errorf("%s: id %q needs exactly one input and one label in %s", label, id, out)
			}
		}
	}

	logoutForm := `<form method="post" action="/p/hn/logout" class="hn-out"><button type="submit">logout</button></form>`
	has("bar named", render("/news", "k1", "hn_userbar"), `<a href="/user?id=bob">bob</a> (1)`, logoutForm)
	has("bar noname", render("/news", "k2", "hn_userbar"), "pick a username", logoutForm)
	if strings.Contains(render("/news", "k1", "hn_userbar"), "/-/comments/signin") {
		t.Error("bar logs out through a GET link")
	}
	has("bar out", render("/news", "", "hn_userbar"), ">login</a>")

	out := render("/login", "", "hn_login")
	has("login out", out, `<b>Login</b>`, `<b>Create Account</b>`, `action="/p/hn/login"`, `action="/p/hn/signup"`,
		`<a href="/-/comments/signin">Sign in by email</a>`, `>login</button>`, `>create account</button>`)
	if n := strings.Count(out, `type="password"`); n != 2 {
		t.Errorf("login out has %d password inputs, want 2", n)
	}
	for _, bad := range []string{`type="email"`, "Forgot your password?"} {
		if strings.Contains(out, bad) {
			t.Errorf("login out shows %q", bad)
		}
	}
	hasLabels("login out", out, "hn-login-user", "hn-login-pw", "hn-signup-user", "hn-signup-pw")
	has("login noname", render("/login", "k2", "hn_login"), `action="/p/hn/username"`, `name="username"`, `maxlength="15"`)
	has("login noname logout", render("/login", "k2", "hn_login"), logoutForm)
	has("login named", render("/login", "k1", "hn_login"), "signed in as bob", logoutForm)

	own := render("/user", "k1", "hn_user")
	has("user own", own, `action="/p/hn/profile"`, `name="about"`, `name="maxvisit"`, `<option value="no" selected>`)
	// A member without a password sees only the connect form.
	has("user own, no password", own, `action="/p/hn/password/connect"`, "connect password")
	for _, bad := range []string{`action="/p/hn/password"`, `type="email"`, "log out everywhere", "set password"} {
		if strings.Contains(own, bad) {
			t.Errorf("own profile without a password shows %q", bad)
		}
	}
	hasLabels("user own, no password", own, "hn-pw-connect")
	amyCtx := sdk.PageContext{Path: "/user", CommenterKey: "k3", Query: map[string]string{"id": "amy"}}
	withPw := renderBlock(t, tpl, block(t, renderPage(t, amyCtx), "hn_user"))
	has("user own, password", withPw, `action="/p/hn/password"`, "change password", `type="email"`, `value="amy@example.com"`,
		`action="/p/hn/logout/all"`, "log out everywhere")
	if strings.Contains(withPw, "connect") {
		t.Errorf("own profile with a password shows the connect form: %s", withPw)
	}
	hasLabels("user own, password", withPw, "hn-email", "hn-pw-current", "hn-pw-new")
	other := render("/user", "", "hn_user")
	has("user other", other, `<a href="https://example.com/x" rel="nofollow noopener">`)
	if strings.Contains(other, "<form") || strings.Contains(other, "<input") {
		t.Errorf("user page shows a form to others: %s", other)
	}
	ctx := sdk.PageContext{Path: "/user", Query: map[string]string{"id": "ghost"}}
	has("user unknown", renderBlock(t, tpl, block(t, renderPage(t, ctx), "hn_user")), "No such user.")
	// A host error leaves the placeholder with no content: not a not-found page.
	if out := renderBlock(t, tpl, sdk.Block{Type: "hn_user"}); strings.Contains(out, "No such user.") {
		t.Errorf("empty user placeholder says not found: %s", out)
	}
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
