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
// publishing the change makes an event public.
func TestUITellsEventsArriveAsDrafts(t *testing.T) {
	html := readUI(t, "index.html")
	if strings.Count(html, "draft") < 2 {
		t.Error("the Submissions and Imports notes must say events arrive as drafts")
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(readme), "drafts") < 2 {
		t.Error("README must say imported and approved events arrive as drafts")
	}
}

// Scheduled runs publish up to 100 new events each, twice a day. Core does not
// show a finished run's message, so the Imports tab must not promise it.
func TestUIExplainsScheduledImports(t *testing.T) {
	html := readUI(t, "index.html")
	for _, want := range []string{"100 new events", "200 a day", "Automatic updates tab", "Run now", "before the next scheduled run"} {
		if !strings.Contains(html, want) {
			t.Errorf("ui/index.html lacks %q", want)
		}
	}
	for _, stale := range []string{"first 100", "running again", "message on the Runs tab", "later core update", "only when you press"} {
		if strings.Contains(html, stale) {
			t.Errorf("ui/index.html still says %q", stale)
		}
	}
}

// The page ships one stylesheet of its own: the frame cannot reach the admin
// tokens, so app.css carries them.
func TestUIHasOneOwnStylesheet(t *testing.T) {
	links := regexp.MustCompile(`<link[^>]*>`).FindAllString(readUI(t, "index.html"), -1)
	if len(links) != 1 || !strings.Contains(links[0], `href="app.css"`) {
		t.Fatalf("links %v", links)
	}
}

func TestUIEventsWorkspace(t *testing.T) {
	html := readUI(t, "index.html")
	for _, want := range []string{"Upcoming", "Past", "Drafts", "Submissions", "Imports", "Repeating", "No venue set", "iCal feed", "New event"} {
		if !strings.Contains(html, want) {
			t.Errorf("ui/index.html lacks %q", want)
		}
	}
	for _, bad := range []string{"<h1", "below", "above"} {
		if strings.Contains(html, bad) {
			t.Errorf("ui/index.html contains %q", bad)
		}
	}
	css := readUI(t, "app.css")
	for _, want := range []string{"prefers-color-scheme: dark", "color-scheme: light dark"} {
		if !strings.Contains(css, want) {
			t.Errorf("ui/app.css lacks %q", want)
		}
	}
	for _, bad := range []string{"100vh", "100vw", "position: fixed"} {
		if strings.Contains(css, bad) {
			t.Errorf("ui/app.css contains %q", bad)
		}
	}
}
