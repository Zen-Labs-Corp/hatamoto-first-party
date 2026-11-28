package main

import (
	"encoding/json"
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The host refuses a whole plugin template pack that breaks its markup rules,
// so each section is checked against the same banned list here.
func TestSectionsAvoidBannedMarkup(t *testing.T) {
	for file, define := range map[string]string{
		"sections/registry_directory.html": "section-registry_directory",
		"sections/registry_detail.html":    "section-registry_detail",
	} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		for _, bad := range []string{"<textarea", "<script", "<style", "<meta", "<iframe", "<svg", "<link", "<object", "<embed", "<base", "<noscript",
			"<dialog", "<template", "<title", "<math", "<form", "formaction", "http-equiv", "srcdoc", "xlink:href", "javascript:", "data:", "vbscript:", "style="} {
			if strings.Contains(strings.ToLower(src), bad) {
				t.Errorf("%s contains %q", file, bad)
			}
		}
		if regexp.MustCompile(`(?i)\son\w+\s*=`).MatchString(src) {
			t.Errorf("%s has an on* attribute", file)
		}
		if !strings.Contains(src, `{{define "`+define+`"}}`) {
			t.Errorf("%s does not define %s", file, define)
		}
		// Printing actions sit in text or whole quoted attribute values only.
		if regexp.MustCompile(`<[a-z][^>"']*\{\{`).MatchString(src) {
			t.Errorf("%s: an action sits inside a tag outside quotes", file)
		}
		if _, err := template.New("t").Parse(src); err != nil {
			t.Errorf("%s does not parse: %v", file, err)
		}
	}
}

func render(t *testing.T, file, name string, content map[string]any) string {
	t.Helper()
	tpl, err := template.ParseFiles(file)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := tpl.ExecuteTemplate(&sb, name, map[string]any{"Content": content}); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func TestDirectorySectionRenders(t *testing.T) {
	cardsJSON := func(cards ...pluginCard) []any {
		var out []any
		for _, c := range cards {
			out = append(out, structToMap(t, c))
		}
		return out
	}
	html := render(t, "sections/registry_directory.html", "section-registry_directory", map[string]any{
		"state": "ok", "linked": true, "list_url": "/plugins",
		"cards": cardsJSON(pluginCard{Name: "tagger", Title: "<b>Tagger</b>", Version: "v1.0.0", TierLabel: "Community", Author: "acme", Description: "Tags", Source: "https://example.org/x", PermCount: 1, PermLines: []permLine{{Name: "kv", Text: "Keep"}}}),
	})
	for _, want := range []string{`href="/plugins/tagger"`, "&lt;b&gt;Tagger&lt;/b&gt;", "v1.0.0", "Community", "acme", "Permissions (1)", `href="https://example.org/x"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %s", want, html)
		}
	}
	if !strings.Contains(render(t, "sections/registry_directory.html", "section-registry_directory", map[string]any{"state": "empty"}), "Nothing listed yet.") {
		t.Error("empty state text")
	}
	if !strings.Contains(render(t, "sections/registry_directory.html", "section-registry_directory", map[string]any{"state": "unavailable"}), "unavailable right now") {
		t.Error("unavailable state text")
	}
}

func TestDetailSectionRenders(t *testing.T) {
	html := render(t, "sections/registry_detail.html", "section-registry_detail", map[string]any{
		"type": "plugin", "list_name": "Plugins", "list_url": "/plugins",
		"plugin": structToMap(t, pluginCard{Name: "tagger", Version: "v1.0.0", About: []string{"one", "two"}, Features: []string{"fast"}, PermCount: 1, PermLines: []permLine{{Name: "site.scripts", Text: "Runs JS", Warn: true}}}),
	})
	for _, want := range []string{`<a href="/plugins">Plugins</a>`, "<h1>tagger</h1>", "<p>one</p>", "<p>two</p>", "<li>fast</li>", "<strong>Warning:</strong>", "Runs JS"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %s", want, html)
		}
	}
	html = render(t, "sections/registry_detail.html", "section-registry_detail", map[string]any{
		"type": "theme", "list_name": "Themes", "list_url": "/themes",
		"theme": structToMap(t, themeCard{Name: "boreal", Title: "Boreal", Screenshot: "https://hatamoto.dev/screenshots/boreal.png"}),
	})
	for _, want := range []string{`<a href="/themes">Themes</a>`, "<h1>Boreal</h1>", `src="https://hatamoto.dev/screenshots/boreal.png"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %s", want, html)
		}
	}
}

// structToMap is the JSON round trip the host does to a block's content.
func structToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
