package main

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

const (
	fetchTimeout = 8 * time.Second
	// minCall is the least time left for which a request is started.
	minCall = time.Second
)

// now is the clock; tests replace it.
var now = time.Now

// budgetEnd, when set, is the time by which the running call must be done:
// fetch never starts a request that cannot finish before it and cuts the
// request timeout to what is left. The plugin's call limit is 10 s.
var budgetEnd time.Time

var errBudget = errors.New("out of time for this call")

// errTimeout marks a request that did not finish in time.
var errTimeout = errors.New("request timed out")

// fetch calls the host's http.fetch. The Google API key travels in the
// X-Goog-Api-Key header, never in the URL (the host logs URLs). No error built
// here carries the request URL, and every error passes through redact. An answer cut at the host's 1 MiB cap is an error.
func fetch(key, method, rawURL, body string) (fetched, error) {
	timeout := fetchTimeout
	if !budgetEnd.IsZero() {
		left := budgetEnd.Sub(now())
		if left < minCall {
			return fetched{}, errBudget
		}
		timeout = min(timeout, left)
	}
	args := map[string]any{
		"method": method, "url": rawURL, "timeout_ms": int(timeout / time.Millisecond),
	}
	headers := map[string]string{"X-Goog-Api-Key": key}
	if body != "" {
		headers["Content-Type"] = "application/json"
		args["body"] = body
	}
	args["headers"] = headers
	var res fetched
	if err := callHost("http.fetch", args, &res); err != nil {
		return fetched{}, redact(err, key)
	}
	if res.Truncated {
		return res, errors.New("the answer is larger than the 1 MiB a plugin may receive")
	}
	return res, nil
}

// isTimeout reports whether err is the request running out of time.
func isTimeout(err error) bool {
	if errors.Is(err, errBudget) || errors.Is(err, errTimeout) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "timeout") || strings.Contains(m, "timed out") || strings.Contains(m, "deadline")
}

// redact returns err with the API key (plain and URL-escaped) replaced.
func redact(err error, key string) error {
	if err == nil || key == "" {
		return err
	}
	msg := err.Error()
	out := strings.ReplaceAll(strings.ReplaceAll(msg, key, "***"), url.QueryEscape(key), "***")
	if out == msg {
		return err
	}
	return errors.New(out)
}
