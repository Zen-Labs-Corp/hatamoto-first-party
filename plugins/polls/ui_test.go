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
// from strings, no navigation, no network.
func TestUIHasNoInlineCodeOrMarkupInjection(t *testing.T) {
	banned := []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function",
		" style=", `setAttribute("style"`, `setAttribute('style'`, ".style.", ".style[", "cssText",
		"onclick=", "onload=", "onchange=", "<style", "<a ", "javascript:", "fetch(", "XMLHttpRequest",
		"window.open", "location.", "@import",
	}
	for _, name := range []string{"index.html", "app.js", "style.css"} {
		src := readUI(t, name)
		for _, b := range banned {
			if strings.Contains(src, b) {
				t.Errorf("ui/%s contains %q", name, b)
			}
		}
	}
	for _, name := range []string{"index.html", "style.css"} {
		if m := regexp.MustCompile(`https?://[^"')\s]*`).FindString(readUI(t, name)); m != "" {
			t.Errorf("ui/%s loads or links %q", name, m)
		}
	}
}

func TestIndexLoadsOnlyBridgeAndApp(t *testing.T) {
	html := readUI(t, "index.html")
	var srcs []string
	for _, m := range regexp.MustCompile(`<script[^>]*>`).FindAllString(html, -1) {
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

// Core's plugin card already shows ui.title, so the page has no heading of its own.
func TestUIHasNoDuplicateTitle(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "<h1") {
		t.Error("index.html repeats the card title in an h1")
	}
}

// Core serves no stable design-token stylesheet to plugin pages, so every
// colour variable carries a literal fallback.
func TestUIColoursHaveFallbacks(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "tokens.css") {
		t.Error("index.html links a tokens stylesheet core does not serve")
	}
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "style.css")); bare != "" {
		t.Errorf("ui/style.css uses %q without a fallback", bare)
	}
}

// The sandboxed frame cannot download, so the CSV is shown in a read-only box.
func TestUICSVBoxIsReadOnlyAndButtonsExist(t *testing.T) {
	if !regexp.MustCompile(`<textarea[^>]*\breadonly\b`).MatchString(readUI(t, "index.html")) {
		t.Error("the CSV box is not read-only")
	}
	js := readUI(t, "app.js")
	for _, want := range []string{`"Recount"`, `"Export CSV"`, "/recount", "/csv", "No forms set up yet. Add one under Settings > forms.", "Score distribution", "Average "} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
}

func TestUITitleMatchesManifest(t *testing.T) {
	if m := loadManifest(t); m.UI == nil || m.UI.Title != "Polls" {
		t.Fatalf("ui %+v", m.UI)
	}
}
