package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readUI(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("ui/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The admin page runs under a strict CSP: no inline script or style, no HTML
// from strings, no navigation, no network of its own.
func TestUIHasNoInlineCodeOrMarkupInjection(t *testing.T) {
	banned := []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function",
		" style=", `setAttribute("style"`, `setAttribute('style'`, ".style.", ".style[", "cssText",
		"onclick=", "onload=", "onchange=", "<style", "<a ", "javascript:", "fetch(", "XMLHttpRequest",
	}
	for _, name := range []string{"index.html", "app.js", "style.css"} {
		src := readUI(t, name)
		for _, b := range banned {
			if strings.Contains(src, b) {
				t.Errorf("ui/%s contains %q", name, b)
			}
		}
		if m := regexp.MustCompile(`https?://[^"')\s]*`).FindString(src); m != "" {
			t.Errorf("ui/%s loads or links %q", name, m)
		}
	}
}

func TestIndexLoadsOnlyBridgeAndApp(t *testing.T) {
	var srcs []string
	for _, m := range regexp.MustCompile(`<script[^>]*>`).FindAllString(readUI(t, "index.html"), -1) {
		s := regexp.MustCompile(`src="([^"]*)"`).FindStringSubmatch(m)
		if s == nil {
			t.Fatalf("inline script %q", m)
		}
		srcs = append(srcs, s[1])
	}
	if len(srcs) != 2 || srcs[0] != "/plugin-ui/_sdk/bridge.js" || srcs[1] != "app.js" {
		t.Fatalf("scripts %v", srcs)
	}
}

// The page has the not-set-up state and the cards of the Client view; the
// not-set-up notice sits outside the cards, which start hidden.
func TestIndexHasTheClientViewCards(t *testing.T) {
	html := readUI(t, "index.html")
	for _, want := range []string{
		`<p id="not-set" class="notice" hidden>Add your Bing API key in Settings.</p>`,
		`<div id="cards" hidden>`,
		`id="connection"`, `id="traffic"`, `id="top"`, `id="crawl"`, `id="sitemap"`,
		`id="test"`, `id="import-now"`, `id="submit"`, `id="status-line"`,
		`id="days"`, `id="queries"`, `id="pages"`, `id="issues"`, `id="feeds"`,
		"Bing reports no crawl issues.", "last 4 weeks",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	if strings.Contains(html, "<h1") {
		t.Error("index.html repeats the card title in an h1")
	}
	if strings.Index(html, `id="not-set"`) > strings.Index(html, `id="cards"`) {
		t.Error("the not-set notice must come before the cards")
	}
}

// The page talks to the plugin through these four routes and nothing else.
func TestUICallsOnlyThePluginRoutes(t *testing.T) {
	js := readUI(t, "app.js")
	got := map[string]bool{}
	for _, m := range regexp.MustCompile(`api\("([A-Z]+)", "([^"]+)"`).FindAllStringSubmatch(js, -1) {
		got[m[1]+" "+m[2]] = true
	}
	for _, want := range []string{"GET /overview", "POST /test", "POST /submit-sitemap", "POST /import-now"} {
		if !got[want] {
			t.Errorf("app.js never calls %s", want)
		}
		delete(got, want)
	}
	if len(got) != 0 {
		t.Errorf("app.js calls other routes: %v", got)
	}
	if strings.Count(js, "hatamoto.route(") != 1 || strings.Contains(js, "hatamoto.host") {
		t.Error("the page must reach the plugin only through hatamoto.route")
	}
	if !strings.Contains(js, "o.api_key_set") || !strings.Contains(js, "More to import, run again or wait for the next scheduled import.") {
		t.Error("not-set state or partial import text missing")
	}
}

// Every colour variable carries a literal fallback, so the page stays readable
// when no design tokens are loaded.
func TestUIColoursHaveFallbacks(t *testing.T) {
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "style.css")); bare != "" {
		t.Errorf("ui/style.css uses %q without a fallback", bare)
	}
}

func TestUIRolesNotSet(t *testing.T) {
	if _, ok := loadManifest(t).UI["roles"]; ok {
		t.Fatal("ui.roles must stay absent (owner only)")
	}
}
