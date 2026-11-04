package main

import (
	"html/template"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func sectionSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("sections/reviews.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// stubs returns the plugin's own funcs only, as core's vet does: a base func
// such as fromJSON or dict is an unknown function and rejects the template.
func stubs(impl map[string]func(...any) (string, error)) template.FuncMap {
	m := template.FuncMap{}
	for _, n := range []string{"summary", "count", "field", "profile"} {
		f := impl[n]
		if f == nil {
			f = func(...any) (string, error) { return "", nil }
		}
		m["plugin_reviews_"+n] = f
	}
	return m
}

func parseSection(t *testing.T, impl map[string]func(...any) (string, error)) *template.Template {
	t.Helper()
	tpl, err := template.New("plugin").Funcs(stubs(impl)).Parse(sectionSource(t))
	if err != nil {
		t.Fatalf("section does not parse with the plugin's own funcs: %v", err)
	}
	return tpl
}

func TestSectionParsesWithOnlyOwnFuncs(t *testing.T) {
	tpl := parseSection(t, nil)
	if tpl.Lookup("section-reviews") == nil || tpl.Lookup("plugin/reviews/card") == nil {
		t.Fatal("section-reviews and plugin/reviews/card must be defined")
	}
}

var bannedMarkup = regexp.MustCompile(`(?i)<\s*/?\s*(script|svg|iframe|object|embed|link|meta|base|template|noscript|textarea|title|dialog|math|form)\b|\son\w+\s*=|javascript:|data:|vbscript:|srcdoc|formaction|http-equiv|xlink:href`)

func TestSectionMarkupRules(t *testing.T) {
	src := sectionSource(t)
	if m := bannedMarkup.FindString(src); m != "" {
		t.Fatalf("banned markup %q", m)
	}
	if strings.Contains(src, "<style") || strings.Contains(src, "http://") || strings.Contains(src, "https://") {
		t.Fatal("no inline style block and no remote URL expected")
	}
	// Mirror core's action-context rule: print actions only in text or fully
	// inside a quoted attribute value; control actions and template calls only in text.
	action := regexp.MustCompile(`\{\{-?\s*(\w+)?[^}]*\}\}`)
	control := map[string]bool{"if": true, "else": true, "end": true, "range": true, "with": true, "template": true, "define": true, "block": true}
	inTag, quote := false, byte(0)
	pos := 0
	for _, loc := range action.FindAllStringSubmatchIndex(src, -1) {
		walk := func(text string) {
			for i := 0; i < len(text); i++ {
				c := text[i]
				switch {
				case quote != 0:
					if c == quote {
						quote = 0
					}
				case inTag && (c == '"' || c == '\''):
					quote = c
				case inTag && c == '>':
					inTag = false
				case !inTag && c == '<' && i+1 < len(text) && (text[i+1] == '/' || text[i+1] >= 'a' && text[i+1] <= 'z'):
					inTag = true
				}
			}
		}
		walk(src[pos:loc[0]])
		word := ""
		if loc[2] >= 0 {
			word = src[loc[2]:loc[3]]
		}
		kind := src[loc[0]:loc[1]]
		isControl := control[word]
		switch {
		case isControl && (inTag || quote != 0):
			t.Fatalf("control action %s inside a tag", kind)
		case !isControl && inTag && quote == 0:
			t.Fatalf("print action %s in a tag but outside a quoted value", kind)
		}
		pos = loc[1]
	}
}

func TestSectionEmptyCacheRendersNothing(t *testing.T) {
	tpl := parseSection(t, map[string]func(...any) (string, error){
		"count": func(...any) (string, error) { return "0", nil },
	})
	var b strings.Builder
	if err := tpl.ExecuteTemplate(&b, "section-reviews", nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(b.String()) != "" {
		t.Fatalf("empty cache rendered %q", b.String())
	}
}

func TestSectionRendersGoogleReviews(t *testing.T) {
	reviews := []map[string]string{
		{"author": "Jane D.", "text": "Great", "date": "2026-10-30", "stars": "★★★★★", "source_label": "Google", "author_url": "https://example.test/jane"},
		{"author": "Hans", "text": "Gut", "date": "2026-10-20", "stars": "★★★★☆", "source_label": "Google"},
	}
	tpl := parseSection(t, map[string]func(...any) (string, error){
		"count": func(a ...any) (string, error) {
			if len(a) == 1 && a[0] == "trustpilot" {
				return "0", nil
			}
			return "2", nil
		},
		"summary": func(...any) (string, error) { return "4.8 from 132 Google reviews", nil },
		"profile": func(a ...any) (string, error) {
			if a[0] == "google" {
				return "https://maps.example.test/acme", nil
			}
			return "", nil
		},
		"field": func(a ...any) (string, error) {
			i, _ := a[0].(int)
			if i >= len(reviews) {
				return "", nil
			}
			return reviews[i][a[1].(string)], nil
		},
	})
	var b strings.Builder
	if err := tpl.ExecuteTemplate(&b, "section-reviews", nil); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Jane D.", `href="https://example.test/jane"`, "Hans", "★★★★★", "Great", "2026-10-30", "4.8 from 132 Google reviews",
		`href="https://maps.example.test/acme"`, "Reviews from Google"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Reviews from Trustpilot") {
		t.Fatalf("Trustpilot attribution without Trustpilot reviews:\n%s", out)
	}
	if n := strings.Count(out, `<article class="review">`); n != 2 {
		t.Fatalf("%d cards", n)
	}
}

func TestSectionHasTwelveCards(t *testing.T) {
	if n := strings.Count(sectionSource(t), `{{template "plugin/reviews/card"`); n != 12 {
		t.Fatalf("%d card calls, want 12", n)
	}
}

func TestManifestFuncsMatchTheTemplate(t *testing.T) {
	used := map[string]bool{}
	for _, m := range regexp.MustCompile(`plugin_reviews_(\w+)`).FindAllStringSubmatch(sectionSource(t), -1) {
		used[m[1]] = true
	}
	var want []string
	for n := range used {
		want = append(want, n)
	}
	sort.Strings(want)
	have := append([]string(nil), loadManifest(t).Funcs...)
	sort.Strings(have)
	if strings.Join(want, ",") != strings.Join(have, ",") {
		t.Fatalf("template uses %v, manifest lists %v", want, have)
	}
}
