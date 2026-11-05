package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	// callTimeout caps one platform request.
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

// fetchReq is one platform request.
type fetchReq struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

// fetchResp is the answer: an HTTP status, body and requested wait, or a host
// error code (Status 0). Raw host error text is never kept.
type fetchResp struct {
	Code       string
	Status     int
	Body       string
	RetryAfter time.Duration
}

// fetchFn sends a request; doFetch in production, a fake in tests.
var fetchFn = doFetch

// doFetch calls the host's http.fetch. Any HTTP status is an answer; a failed
// call keeps only the host error code, because its text can hold the URL.
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
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	err := callHost("http.fetch", map[string]any{
		"method": req.Method, "url": req.URL, "headers": req.Headers, "body": req.Body,
		"timeout_ms": int(timeout / time.Millisecond),
	}, &res)
	if err != nil {
		return fetchResp{Code: hostCode(err)}
	}
	out := fetchResp{Status: res.Status, Body: res.Body, RetryAfter: retryAfter(res.Headers["retry-after"])}
	if res.Status == 429 && out.RetryAfter == 0 {
		out.RetryAfter = telegramRetryAfter(res.Body)
	}
	return out
}

// retryAfter reads a Retry-After header: seconds, or an HTTP date.
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now()), 0)
	}
	return 0
}

// telegramRetryAfter reads parameters.retry_after from a Telegram 429 answer,
// which carries the wait in the body rather than in a header.
func telegramRetryAfter(body string) time.Duration {
	var v struct {
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal([]byte(body), &v) != nil || v.Parameters.RetryAfter <= 0 {
		return 0
	}
	return time.Duration(v.Parameters.RetryAfter) * time.Second
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

// target is the URL to post to for a channel. It exists only in memory for
// the call and is never stored or logged. ok is false when the channel is not
// set up.
func target(c cfg, channel string) (string, bool) {
	if !c.isSet(channel) {
		return "", false
	}
	switch channel {
	case chanSlack:
		return strings.TrimSpace(c.SlackWebhookURL), true
	case chanTeams:
		return strings.TrimSpace(c.TeamsWorkflowURL), true
	case chanGChat:
		return strings.TrimSpace(c.GChatWebhookURL), true
	case chanTelegram:
		return "https://api.telegram.org/bot" + strings.TrimSpace(c.TelegramBotToken) + "/sendMessage", true
	}
	return "", false
}

// post sends a rendered body to a channel and returns the raw answer.
func post(c cfg, channel, body string) fetchResp {
	u, ok := target(c, channel)
	if !ok {
		return fetchResp{Code: sdk.ErrInvalidArgument}
	}
	return fetchFn(fetchReq{Method: "POST", URL: u, Headers: map[string]string{"Content-Type": "application/json"}, Body: body})
}
