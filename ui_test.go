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
	for _, rx := range []string{`https?://[^"')\s]*`} {
		for _, name := range []string{"index.html", "app.css"} {
			if m := regexp.MustCompile(rx).FindString(readUI(t, name)); m != "" {
				t.Errorf("ui/%s loads or links %q", name, m)
			}
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
	if !strings.Contains(html, "Stored: install ID, version, channel, day. No IP addresses.") {
		t.Fatal("storage footer missing")
	}
	if !strings.Contains(html, `<option value="30" selected>`) {
		t.Fatal("30 days must be the default")
	}
}

func TestUIRolesNotSet(t *testing.T) {
	if _, ok := loadManifest(t).UI["roles"]; ok {
		t.Fatal("ui.roles must stay absent (owner only)")
	}
}

// Long version labels are cut with an ellipsis; the full text goes in a <title>
// child set through textContent.
func TestUITruncatesLongLabelsWithATitle(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{"MAX_LABEL", "…", `svg("title", {}, text)`, "label(0, y + 15, b.label)"} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
	if strings.Contains(js, `class: "label" }, b.label`) {
		t.Error("bar labels are drawn untruncated")
	}
}

// The trend line, its axes and points carry literal colour fallbacks, so they
// stay visible when no design tokens are loaded.
func TestUITrendDrawsWithoutTokens(t *testing.T) {
	css := readUI(t, "app.css")
	for _, want := range []string{
		".chart .line { fill: none; stroke: var(--color-accent, #",
		".chart .axis { stroke: var(--color-hai, #",
		".chart .point { fill: var(--color-accent, #",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("ui/app.css lacks %q", want)
		}
	}
	if !strings.Contains(readUI(t, "app.js"), `class: "point"`) {
		t.Error("trend points are not drawn")
	}
}

// Core serves no stable design-token stylesheet to plugin pages, so the page
// links none and every colour variable carries a literal fallback.
func TestUIColoursHaveFallbacks(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "tokens.css") {
		t.Error("index.html links a tokens stylesheet core does not serve")
	}
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "app.css")); bare != "" {
		t.Errorf("ui/app.css uses %q without a fallback", bare)
	}
}

// The trend has one short month label per month, placed on the time axis.
func TestUITrendLabelsOncePerMonth(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{"function monthTicks", `month: "short"`, "MIN_GAP"} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
	if strings.Contains(js, "lastMonth") {
		t.Error("labels are placed at the first point of each month again")
	}
}

// Core's plugin card already shows ui.title, so the page has no heading of its own above the selector.
func TestUIHasNoDuplicateTitle(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "<h1") {
		t.Error("index.html repeats the card title in an h1")
	}
	if !strings.Contains(readUI(t, "app.css"), ".head { display: flex; justify-content: flex-end;") {
		t.Error("the window selector is not aligned right")
	}
}
