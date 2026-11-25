package main

import (
	"errors"
	"net/url"
	"regexp"
	"time"

	"hatamoto.dev/sdk"
)

// The provider hosts the plugin talks to, and the only hosts a buyer is sent
// to. They are the allowed_hosts of plugin.json plus the two checkout pages.
const (
	stripeAPI      = "https://api.stripe.com"
	paypalLiveAPI  = "https://api-m.paypal.com"
	paypalSandAPI  = "https://api-m.sandbox.paypal.com"
	callTimeoutMS  = 4000 // longest a single provider call may take
	minCallLeftMS  = 1500 // a call is not started with less time than this left
	maxFetchAnswer = 1 << 20
)

var apiHosts = map[string]bool{"api.stripe.com": true, "api-m.paypal.com": true, "api-m.sandbox.paypal.com": true}

var checkoutHosts = map[string]bool{"checkout.stripe.com": true, "www.paypal.com": true, "www.sandbox.paypal.com": true}

// idRe is what a provider id must match before it is put in a URL path.
var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

// Errors are fixed strings: they never carry a key, a token, a header, a URL
// or a response body, so they are safe to show or store.
var (
	errProviderDown      = errors.New("payment provider unreachable")
	errProviderRefused   = errors.New("payment provider refused the request")
	errProviderBadAnswer = errors.New("payment provider answer not understood")
	errNoTime            = errors.New("no time left for the payment provider")
)

// deadline is the time a request may still spend on provider calls. The zero
// value means no overall limit, only the per-call timeout.
type deadline time.Time

// newDeadline is the deadline d from now.
func newDeadline(d time.Duration) deadline { return deadline(now().Add(d)) }

// left is the milliseconds remaining, callTimeoutMS for the zero value.
func (d deadline) left() int {
	if time.Time(d).IsZero() {
		return callTimeoutMS
	}
	return int(time.Time(d).Sub(now()) / time.Millisecond)
}

// fetchJSON makes one http.fetch call and returns the status and body. A
// truncated answer is an error. The URL must be https on an API host.
func fetchJSON(method, rawURL string, headers map[string]string, body string, dl deadline) (int, []byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil || !apiHosts[u.Host] {
		return 0, nil, errProviderRefused
	}
	ms := dl.left()
	if ms < minCallLeftMS {
		return 0, nil, errNoTime
	}
	args := map[string]any{"method": method, "url": rawURL, "headers": headers, "timeout_ms": min(ms, callTimeoutMS)}
	if body != "" {
		args["body"] = body
	}
	var res struct {
		Status    int    `json:"status"`
		Body      string `json:"body"`
		Truncated bool   `json:"truncated"`
	}
	if err := callHost(sdk.FnHTTPFetch, args, &res); err != nil {
		return 0, nil, errProviderDown
	}
	if res.Truncated || len(res.Body) > maxFetchAnswer {
		return res.Status, nil, errProviderBadAnswer
	}
	return res.Status, []byte(res.Body), nil
}

// checkoutURLOK reports whether a URL a provider answered is one the buyer may
// be sent to: https, no user info, no port, and a known checkout host.
func checkoutURLOK(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Opaque != "" {
		return false
	}
	return checkoutHosts[u.Host]
}
