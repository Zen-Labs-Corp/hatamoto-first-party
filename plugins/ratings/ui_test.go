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
		" style=", `setAttribute("style"`, ".style.", "cssText", "onclick=", "onload=",
		"<style", "<a ", "javascript:", "fetch(", "XMLHttpRequest", "window.open", "location.", "@import",
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

// Core's plugin card already shows ui.title, so the page has no heading of its own.
func TestUIHasNoDuplicateTitle(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "<h1") {
		t.Error("index.html repeats the card title in an h1")
	}
}

func TestUIShowsTheColumnsAndTheEmptyState(t *testing.T) {
	index := readUI(t, "index.html")
	for _, want := range []string{"Page", "Average", "Ratings", `id="rows"`, "No ratings yet. Add the form partial to your theme to collect them."} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	js := readUI(t, "app.js")
	for _, want := range []string{"textContent", `"/overview"`, "page.path", "page.dist"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

func TestUITitleMatchesManifest(t *testing.T) {
	if m := loadManifest(t); m.UI == nil || m.UI.Title != "Star ratings" {
		t.Fatalf("ui %+v", m.UI)
	}
}
