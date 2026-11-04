package main

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"
)

// csvText neutralises a cell a spreadsheet could read as a formula.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// csvName reduces a slug to characters that are safe in a file name.
func csvName(slug string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(slug) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "poll"
	}
	return b.String()
}

// renderCSV writes form,question,option,count,percent: one row per option, and
// for quizzes one row per score ("score" as the question, the score as the option).
func renderCSV(res formResult) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	_ = w.Write([]string{"form", "question", "option", "count", "percent"})
	for _, q := range res.Questions {
		for _, o := range q.Options {
			_ = w.Write([]string{csvText(res.Slug), csvText(q.Label), csvText(o.Label), strconv.Itoa(o.Count), strconv.Itoa(o.Percent)})
		}
	}
	if res.Kind == kindQuiz {
		for _, s := range res.Scores {
			_ = w.Write([]string{csvText(res.Slug), "score", strconv.Itoa(s.Score), strconv.Itoa(s.Count), strconv.Itoa(percentOf(s.Count, res.Responses))})
		}
	}
	w.Flush()
	return buf.String(), w.Error()
}

func handleCSV(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	cfgs, err := loadConfig()
	if err != nil {
		_ = logf("warn", "polls: csv: settings unreadable")
		return fail(http.StatusServiceUnavailable, "settings unreadable")
	}
	cfg, ok := findCfg(cfgs, r.PathValues["slug"])
	if !ok {
		return fail(http.StatusNotFound, "form not set up")
	}
	forms, err := listForms()
	if err != nil {
		return fail(http.StatusServiceUnavailable, "forms unreadable")
	}
	form, ok := forms[cfg.Slug]
	if !ok {
		return fail(http.StatusNotFound, "form not found")
	}
	res, err := buildResults(cfg, form)
	if err != nil {
		_ = logf("warn", "polls: csv of %q failed: %v", cfg.Slug, err)
		return fail(http.StatusServiceUnavailable, "results unavailable")
	}
	body, err := renderCSV(res)
	if err != nil {
		return fail(http.StatusInternalServerError, "csv failed")
	}
	return sdk.HTTPResponse{Status: http.StatusOK, Body: body, Headers: map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": `attachment; filename="` + csvName(cfg.Slug) + `.csv"`,
	}}, nil
}
