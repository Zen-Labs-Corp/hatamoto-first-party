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
		"<style", "<a ", "javascript:", "fetch(", "XMLHttpRequest", "WebSocket", "localStorage", "window.open",
	}
	for _, name := range []string{"index.html", "app.js", "style.css"} {
		src := readUI(t, name)
		for _, b := range banned {
			if strings.Contains(src, b) {
				t.Errorf("ui/%s contains %q", name, b)
			}
		}
		if m := regexp.MustCompile(`(?i)\son[a-z]+\s*=`).FindString(src); m != "" {
			t.Errorf("ui/%s has an event handler attribute %q", name, m)
		}
	}
	// A placeholder may show an address; only src, href and url() load one.
	loads := regexp.MustCompile(`(?:src|href)="https?:|url\(\s*["']?(?:https?:|//)`)
	for _, name := range []string{"index.html", "style.css"} {
		if m := loads.FindString(readUI(t, name)); m != "" {
			t.Errorf("ui/%s loads an outside address: %q", name, m)
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
	if n := len(regexp.MustCompile(`<script[^>]*>\s*[^<\s]`).FindAllString(readUI(t, "index.html"), -1)); n != 0 {
		t.Fatalf("%d script tags with a body", n)
	}
	for _, m := range regexp.MustCompile(`<link[^>]*>`).FindAllString(readUI(t, "index.html"), -1) {
		if !strings.Contains(m, `href="style.css"`) {
			t.Fatalf("link %q", m)
		}
	}
}

func TestUIColoursHaveFallbacks(t *testing.T) {
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "style.css")); bare != "" {
		t.Errorf("ui/style.css uses %q without a fallback", bare)
	}
}

// Only the bridge's route call reaches the plugin, and only the declared routes.
func TestUIUsesOnlyDeclaredRoutesThroughTheBridge(t *testing.T) {
	js := readUI(t, "app.js")
	for _, m := range regexp.MustCompile(`hatamoto\.(\w+)`).FindAllStringSubmatch(js, -1) {
		if m[1] != "route" && m[1] != "ready" {
			t.Errorf("app.js uses hatamoto.%s", m[1])
		}
	}
	var manifest struct {
		Routes []struct{ Method, Path string } `json:"routes"`
	}
	b, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, r := range manifest.Routes {
		declared[r.Method+" "+r.Path] = true
	}
	used := map[string]bool{}
	for _, m := range regexp.MustCompile(`api\("(GET|POST)", "(/[a-z-]+)"`).FindAllStringSubmatch(js, -1) {
		used[m[1]+" "+m[2]] = true
		if !declared[m[1]+" "+m[2]] {
			t.Errorf("app.js calls %s %s, which plugin.json does not declare", m[1], m[2])
		}
	}
	for _, want := range []string{"GET /overview", "POST /monitors", "POST /check-now", "POST /test-alert"} {
		if !used[want] {
			t.Errorf("app.js never calls %s", want)
		}
	}
}

// Colours come from CSS classes: every badge and SSL class the script uses is
// defined in the stylesheet.
func TestUIBadgeClassesExistInTheStylesheet(t *testing.T) {
	js, css := readUI(t, "app.js"), readUI(t, "style.css")
	block := regexp.MustCompile(`const BADGES = \{([^}]*)\}`).FindStringSubmatch(js)
	if block == nil {
		t.Fatal("no BADGES table")
	}
	keys := regexp.MustCompile(`(\w+):`).FindAllStringSubmatch(block[1], -1)
	if len(keys) != 5 {
		t.Fatalf("badges %v", keys)
	}
	for _, k := range keys {
		if !strings.Contains(css, ".badge."+k[1]) {
			t.Errorf("style.css lacks .badge.%s", k[1])
		}
	}
	for _, cls := range []string{".ssl-warn", ".ssl-crit", ".cause.down", ".bar-img"} {
		if !strings.Contains(css, cls) {
			t.Errorf("style.css lacks %s", cls)
		}
	}
	for _, cls := range []string{"ssl-warn", "ssl-crit", "cause down", "bar-img"} {
		if !strings.Contains(js, cls) {
			t.Errorf("app.js never uses %q", cls)
		}
	}
}

// The page follows the Client view.
func TestUIClientView(t *testing.T) {
	js, html, css := readUI(t, "app.js"), readUI(t, "index.html"), readUI(t, "style.css")
	for _, want := range []string{
		"textContent", "Setup: add your site as the first monitor", "Permissions not granted", "The database is not ready",
		"Mail is not set up", "Check now", "Edit", "Confirm delete", "Heartbeat: set", "Heartbeat: not set", "ongoing",
		"This site", "Alerts: ", "invalid_emails", `startsWith(DATA_SVG)`, `"data:image/svg+xml"`, `setAttribute("src"`, "hatamoto.ready",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	for _, want := range []string{
		"<th>Monitor</th>", "<th>Status</th>", "<th>24h</th>", "<th>30d</th>", "<th>Avg</th>", "<th>Last check</th>", "<th>SSL</th>",
		"+ Add monitor", "Send test alert", "<h2>Incidents</h2>", `value="1m"`, `value="5m"`, `value="15m"`, "Slow above", "Keyword",
		"This site", "Save", "Cancel", "Delete", "checks run inside this site", "crt.sh", "heartbeat URL with an outside service",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	if !strings.Contains(css, "@media (max-width: 640px)") || !strings.Contains(css, "display: block") {
		t.Error("no small screen card layout")
	}
	if strings.Count(js, "ssl-crit") == 0 || !strings.Contains(js, "SSL_WARN_DAYS = 21") || !strings.Contains(js, "SSL_CRIT_DAYS = 7") {
		t.Error("SSL thresholds are not 21 and 7 days")
	}
}

// The host drops requests sent before the bridge handshake.
func TestAppWaitsForBridgeBeforeRouteCalls(t *testing.T) {
	js := readUI(t, "app.js")
	ready := strings.Index(js, "await hatamoto.ready")
	call := strings.Index(js, "await load();\n}")
	if ready < 0 || call < 0 || ready > call {
		t.Fatalf("app.js must await hatamoto.ready before its first route call (ready=%d call=%d)", ready, call)
	}
}
