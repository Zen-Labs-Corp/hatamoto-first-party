package main

import (
	"bytes"
	"errors"
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"
)

func readTemplate(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var templateFiles = map[string]string{
	"partials/button.html":         `{{define "plugin/payment_links/button"}}`,
	"sections/payment_button.html": `{{define "section-payment_button"}}`,
}

func TestTemplatesDefineTheirNames(t *testing.T) {
	for path, define := range templateFiles {
		if !strings.Contains(readTemplate(t, path), define) {
			t.Errorf("%s lacks %s", path, define)
		}
	}
}

// Core's plugin template check rejects script, frames, meta, link, svg, event
// handlers and script-like URLs, and any control action inside a tag.
func TestTemplatesHaveNoForbiddenMarkup(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)<(script|iframe|meta|link|svg)\b|\bon[a-z]+\s*=|javascript:|data:|\bstyle\s*=`)
	control := regexp.MustCompile(`\{\{-?\s*(if|else|end|range|with|template)\b`)
	tag := regexp.MustCompile(`<[^>]*>`)
	for path := range templateFiles {
		src := readTemplate(t, path)
		if m := forbidden.FindString(src); m != "" {
			t.Errorf("%s contains %q", path, m)
		}
		// Drop the actions first so a ">" inside one cannot cut a tag short.
		for _, m := range tag.FindAllString(src, -1) {
			if control.MatchString(m) {
				t.Errorf("%s has a control action inside the tag %q", path, m)
			}
		}
	}
}

// The test renders with stub versions of core's hasPrefix and dict (stdlib
// only: no core import).
func renderSection(t *testing.T, label, url string) string {
	t.Helper()
	funcs := template.FuncMap{
		"hasPrefix": strings.HasPrefix,
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, errors.New("odd dict")
			}
			m := map[string]any{}
			for i := 0; i < len(kv); i += 2 {
				m[kv[i].(string)] = kv[i+1]
			}
			return m, nil
		},
	}
	tpl := template.New("t").Funcs(funcs)
	for path := range templateFiles {
		template.Must(tpl.Parse(readTemplate(t, path)))
	}
	var buf bytes.Buffer
	data := map[string]any{"Content": map[string]any{"label": label, "url": url}}
	if err := tpl.ExecuteTemplate(&buf, "section-payment_button", data); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestButtonRendersALinkOnlyForHTTPS(t *testing.T) {
	got := renderSection(t, "Donate", "https://buy.stripe.com/abc")
	if !strings.Contains(got, `<a class="pl-button" href="https://buy.stripe.com/abc" rel="noopener">Donate</a>`) {
		t.Fatalf("https: %s", got)
	}
	for _, u := range []string{"javascript:alert(1)", "http://example.com", "", "data:text/html,x"} {
		if got := renderSection(t, "Donate", u); strings.Contains(got, "<a ") || !strings.Contains(got, "pl-button-off") {
			t.Errorf("%q rendered %s", u, got)
		}
	}
}

func TestButtonEscapesTheLabel(t *testing.T) {
	if got := renderSection(t, `<b onclick="x">`, "https://example.com/p"); strings.Contains(got, "<b") {
		t.Fatalf("label not escaped: %s", got)
	}
}
