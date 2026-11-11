package main

import (
	"html"
	"strings"

	"hatamoto.dev/sdk"
)

// defaultTerms is the draft shown while the owner has not written terms. It is
// the same text as the terms_markdown default in plugin.json.
const defaultTerms = `# Developer terms

Developer accounts are free while we grow. We may introduce a fee later and will tell you in advance before it applies to you.

# Listings

We may refuse, hide or remove any listing at our discretion, for example when it is unsafe, misleading or abandoned.

# Your code

You are responsible for the code you publish and for having the right to publish it. Keep your signing key private.

# No legal claims

These terms are a plain draft and do not create any legal claim against the site owner. Questions can go to the review contact shown on this page.`

// handleTerms is the public terms page. Every character of the owner's text is
// escaped: there is no raw HTML and no link syntax.
func handleTerms() sdk.HTTPResponse {
	st, err := loadSettings()
	if err != nil {
		return text(503, "try again later")
	}
	return htmlPage(200, renderTerms(st.TermsMarkdown, st.ReviewEmail))
}

func renderTerms(markdown, reviewEmail string) string {
	if strings.TrimSpace(markdown) == "" {
		markdown = defaultTerms
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Developer terms</title>` +
		`<style>body{font:16px/1.6 system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem}h2{margin-top:2rem}</style></head><body>` +
		`<h1>Developer terms</h1>`)
	var para []string
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + strings.Join(para, "<br>") + "</p>")
			para = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "# "):
			flush()
			b.WriteString("<h2>" + html.EscapeString(strings.TrimSpace(line[2:])) + "</h2>")
		default:
			para = append(para, html.EscapeString(line))
		}
	}
	flush()
	if e := strings.TrimSpace(reviewEmail); e != "" {
		b.WriteString("<p>Contact: " + html.EscapeString(e) + "</p>")
	}
	b.WriteString(`<p><a href="/developers">Back to developer sign-in</a></p></body></html>`)
	return b.String()
}
