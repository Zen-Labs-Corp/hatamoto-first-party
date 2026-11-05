package main

import (
	"encoding/json"
	"errors"
	"time"

	"hatamoto.dev/sdk"
)

const (
	// callTimeout caps one provider request.
	callTimeout = 4 * time.Second
	// minCall is the least time left for which a request is started.
	minCall = 1500 * time.Millisecond
)

// budgetCode is the fetchResp code of a request that was not started because
// the time budget is spent. It is not a host error code.
const budgetCode = "budget_exhausted"

// budgetEnd, when set, is the time by which the running call must be done:
// no request starts that cannot finish before it, and a request timeout is cut
// to what is left.
var budgetEnd time.Time

// fetchReq is one provider request.
type fetchReq struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

// fetchResp is the answer: an HTTP status and body, or a host error code
// (Status 0). Raw host error text is never kept.
type fetchResp struct {
	Status int
	Body   string
	Code   string
}

// fetcher sends a request; doFetch in production, a fake in tests.
type fetcher func(fetchReq) fetchResp

// Result kinds.
const (
	kindCreated = "created"
	kindUpdated = "updated"
	kindRetry   = "retry"
	kindFail    = "fail"
	// kindBudget: the time budget ran out before the delivery was done. The
	// provider did not fail, so it is neither logged nor counted as an attempt.
	kindBudget = "budget"
)

// result is the outcome of one delivery. reason and message are fixed short
// words or sentences built here, never provider text.
type result struct {
	kind      string
	status    int
	reason    string
	message   string
	recordID  string
	recordURL string
	dropped   []string
}

// doFetch calls the host's http.fetch. Any HTTP status is an answer; a failed
// call keeps only the host error code.
func doFetch(req fetchReq) fetchResp {
	timeout := callTimeout
	if !budgetEnd.IsZero() {
		left := budgetEnd.Sub(now())
		if left < minCall {
			return fetchResp{Code: budgetCode}
		}
		timeout = min(timeout, left)
	}
	var res struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	err := callHost("http.fetch", map[string]any{
		"method": req.Method, "url": req.URL, "headers": req.Headers, "body": req.Body,
		"timeout_ms": int(timeout / time.Millisecond),
	}, &res)
	if err != nil {
		return fetchResp{Code: hostCode(err)}
	}
	return fetchResp{Status: res.Status, Body: res.Body}
}

func hostCode(err error) string {
	var he *sdk.HostError
	if errors.As(err, &he) && he.Code != "" {
		return he.Code
	}
	return sdk.ErrInternal
}

// hostResult maps a failed call to an outcome. Refusals by policy or a bad
// request never get better by waiting; everything else (timeout, unavailable,
// rate_limited, internal, unknown) is tried again later.
func hostResult(code string) result {
	switch code {
	case budgetCode:
		return result{kind: kindBudget, reason: "timeout"}
	case sdk.ErrCapabilityDenied, sdk.ErrBlocked:
		return result{kind: kindFail, reason: "blocked"}
	case sdk.ErrInvalidArgument:
		return result{kind: kindFail, reason: "rejected"}
	case sdk.ErrTimeout:
		return result{kind: kindRetry, reason: "timeout"}
	}
	return result{kind: kindRetry, reason: "network"}
}

// classify turns a non-2xx status into an outcome: 408, 429 and 5xx are tried
// again later, 401 and 403 mean the credentials are wrong, any other status is
// a rejection that retrying cannot fix.
func classify(status int) result {
	switch {
	case status == 408 || status == 429 || status >= 500:
		return result{kind: kindRetry, status: status}
	case status == 401 || status == 403:
		return result{kind: kindFail, status: status, reason: "unauthorized"}
	}
	return result{kind: kindFail, status: status, reason: "rejected"}
}

// failure is the outcome of an answer that is not a 2xx, or false for a 2xx.
func failure(resp fetchResp) (result, bool) {
	if resp.Code != "" {
		return hostResult(resp.Code), true
	}
	if resp.Status < 200 || resp.Status > 299 {
		return classify(resp.Status), true
	}
	return result{}, false
}

// jsonReq is a request with a JSON body (nil body for none).
func jsonReq(method, url string, headers map[string]string, body any) fetchReq {
	h := map[string]string{"Accept": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	r := fetchReq{Method: method, URL: url, Headers: h}
	if body != nil {
		b, _ := json.Marshal(body)
		r.Body = string(b)
		h["Content-Type"] = "application/json"
	}
	return r
}
