package main

import (
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
