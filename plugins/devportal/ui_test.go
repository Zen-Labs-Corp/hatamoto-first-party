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
}

func TestUIUsesTheAdminRoutesAndEscapesText(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{
		"await hatamoto.ready", `"/admin/accounts"`, `"/admin/submissions"`, "/admin/listings/${l.id}/revoke", "/admin/accounts/${l.account}/revoke-key", "/admin/submissions/${s.id}/approve", "/admin/submissions/${s.id}/reject", "/admin/accounts/${row.id}/${action}",
		`"unsuspend"`, `"suspend"`, `setAttribute("title", keyId)`, "keyId.slice(0, 4)", "Developers (${data.total})", "textContent",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
}

func TestUIShowsThePublishWarning(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{"out.published === false", "out.warning", "scrollIntoView"} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
}

func TestUIColoursHaveFallbacks(t *testing.T) {
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "app.css")); bare != "" {
		t.Errorf("ui/app.css uses %q without a fallback", bare)
	}
}
