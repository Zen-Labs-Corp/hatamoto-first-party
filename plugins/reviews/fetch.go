package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// fetchTimeout is the cap for one request; a refresh makes up to three.
	fetchTimeout = 3500 * time.Millisecond
	// minCall is the least time left for which a request is started.
	minCall = time.Second
)

var errBudget = errors.New("out of time for this run; the rest continues next time")

type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// fetchJSON calls the host's http.fetch. It starts no request that cannot
// finish before deadline. A body the host cut at its 1 MiB cap is an error:
// parsing half a document only hides the cause. The returned error may still
// carry the host's wording; callers redact it before it is stored or logged.
func fetchJSON(method, rawURL string, headers map[string]string, deadline time.Time) (fetched, error) {
	left := deadline.Sub(now())
	if left < minCall {
		return fetched{}, errBudget
	}
	timeout := min(fetchTimeout, left)
	var res fetched
	err := callHost("http.fetch", map[string]any{
		"method": method, "url": rawURL, "headers": headers, "body": "", "timeout_ms": int(timeout / time.Millisecond),
	}, &res)
	if err == nil && res.Truncated {
		return res, errors.New("the answer is larger than the 1 MiB a plugin may receive")
	}
	return res, err
}

// redact replaces every secret, and its URL-escaped form, with ***.
func redact(msg string, secrets ...string) string {
	for _, s := range secrets {
		if s == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, s, "***")
		msg = strings.ReplaceAll(msg, url.QueryEscape(s), "***")
		msg = strings.ReplaceAll(msg, url.PathEscape(s), "***")
	}
	return msg
}

// hostErr is a host or transport error with the secrets removed.
func hostErr(err error, secrets ...string) error {
	if err == errBudget {
		return err
	}
	return errors.New(redact(err.Error(), secrets...))
}

// apiError reduces an error answer to its status and a short message. It never
// includes the request URL or headers. The secrets are removed before the
// message is shortened, so a cut can not leave part of one behind.
func apiError(status int, body string, secrets ...string) error {
	var api struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal([]byte(body), &api) == nil {
		msg = api.Error.Message
		if msg == "" {
			msg = api.Message
		}
	}
	msg = redact(strings.Join(strings.Fields(msg), " "), secrets...)
	if len(msg) > 200 {
		msg = strings.ToValidUTF8(msg[:200], "") + "..."
	}
	if msg == "" {
		msg = "no details"
	}
	return fmt.Errorf("HTTP %d: %s", status, msg)
}

// httpsURL returns s when it is an https URL with a host, else "".
func httpsURL(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return u.String()
}
