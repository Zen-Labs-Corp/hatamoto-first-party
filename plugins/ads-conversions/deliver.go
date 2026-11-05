package main

import (
	"errors"
	"strconv"
	"time"

	"hatamoto.dev/sdk"
)

// fetchTimeoutMS stays under the 10 s the host allows a cron run.
const fetchTimeoutMS = 8000

// minFetch is the least time left that a call is still started with.
const minFetch = time.Second

// budgetEnd is when the running cron job or route must stop; zero outside
// one. Calls shrink their timeout to what is left, so a run stays inside the
// host's 10 s limit.
var budgetEnd time.Time

// errBudget means too little time was left to start a call.
var errBudget = errors.New("out of time")

// startBudget gives the running job or route cronBudget from start; the
// returned func ends it.
func startBudget(start time.Time) func() {
	budgetEnd = start.Add(cronBudget)
	return func() { budgetEnd = time.Time{} }
}

// callTimeout is the timeout of the next call in milliseconds; ok is false
// when less than minFetch is left, and no call may be made.
func callTimeout() (ms int, ok bool) {
	if budgetEnd.IsZero() {
		return fetchTimeoutMS, true
	}
	left := budgetEnd.Sub(now())
	if left < minFetch {
		return 0, false
	}
	return int(min(left.Milliseconds(), fetchTimeoutMS)), true
}

// Outcome kinds.
const (
	kindSent   = "sent"   // the platform accepted the conversion
	kindFailed = "failed" // permanent: log it, delete the row, no retry
	kindRetry  = "retry"  // try again later with backoff
	kindAuth   = "auth"   // the connection is broken
)

// request is one HTTP call to a platform.
type request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// outcome is what one call came to. Code is the HTTP status or the platform's
// own code; Message is the platform's message cut to 300 characters. Neither
// ever holds a value, hash, click ID or token.
type outcome struct {
	Kind    string
	Reason  string // short code for the log: http, platform, network, truncated
	Code    string
	Message string
}

// fetched is the host's http.fetch answer.
type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// httpDo performs a request through the host's http.fetch; tests replace it.
var httpDo = realHTTPDo

func realHTTPDo(req request, timeoutMS int) (fetched, error) {
	args := map[string]any{"method": req.Method, "url": req.URL, "headers": req.Headers, "timeout_ms": timeoutMS}
	if len(req.Body) > 0 {
		args["body"] = string(req.Body)
	}
	var res fetched
	err := callHost("http.fetch", args, &res)
	return res, err
}

// classifyHTTP maps a bare HTTP status: 2xx sent, 401 auth, 429, 5xx and a
// truncated body retry, any other status fails for good.
func classifyHTTP(status int, truncated bool) outcome {
	code := strconv.Itoa(status)
	switch {
	case truncated:
		return outcome{Kind: kindRetry, Reason: "truncated", Code: code}
	case status >= 200 && status < 300:
		return outcome{Kind: kindSent, Code: code}
	case status == 401:
		return outcome{Kind: kindAuth, Reason: "http", Code: code}
	case status == 429 || status >= 500:
		return outcome{Kind: kindRetry, Reason: "http", Code: code}
	}
	return outcome{Kind: kindFailed, Reason: "http", Code: code}
}

// hostReason names why the host refused or failed a call by its stable error
// code, never by its free text.
func hostReason(err error) string {
	var he *sdk.HostError
	if errors.As(err, &he) && he.Code != "" {
		return he.Code
	}
	return sdk.ErrInternal
}

// send makes the call. With no time left it makes none and answers a retry
// with Reason "budget". A host or transport error becomes a retry (a refusal
// such as a missing grant heals once the owner fixes it); a truncated answer
// counts as a server error. The returned fetched has Status 0 on an error.
func send(req request) (fetched, outcome) {
	ms, ok := callTimeout()
	if !ok {
		return fetched{}, outcome{Kind: kindRetry, Reason: "budget", Code: "budget"}
	}
	f, err := httpDo(req, ms)
	if err != nil {
		return fetched{}, outcome{Kind: kindRetry, Reason: "network", Code: hostReason(err)}
	}
	return f, classifyHTTP(f.Status, f.Truncated)
}

// execute sends the request and lets the platform's classifier read the
// answer. Transport errors and truncated answers never reach it.
func execute(req request, classify func(status int, body []byte) outcome) outcome {
	f, o := send(req)
	if f.Status == 0 || f.Truncated {
		return o
	}
	return classify(f.Status, []byte(f.Body))
}
