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
// from strings, no navigation, no direct network calls.
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

func TestUIHasMappingAndLogSections(t *testing.T) {
	html := readUI(t, "index.html")
	for _, want := range []string{
		"Not set up yet: choose a provider and enter its credentials in Settings.",
		"Test connection", "Field mapping", "(default mapping in use)",
		"<th>Form field</th><th>CRM field</th>", "+ Add row", "Save mapping",
		"Email always goes to the email field.",
		"CRM fields could not be loaded: test the connection first.",
		"Recent deliveries (last 100, kept 30 days, no form values)",
		`<th>Time</th><th>Form</th><th>Status</th><th class="num">Code</th><th>Message</th><th>Record</th>`,
		"No deliveries yet.",
		`id="status"`, `id="setup"`, `id="test"`, `id="test-result"`, `id="map"`, `id="map-default"`,
		`id="map-note"`, `id="add"`, `id="save"`, `id="log"`, `id="empty"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	js := readUI(t, "app.js")
	for _, want := range []string{
		`api("GET", "/log")`, `api("GET", "/map")`, `api("GET", "/fields")`, `api("PUT", "/map"`, `api("POST", "/test"`,
		"createElement", "textContent", "noopener",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

func TestUINeverHandlesSecrets(t *testing.T) {
	for _, name := range []string{"app.js", "index.html"} {
		src := readUI(t, name)
		for _, secret := range []string{"access_token", "api_token", "client_secret"} {
			if strings.Contains(src, secret) {
				t.Errorf("ui/%s mentions %q: the page must never handle credentials", name, secret)
			}
		}
	}
}

func TestUILinkHostsAreRestricted(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{".hubspot.com", ".pipedrive.com", ".salesforce.com", ".force.com", `"https:"`} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

func TestUIColoursHaveFallbacks(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "tokens.css") {
		t.Error("index.html links a tokens stylesheet core does not serve")
	}
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "style.css")); bare != "" {
		t.Errorf("ui/style.css uses %q without a fallback", bare)
	}
}

func TestUIRolesNotSet(t *testing.T) {
	if _, ok := loadManifest(t).UI["roles"]; ok {
		t.Fatal("ui.roles must stay absent (owner only)")
	}
}
