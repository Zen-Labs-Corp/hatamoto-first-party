package main

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// fetchResult is the part of an http.fetch answer this plugin reads.
type fetchResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

// retryLater is how long an unknown link waits for its next check.
const retryLater = 24 * time.Hour

// fetch runs one request. It sends no cookies and no credentials: the only
// header is Accept, and the host keeps no cookie jar.
func fetch(method, url string, timeout time.Duration) (fetchResult, error) {
	var res fetchResult
	err := callHost(sdk.FnHTTPFetch, map[string]any{
		"method":     method,
		"url":        url,
		"timeout_ms": int(timeout / time.Millisecond),
		"headers":    map[string]string{"Accept": "*/*"},
	}, &res)
	return res, err
}

// checkOutbound requests url with HEAD, and with GET when the server refuses
// HEAD (405, 501). prevFails is the consecutive connection failures so far: a
// timeout or connection error is unknown the first time and broken the second.
func checkOutbound(url string, timeout time.Duration, prevFails int) result {
	res, err := fetch(http.MethodHead, url, timeout)
	if err == nil && (res.Status == http.StatusMethodNotAllowed || res.Status == http.StatusNotImplemented) {
		res, err = fetch(http.MethodGet, url, timeout)
	}
	if err != nil {
		return fromFetchError(err, prevFails)
	}
	switch {
	case res.Status == http.StatusTooManyRequests || res.Status == http.StatusServiceUnavailable:
		return result{Status: statusUnknown, HTTPStatus: res.Status, Delay: retryLater}
	case res.Status >= 400:
		return result{Status: statusBroken, HTTPStatus: res.Status}
	}
	return result{Status: statusOK, HTTPStatus: res.Status}
}

func fromFetchError(err error, prevFails int) result {
	var he *sdk.HostError
	if !errors.As(err, &he) {
		logf("warn", "outbound check: %v", err)
		return keep()
	}
	switch he.Code {
	case sdk.ErrBlocked:
		// "blocked" also covers an ungranted permission, a port outside 80/443,
		// credentials in the URL and too many redirects. Only the address
		// guard says something about the link itself.
		if strings.Contains(he.Message, "blocked address") {
			return result{Status: statusBroken, Err: "blocked address"}
		}
		return result{Status: statusUnknown, Err: "not checkable", Delay: retryLater}
	case sdk.ErrTimeout, sdk.ErrInternal:
		text := "connection failed"
		if he.Code == sdk.ErrTimeout {
			text = "timed out"
		}
		if prevFails >= 1 {
			return result{Status: statusBroken, Err: text, FailCount: prevFails + 1}
		}
		return result{Status: statusUnknown, Err: text, FailCount: prevFails + 1, Delay: retryLater}
	case sdk.ErrRateLimited:
		// Nothing learned about the link: it stays due and the next run tries again.
		return keep()
	}
	logf("warn", "outbound check refused by the host (%s)", he.Code)
	return keep()
}

// outboundGranted reports whether the owner granted both http.fetch and
// http.fetch.any, which every outbound check needs. When it cannot tell, it
// says no: the links stay due and nothing is reported against them.
func outboundGranted() bool {
	var p struct {
		Granted []string `json:"granted"`
	}
	if err := callHost("permissions", struct{}{}, &p); err != nil {
		logf("warn", "permissions unreadable, outbound checks skipped: %v", err)
		return false
	}
	return slices.Contains(p.Granted, "http.fetch") && slices.Contains(p.Granted, "http.fetch.any")
}
