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
		"navigator.clipboard",
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

func TestUICallsTheRoutesAndShowsTheAddresses(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{`api("GET", "/links")`, `api("GET", "/payments"`, "/api/v1/p/payment-links/payments.csv", "No payments yet.", "key_set"} {
		if !strings.Contains(js, want) {
			t.Errorf("ui/app.js lacks %q", want)
		}
	}
}

func TestUIRolesNotSet(t *testing.T) {
	if _, ok := loadManifest(t).UI["roles"]; ok {
		t.Fatal("ui.roles must stay absent (owner only)")
	}
}

// The page editor has no fields for the section, so the copy must not send the
// owner there.
func TestUICopyDoesNotPromiseASectionEditor(t *testing.T) {
	for _, name := range []string{"index.html", "app.js"} {
		if strings.Contains(strings.ToLower(readUI(t, name)), "page editor") {
			t.Errorf("ui/%s mentions the page editor", name)
		}
	}
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "in the page editor and set") {
		t.Error("README promises label and url fields in the page editor")
	}
}
