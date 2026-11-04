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
// from strings, no navigation, and no window.confirm (the sandboxed frame
// ignores it, so the page asks in its own panel).
func TestUIHasNoInlineCodeOrMarkupInjection(t *testing.T) {
	banned := []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function",
		" style=", `setAttribute("style"`, `setAttribute('style'`, ".style.", ".style[", "cssText",
		"onclick=", "onload=", "onchange=", "<style", "<a ", "<a>", "javascript:", "fetch(", "XMLHttpRequest",
		"confirm(", "alert(", "prompt(", "hatamoto.host",
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

// The page shows the log, its empty state and an in-page confirm panel that
// starts hidden.
func TestUIHasLogEmptyStateAndConfirmPanel(t *testing.T) {
	html := readUI(t, "index.html")
	for _, want := range []string{`id="log"`, "No purges yet.", `id="confirm"`, "Test connection", "Purge everything",
		"Choose a provider and add an API token in Settings."} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	if !regexp.MustCompile(`id="confirm"[^>]*\shidden`).MatchString(html) {
		t.Error("the confirm panel does not start hidden")
	}
}

// Core serves no stable design-token stylesheet to plugin pages, so the page
// links none and every colour variable carries a literal fallback.
func TestUIColoursHaveFallbacks(t *testing.T) {
	if strings.Contains(readUI(t, "index.html"), "tokens.css") {
		t.Error("index.html links a tokens stylesheet core does not serve")
	}
	if bare := regexp.MustCompile(`var\(--[a-z-]+\)`).FindString(readUI(t, "style.css")); bare != "" {
		t.Errorf("ui/style.css uses %q without a fallback", bare)
	}
}

func TestUISkippedRowIsNotShownAsEverything(t *testing.T) {
	js := readUI(t, "app.js")
	for _, want := range []string{"urlsCell(r)", `r.error === "set Site URL"`, "(nothing purged)"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

// The server error text already starts with the status, so the page shows it
// as it is and does not add the status a second time.
func TestUIDoesNotRepeatTheStatus(t *testing.T) {
	js := readUI(t, "app.js")
	for _, banned := range []string{`"no answer"`, ` error: ${r.error}`, `"No answer"`} {
		if strings.Contains(js, banned) {
			t.Errorf("app.js repeats the status: %q", banned)
		}
	}
}

// Long text must stay readable: the test result wraps, Time and Trigger do not
// break inside a word, URLs wrap at word boundaries (anywhere only when long)
// and "(everything)" stays in one piece.
func TestUILayoutKeepsTextReadable(t *testing.T) {
	css := readUI(t, "style.css")
	for _, want := range []string{
		"td.when, td.trigger, th { white-space: nowrap; }",
		"td.url { min-width: 16em; overflow-wrap: break-word; }",
		"td.url .long { overflow-wrap: anywhere; }",
		"td.url .none { white-space: nowrap; }",
		"#test-result { flex: 1 1 20em; min-width: 0; white-space: normal; overflow-wrap: anywhere; }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css lacks %q", want)
		}
	}
	if strings.Contains(css, "word-break: break-all") {
		t.Error("style.css breaks URLs at any character")
	}
	js := readUI(t, "app.js")
	for _, want := range []string{`className: "none"`, `"long"`, `className: "when"`, `className: "trigger"`} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

// Until the settings are complete (provider, token and target id) the two
// action buttons are disabled and the not-configured message is shown. They
// start disabled, so the page is never actionable before /log answers.
func TestUIDisablesActionsWhenNotConfigured(t *testing.T) {
	html := readUI(t, "index.html")
	for _, id := range []string{"test", "purge-all"} {
		if !regexp.MustCompile(`<button id="` + id + `"[^>]*\sdisabled`).MatchString(html) {
			t.Errorf("button #%s does not start disabled", id)
		}
	}
	js := readUI(t, "app.js")
	for _, want := range []string{`$("#test").disabled = !st.configured`, `$("#purge-all").disabled = !st.configured`, `$("#setup").hidden = st.configured`} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

func TestLogReportsNotConfiguredWithoutTargetID(t *testing.T) {
	for _, c := range []map[string]any{
		{"provider": "cloudflare", "zone_id": ""},
		{"provider": "", "api_token": testToken},
		{"api_token": ""},
	} {
		newFake(t, c)
		if res := serveRoute(t, "GET", "/log"); !strings.Contains(res.Body, `"configured":false`) {
			t.Errorf("%v: %s", c, res.Body)
		}
	}
}
