package main

import (
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
// from strings, no navigation, no network.
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

func TestUIIndexLoadsOnlyBridgeAndApp(t *testing.T) {
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

// Core creates what a run stages as a draft, so the pages must not promise that

// This part ships exactly four tabs.
func TestUITabs(t *testing.T) {
	html := readUI(t, "index.html")
	var ids []string
	for _, m := range regexp.MustCompile(`<button id="(tab-[a-z]+)"`).FindAllStringSubmatch(html, -1) {
		ids = append(ids, m[1])
	}
	if want := []string{"tab-events", "tab-attendees", "tab-orders", "tab-settings"}; !slices.Equal(ids, want) {
		t.Errorf("tabs %v, want %v", ids, want)
	}
	if strings.Contains(readUI(t, "app.js"), "hatamoto.host") {
		t.Error("the page must not call hatamoto.host: editors get 403")
	}
}

// Every element the page script looks up by id is in the HTML, and the script
// never looks up an id twice in the markup.
func TestUIScriptIDsExistInTheMarkup(t *testing.T) {
	html, js := readUI(t, "index.html"), readUI(t, "app.js")
	have := map[string]int{}
	for _, m := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		have[m[1]]++
	}
	for id, n := range have {
		if n > 1 {
			t.Errorf("id %q is in the markup %d times", id, n)
		}
	}
	for _, m := range regexp.MustCompile(`\$\("#([a-z-]+)"\)`).FindAllStringSubmatch(js, -1) {
		if have[m[1]] == 0 {
			t.Errorf("app.js looks up #%s, which index.html lacks", m[1])
		}
	}
}
