package main

import (
	"bytes"
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

// parseTemplates parses both files with no extra funcs, as core's scratch
// parse of plugin templates does: only the Go builtins exist.
func parseTemplates(t *testing.T) *template.Template {
	t.Helper()
	tpl := template.New("t")
	for path := range templateFiles {
		if _, err := tpl.Parse(readTemplate(t, path)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	return tpl
}

func renderContent(t *testing.T, content map[string]any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := parseTemplates(t).ExecuteTemplate(&buf, "section-payment_button", map[string]any{"Content": content}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func renderSection(t *testing.T, label, url string) string {
	return renderContent(t, map[string]any{"label": label, "url": url})
}

func TestSectionRendersEmptyContentAsAnInertSpan(t *testing.T) {
	got := renderContent(t, map[string]any{})
	if strings.Contains(got, "<a ") || !strings.Contains(got, "pl-button-off") {
		t.Fatalf("empty content rendered %s", got)
	}
}

func TestButtonRendersALinkOnlyForHTTPS(t *testing.T) {
	got := renderSection(t, "Donate", "https://buy.stripe.com/abc")
	if !strings.HasPrefix(got, `<div class="pl-button-wrap">`) || !strings.HasSuffix(got, `</div>`) {
		t.Fatalf("no wrapper: %s", got)
	}
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
