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
// from strings (log rows hold platform text), no network, no storage, no
// navigation.
func TestUIHasNoInlineCodeOrMarkupInjection(t *testing.T) {
	banned := []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function",
		" style=", `setAttribute("style"`, ".style.", "cssText", "<style", "javascript:",
		"onclick=", "onload=", "onchange=", "onsubmit=", "<a ", "fetch(", "XMLHttpRequest",
		"localStorage", "sessionStorage", "document.cookie", "location.", "window.open",
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
	if !strings.Contains(html, `href="style.css"`) {
		t.Error("index.html lacks style.css")
	}
}

// The page carries the layout the owner was promised.
func TestIndexHasTheClientView(t *testing.T) {
	html := readUI(t, "index.html")
	for _, want := range []string{
		"Server-side conversions", `id="cards"`, `id="log"`, `id="filter"`, "Delivery log", "(last 200, kept 30 days)",
		"No deliveries yet.", "never passes the visitor's IP", "hashed email and phone plus the ad click ID",
		">All<", ">Google<", ">TikTok<", ">LinkedIn<",
		"<th>Time</th><th>Platform</th><th>Form</th><th>Submission</th><th>Status</th><th>Detail</th>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	js := readUI(t, "app.js")
	for _, want := range []string{
		"No forms selected in Settings: nothing is sent.", "Sending for forms:", "Set: ", "Reconnect needed", "Connected · token renews automatically",
		"Disconnect", `"Test"`, `"Reconnect"`, `"Connect"`, "Test OK", "…",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

// Core serves no stable design-token stylesheet to plugin pages, so the page
// links none and every colour variable carries a literal fallback.
func TestUIColoursHaveFallbacks(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "tokens.css") {
		t.Error("index.html links a tokens stylesheet")
	}
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "style.css")); bare != "" {
		t.Errorf("ui/style.css uses %q without a fallback", bare)
	}
	css := readUI(t, "style.css")
	for _, kind := range []string{"sent", "failed", "retrying", "skipped", "test"} {
		if !strings.Contains(css, ".badge."+kind) {
			t.Errorf("no style for the %s badge", kind)
		}
	}
}

func TestUICallsTheRoutes(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{"`/log?platform=${encodeURIComponent(platform)}`", `api("POST", "/test"`, "`/connect/${p}`", "`/disconnect/${p}`", "textContent", "hatamoto.ready"} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
}

func TestUITitleAndOwnerOnly(t *testing.T) {
	ui := loadManifest(t).UI
	if ui["title"] != "Server-side conversions" {
		t.Fatalf("ui = %v", ui)
	}
	if _, ok := ui["roles"]; ok {
		t.Fatal("ui.roles must stay absent (owner only)")
	}
}
