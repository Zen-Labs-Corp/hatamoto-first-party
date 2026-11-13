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

// A run reads at most 100 events per feed and running again does not reach the
// rest, so the Imports tab and the README must say so. Core does not show a
// finished run's message, so neither may promise it. The Imports tab does not
// mention scheduled imports; the README Upgrading section does.
func TestUIAndReadmeExplainTheImportCap(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readUI(t, "index.html"), "later core update") {
		t.Error("the Imports tab footnote must not mention a later core update")
	}
	if _, upgrading, ok := strings.Cut(string(readme), "### Upgrading\n"); !ok || !strings.Contains(upgrading, "scheduled imports need a later core update") {
		t.Error("README Upgrading must keep the scheduled-imports note")
	}
	for name, text := range map[string]string{"ui/index.html": readUI(t, "index.html"), "README.md": string(readme)} {
		if !strings.Contains(text, "first 100") || !strings.Contains(text, "running again") {
			t.Errorf("%s does not explain the 100-event cap", name)
		}
		if strings.Contains(text, "message on the Runs tab") {
			t.Errorf("%s promises a run message the Runs tab does not show", name)
		}
	}
}
