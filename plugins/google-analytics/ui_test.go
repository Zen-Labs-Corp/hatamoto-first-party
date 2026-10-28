package main

import (
	"encoding/json"
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
		if m := regexp.MustCompile(`https?://[^"')\s]*`).FindString(readUI(t, name)); m != "" && !strings.HasPrefix(m, "https://your-site/") {
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
	for _, want := range []string{"https://your-site/#ga-exclude", "https://your-site/#ga-include", "readonly"} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
}

func TestUIRolesNotSet(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		UI map[string]any `json:"ui"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.UI == nil {
		t.Fatal("plugin.json has no ui block")
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must stay absent (owner only)")
	}
}

// Core serves no stable design-token stylesheet to plugin pages, so the page
// links none and every colour variable carries a literal fallback.
func TestUIColoursHaveFallbacks(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "tokens.css") {
		t.Error("index.html links a tokens stylesheet core does not serve")
	}
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "style.css")); bare != "" {
		t.Errorf("ui/style.css uses %q without a fallback", bare)
	}
}

// The page reads the grants and the setting through the bridge and has a
// fallback when the clipboard is blocked.
func TestUIStatesAndCopyFallbacks(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{"permissions", `"settings.get"`, "site.scripts", "execCommand", "Press Ctrl/Cmd+C", "r.ok", "Tag is ON", "Tag is OFF"} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
}

// The host drops requests sent before the bridge handshake, so the first
// host call must come after `await hatamoto.ready`.
func TestAppWaitsForBridgeBeforeHostCalls(t *testing.T) {
	js := readUI(t, "app.js")
	ready := strings.Index(js, "await hatamoto.ready")
	host := strings.Index(js, "hatamoto.host(")
	if ready < 0 || host < 0 || ready > host {
		t.Fatalf("app.js must await hatamoto.ready before the first hatamoto.host call (ready=%d host=%d)", ready, host)
	}
}
