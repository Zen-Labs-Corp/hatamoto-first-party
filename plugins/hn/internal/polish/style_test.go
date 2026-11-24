package polish

import (
	"bytes"
	"html/template"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const stylePath = "../../partials/style.html"

var (
	badTagRe = regexp.MustCompile(`(?i)<(script|iframe|object|embed|base|meta|link|svg|math|template|noscript|dialog|textarea|title)\b`)
	onAttrRe = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	badURLRe = regexp.MustCompile(`(?i)(javascript|vbscript|data):`)
)

func styleSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(stylePath)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestStyleStatic(t *testing.T) {
	src := styleSource(t)
	tpl, err := template.ParseFiles(stylePath)
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Lookup("plugin/hn/style") == nil {
		t.Fatal("plugin/hn/style is not defined")
	}
	if strings.Count(src, "<style>") != 1 || strings.Count(src, "</style>") != 1 {
		t.Fatal("want exactly one <style> block")
	}
	css := src[strings.Index(src, "<style>")+len("<style>") : strings.Index(src, "</style>")]
	if strings.Contains(css, "{{") || strings.Contains(css, "<") {
		t.Error("the CSS holds a template action or a tag")
	}
	if n := strings.Count(src, "{{"); n != 2 {
		t.Errorf("%d actions in the file, want the define/end pair only", n)
	}
	for _, re := range []*regexp.Regexp{badTagRe, onAttrRe, badURLRe} {
		if re.MatchString(src) {
			t.Errorf("markup rule %v matches", re)
		}
	}
	for _, bad := range []string{"url(", "@import", "outline:none"} {
		if strings.Contains(strings.ToLower(src), bad) {
			t.Errorf("contains %q", bad)
		}
	}
	if len(src) >= 5120 {
		t.Errorf("%d bytes, want < 5120", len(src))
	}
}

func TestStyleTokens(t *testing.T) {
	src := styleSource(t)
	for _, want := range []string{
		"--hn-accent:#c23b22", "--hn-bg:#f6f6ef", "--hn-muted:#6b6b6b", "--hn-dead:#bfbfbf",
		"Verdana", "10pt", "7pt", "9pt", "--hn-indent:40px", "--hn-indent:14px",
		"@media (max-width:750px)", "22px 14px 1fr", "min-width:24px", "min-height:24px",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, c := range []string{
		"hn-stories", "hn-story", "hn-notice", "hn-note", "hn-more", "hn-rank", "hn-vote-cell",
		"hn-vote-form", "hn-title", "hn-site", "hn-sub", "hn-act", "hn-item-head", "hn-item-text",
		"hn-form-tbl", "hn-lbl", "hn-user", "hn-login", "hn-threads", "hn-head", "hn-text",
		"hn-dead", "hn-faint", "hn-reply",
	} {
		if !strings.Contains(src, "."+c) {
			t.Errorf("no rule for .%s", c)
		}
	}
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	n, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		t.Fatalf("bad colour %q", hex)
	}
	lin := func(v uint64) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(n>>16&0xff) + 0.7152*lin(n>>8&0xff) + 0.0722*lin(n&0xff)
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestStyleContrast(t *testing.T) {
	src := styleSource(t)
	tok := func(name string) string {
		m := regexp.MustCompile(`--hn-` + name + `:(#[0-9a-f]{6})`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("no --hn-%s token", name)
		}
		return m[1]
	}
	muted, accent := contrast(t, tok("muted"), tok("bg")), contrast(t, "#ffffff", tok("accent"))
	t.Logf("muted on bg %.2f, white on accent %.2f", muted, accent)
	if muted < 4.5 || accent < 4.5 {
		t.Errorf("contrast muted %.2f accent %.2f, want >= 4.5", muted, accent)
	}
}

func TestStyleRendersInSections(t *testing.T) {
	src := styleSource(t)
	tpl, err := template.ParseFiles(stylePath)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tpl.ExecuteTemplate(&out, "plugin/hn/style", nil); err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(src), `{{define "plugin/hn/style"}}`), "{{end}}")
	// html/template turns a CSS comment into a space; nothing else may change.
	want = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(want, " ")
	if out.String() != want {
		t.Errorf("rendered style differs from the static text:\n%q\n%q", out.String()[:200], want[:200])
	}
}
