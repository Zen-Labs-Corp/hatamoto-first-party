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
// from strings, no links (navigating the frame cuts the bridge).
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
	}
	for _, name := range []string{"index.html", "style.css"} {
		if m := regexp.MustCompile(`https?://[^"')\s]*`).FindString(readUI(t, name)); m != "" {
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

// The page follows the Client view: tabs with counts, the table columns, the
// states and the copy fallbacks.
func TestUIClientView(t *testing.T) {
	js, html := readUI(t, "app.js"), readUI(t, "index.html")
	for _, want := range []string{
		"hatamoto.route(method", `"/links"`, `hatamoto.host("permissions"`, "Copy editor path", "/entities/", "Recheck", "Un-ignore", "Ignore",
		"Load more" /* in html */, "Last scan", "Queue:", "No broken links found. Checked ", "First scan still running: checked ", "The database is not ready",
		"Permissions not granted", "execCommand", "navigator.clipboard.writeText", "Press Ctrl/Cmd+C to copy the selected text.",
		"textContent", "broken since", "checked ", "more pages", "page_count",
	} {
		if !strings.Contains(js, want) && !strings.Contains(html, want) {
			t.Errorf("ui lacks %q", want)
		}
	}
	for _, col := range []string{"Status", "Link", "Found on"} {
		if !strings.Contains(html, "<th>"+col+"</th>") {
			t.Errorf("index.html lacks column %q", col)
		}
	}
	if !strings.Contains(readUI(t, "style.css"), "@media (max-width: 640px)") {
		t.Error("no small screen layout")
	}
}

// The host drops requests sent before the bridge handshake.
func TestAppWaitsForBridgeBeforeHostCalls(t *testing.T) {
	js := readUI(t, "app.js")
	ready := strings.Index(js, "await hatamoto.ready")
	call := strings.Index(js, "await missingPermissions()")
	if ready < 0 || call < 0 || ready > call {
		t.Fatalf("app.js must await hatamoto.ready before its first bridge call (ready=%d call=%d)", ready, call)
	}
}
