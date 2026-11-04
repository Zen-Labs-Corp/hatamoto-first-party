package main

import (
	"html"
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"
)

const pageStyle = `body{font:16px/1.5 system-ui,sans-serif;margin:0;padding:2rem 1rem;color:#1a1a1a;background:#fff}
main{max-width:40rem;margin:0 auto}h1{font-size:1.5rem;margin:0 0 .25rem}h2{font-size:1.05rem;margin:1.5rem 0 .5rem}
.meta{color:#666;margin:0}.row{display:flex;align-items:center;gap:.75rem;margin:.35rem 0}
.lab{flex:0 0 28%;overflow-wrap:anywhere}.track{flex:1;background:#eee;border-radius:3px;height:.9rem;overflow:hidden}
.fill{display:block;height:100%;background:#2a6f97}.num{flex:0 0 5.5rem;text-align:right;color:#444}`

// page renders an HTML document. Every dynamic string must already be escaped;
// the title is escaped here.
func page(status int, title, body string) sdk.HTTPResponse {
	doc := "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">" +
		"<title>" + html.EscapeString(title) + "</title><style>" + pageStyle + "</style><body><main>" + body + "</main></body></html>"
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "text/html; charset=utf-8"}, Body: doc}
}

// notAvailable is the one answer for every reason results are not shown, so a
// visitor cannot tell a hidden form from an unknown one.
func notAvailable() sdk.HTTPResponse {
	return page(http.StatusNotFound, "Results are not available", "<h1>Results are not available.</h1>")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// resultsHTML lays out the results: questions and bars, no per-person data.
func resultsHTML(res formResult) string {
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(res.Name) + "</h1><p class=\"meta\">" + plural(res.Responses, "response", "responses") + "</p>")
	for _, q := range res.Questions {
		b.WriteString("<section><h2>" + html.EscapeString(q.Label) + "</h2>")
		for _, o := range q.Options {
			p := strconv.Itoa(o.Percent)
			b.WriteString("<div class=\"row\"><span class=\"lab\">" + html.EscapeString(o.Label) + "</span>" +
				"<span class=\"track\"><span class=\"fill\" style=\"width:" + p + "%\"></span></span>" +
				"<span class=\"num\">" + p + "% (" + strconv.Itoa(o.Count) + ")</span></div>")
		}
		b.WriteString("</section>")
	}
	if res.Kind == kindQuiz && res.Responses > 0 {
		b.WriteString("<p class=\"meta\">Average score " + strconv.FormatFloat(res.Average, 'f', 1, 64) + " of " + strconv.Itoa(res.MaxScore) + "</p>")
	}
	return b.String()
}

// handlePublic serves the results page of a form the owner chose to show.
func handlePublic(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	cfgs, err := loadConfig()
	if err != nil {
		return page(http.StatusServiceUnavailable, "Results are unavailable", "<h1>Results are temporarily unavailable.</h1>"), nil
	}
	cfg, ok := findCfg(cfgs, r.PathValues["form_slug"])
	if !ok || !cfg.public(now()) {
		return notAvailable(), nil
	}
	forms, err := listForms()
	if err != nil {
		return page(http.StatusServiceUnavailable, "Results are unavailable", "<h1>Results are temporarily unavailable.</h1>"), nil
	}
	form, ok := forms[cfg.Slug]
	if !ok {
		return notAvailable(), nil
	}
	res, err := buildResults(cfg, form)
	if err != nil {
		_ = logf("warn", "polls: results page of %q failed: %v", cfg.Slug, err)
		return page(http.StatusServiceUnavailable, "Results are unavailable", "<h1>Results are temporarily unavailable.</h1>"), nil
	}
	return page(http.StatusOK, res.Name, resultsHTML(res)), nil
}
