package main

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	// maxRequest is the cap for one feed request.
	maxRequest = 8 * time.Second
	// minCall is the least time left for which a request is started.
	minCall = time.Second
	// maxBody is the host's response cap; a longer answer arrives truncated.
	maxBody = 1 << 20
	// maxValidator bounds a stored ETag or Last-Modified value.
	maxValidator = 512
)

// feedErr is a per-feed failure. Its text is one of the fixed phrases below
// and is what the overview shows: it never holds a URL, a response body or a
// host error text.
type feedErr string

func (e feedErr) Error() string { return string(e) }

const (
	statusOK          = "ok"
	statusNotModified = "not modified"
	statusTooLarge    = feedErr("feed too large")
	statusTimeout     = feedErr("timeout")
	statusBlocked     = feedErr("blocked")
	statusInvalid     = feedErr("invalid feed")
	statusCharset     = feedErr("unsupported charset")
	statusFailed      = feedErr("fetch failed")
)

// fetchRes is the host's http.fetch answer.
type fetchRes struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	BodyB64   string            `json:"body_b64"`
	Truncated bool              `json:"truncated"`
}

// outcome is a successful fetch: either not modified, or a parsed feed with
// the validators for the next conditional request.
type outcome struct {
	NotModified  bool
	Feed         parsed
	ETag         string
	LastModified string
}

func (r fetchRes) header(name string) string {
	for k, v := range r.Headers {
		if strings.EqualFold(k, name) {
			return clipBytes(strings.TrimSpace(v), maxValidator)
		}
	}
	return ""
}

func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// fetchFeed downloads and parses one feed with a conditional GET when the
// stored state has validators. It starts no request that cannot finish
// before deadline. Every error is a feedErr.
func fetchFeed(f feedCfg, st feedRow, deadline time.Time) (outcome, error) {
	left := deadline.Sub(now())
	if left < minCall {
		return outcome{}, statusTimeout
	}
	headers := map[string]string{
		"Accept": "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.9, */*;q=0.5",
	}
	if st.ETag != "" {
		headers["If-None-Match"] = st.ETag
	}
	if st.LastModified != "" {
		headers["If-Modified-Since"] = st.LastModified
	}
	var res fetchRes
	err := callHost("http.fetch", map[string]any{
		"method": "GET", "url": f.URL, "headers": headers,
		"timeout_ms": int(min(maxRequest, left) / time.Millisecond),
	}, &res)
	if err != nil {
		return outcome{}, classify(err)
	}
	if res.Truncated {
		return outcome{}, statusTooLarge
	}
	if res.Status == 304 {
		return outcome{NotModified: true}, nil
	}
	if res.Status < 200 || res.Status > 299 {
		return outcome{}, feedErr("HTTP " + strconv.Itoa(res.Status))
	}
	body := []byte(res.Body)
	if res.BodyB64 != "" {
		var derr error
		if body, derr = base64.StdEncoding.DecodeString(res.BodyB64); derr != nil {
			return outcome{}, statusInvalid
		}
	}
	if len(body) > maxBody {
		return outcome{}, statusTooLarge
	}
	feed, err := parseFeed(body, f.URL)
	if err != nil {
		if errors.Is(err, errUnsupportedCharset) {
			return outcome{}, statusCharset
		}
		return outcome{}, statusInvalid
	}
	return outcome{Feed: feed, ETag: res.header("etag"), LastModified: res.header("last-modified")}, nil
}

// classify maps a host error to a fixed phrase, dropping its text.
func classify(err error) feedErr {
	var he *sdk.HostError
	if errors.As(err, &he) {
		switch he.Code {
		case sdk.ErrTimeout:
			return statusTimeout
		case sdk.ErrBlocked:
			return statusBlocked
		}
	}
	return statusFailed
}
