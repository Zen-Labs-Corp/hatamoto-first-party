package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
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
// from strings, no navigation.
func TestUIHasNoInlineCodeOrMarkupInjection(t *testing.T) {
	banned := []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function",
		" style=", `setAttribute("style"`, `setAttribute('style'`, ".style.", ".style[", "cssText",
		"onclick=", "onload=", "onchange=", "<style", "<a ", "javascript:", "fetch(", "XMLHttpRequest",
	}
	for _, name := range []string{"index.html", "app.js", "app.css"} {
		src := readUI(t, name)
		for _, b := range banned {
			if strings.Contains(src, b) {
				t.Errorf("ui/%s contains %q", name, b)
			}
		}
	}
	for _, name := range []string{"index.html", "app.css"} {
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
	if strings.Contains(html, "<h1") {
		t.Error("index.html repeats the card title in an h1")
	}
}

// The page shows the texts of the brief's client view.
func TestUIShowsClientViewTexts(t *testing.T) {
	html := readUI(t, "index.html")
	for _, want := range []string{
		"All time", "Last 30 days", "Trend (30 days)", ">File<", ">Size<", ">Total<", ">Previous<", ">Next<",
		"No files in the media library yet. Upload a file, then link to it with /p/downloads/f/&lt;file id&gt;.",
		"Link format: /p/downloads/f/&lt;file id&gt;. Bots are counted only when Settings &gt; Count bots is on.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	js := readUI(t, "app.js")
	for _, want := range []string{`"Copy link"`, `"Copied"`, "navigator.clipboard.writeText", "field.select()", "Files ${from}-"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

// Core serves no stable design-token stylesheet to plugin pages, so every
// colour variable carries a literal fallback.
func TestUIColoursHaveFallbacks(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "tokens.css") {
		t.Error("index.html links a tokens stylesheet core does not serve")
	}
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "app.css")); bare != "" {
		t.Errorf("ui/app.css uses %q without a fallback", bare)
	}
}

// The manifest keeps the page and the overview route owner only and asks for
// exactly the permissions the plugin needs.
func TestManifestPermissionsAndOwnerOnlyRoutes(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Permissions []string       `json:"permissions"`
		UI          map[string]any `json:"ui"`
		Routes      []struct {
			Method, Path string
			Public       bool
			Roles        []string
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	want := []string{"media.read", "db", "builder.data", "settings"}
	if !slices.Equal(m.Permissions, want) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must stay absent (owner only)")
	}
	for _, r := range m.Routes {
		if r.Path == "/overview" && (r.Public || len(r.Roles) != 0) {
			t.Fatalf("overview route %+v must be owner only", r)
		}
	}
}
