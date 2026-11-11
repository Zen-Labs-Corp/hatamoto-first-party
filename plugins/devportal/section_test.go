package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func sdkGet(path string) sdk.HTTPRequest {
	return sdk.HTTPRequest{Method: "GET", Path: path, Pattern: path}
}

// The host refuses a whole plugin template pack that breaks its markup rules,
// so the section is checked against the same banned list here.
func TestSectionAvoidsBannedMarkup(t *testing.T) {
	raw, err := os.ReadFile("sections/devportal.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, bad := range []string{"<textarea", "<script", "<meta", "<iframe", "<svg", "<link", "<object", "<embed", "<base", "<noscript",
		"<dialog", "<template", "<title", "<math", "formaction", "http-equiv", "srcdoc", "xlink:href", "javascript:", "data:", "vbscript:"} {
		if strings.Contains(strings.ToLower(src), bad) {
			t.Errorf("section contains %q", bad)
		}
	}
	if regexp.MustCompile(`(?i)\son\w+\s*=`).MatchString(src) {
		t.Error("section has an on* attribute")
	}
	if !strings.Contains(src, `{{define "section-devportal"}}`) {
		t.Error("section does not define section-devportal")
	}
	// Every form is a same-site POST with a CSRF field.
	forms := regexp.MustCompile(`(?s)<form[^>]*>.*?</form>`).FindAllString(src, -1)
	if len(forms) < 4 {
		t.Fatalf("%d forms", len(forms))
	}
	for _, f := range forms {
		if !regexp.MustCompile(`method="post" action="/p/devportal/(account|key|logout)"`).MatchString(f) || !strings.Contains(f, `name="csrf" value="{{$c.csrf}}"`) {
			t.Errorf("form without a POST route or CSRF field: %.80s", f)
		}
	}
	// Printing actions sit in text or quoted attribute values only: no action
	// directly inside a tag outside quotes.
	if regexp.MustCompile(`<[a-z][^>"']*\{\{`).MatchString(src) {
		t.Error("an action sits inside a tag outside quotes")
	}
}
