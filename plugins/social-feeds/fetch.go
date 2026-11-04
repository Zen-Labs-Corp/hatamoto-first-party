package main

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	fetchTimeout = 8 * time.Second
	minCall      = 500 * time.Millisecond
)

var errBudget = errors.New("out of time for this run")

// budgetEnd is the deadline of the current run; zero means none.
var budgetEnd time.Time

type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// fetch calls the host's http.fetch. A non-2xx status is not an error here; a
// transport failure or an answer cut at the host's 1 MiB cap is. Errors are
// redacted, since the URLs carry tokens and keys in their query strings.
func fetch(method, rawURL string, headers map[string]string, body string) (fetched, error) {
	timeout := fetchTimeout
	if !budgetEnd.IsZero() {
		left := budgetEnd.Sub(now())
		if left < minCall {
			return fetched{}, errBudget
		}
		timeout = min(timeout, left)
	}
	var res fetched
	err := callHost("http.fetch", map[string]any{
		"method": method, "url": rawURL, "headers": headers, "body": body, "timeout_ms": int(timeout / time.Millisecond),
	}, &res)
	if err != nil {
		return fetched{}, errors.New(redact(err.Error()))
	}
	if res.Truncated {
		return res, errors.New("the answer is larger than the 1 MiB a plugin may receive")
	}
	return res, nil
}

func ok2xx(status int) bool { return status >= 200 && status < 300 }

// shorten cuts s to at most n runes, adding an ellipsis when it cut.
func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}

// normalizeTime turns the timestamps of the two APIs (RFC 3339, or Instagram's
// "+0000" offset form) into UTC RFC 3339, or "" when unreadable.
func normalizeTime(s string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}
