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
		" style=", `setAttribute("style"`, ".style.", ".style[", "cssText",
		"onclick=", "onload=", "<style", "<a ", "<img", "javascript:", "fetch(", "XMLHttpRequest",
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
	for _, id := range []string{`id="feeds"`, `id="fetch"`, `id="latest"`, `id="empty"`, `id="notice"`} {
		if !strings.Contains(html, id) {
			t.Errorf("index.html lacks %s", id)
		}
	}
	if strings.Contains(html, "tokens.css") {
		t.Error("core serves no stable token stylesheet to plugin pages")
	}
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "style.css")); bare != "" {
		t.Errorf("style.css uses %q without a fallback", bare)
	}
}

func TestUIMatchesTheClientView(t *testing.T) {
	html, js := readUI(t, "index.html"), readUI(t, "app.js")
	for _, want := range []string{"Fetch now", "Feeds are fetched every 30 minutes. Add or edit feeds in Settings.",
		`"Feed items"`, `"Feed group 1" .. "Feed group 7"`, "Latest items",
		"No feeds yet. Add an RSS or Atom feed URL in Settings.",
		"<th>Name</th><th>Group</th><th>Last fetch</th><th>Status</th>"} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	for _, want := range []string{"await hatamoto.ready", "hatamoto.route(", `"/overview"`, `"/fetch-now"`,
		"min ago", "Error: ", "button.disabled = true", "textContent"} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
}

func TestUIRolesNotSet(t *testing.T) {
	if _, ok := loadManifest(t).UI["roles"]; ok {
		t.Fatal("ui.roles must stay absent (owner only)")
	}
}
