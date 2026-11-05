package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// result is the outcome of one check of one monitor.
type result struct {
	Status string // statusUp, statusSlow, statusDown or statusConfig
	HTTP   int    // final HTTP status, 0 when there was none
	MS     int    // time the request took
	Cause  string // why the check failed or could not run; empty for up and slow
	// Skip: no verdict (the host's rate limit or another host error); try again next tick.
	Skip bool
}

const (
	causeConnect    = "could not connect (DNS, certificate or refused)"
	causeKeyword    = "keyword missing"
	causeKeywordCut = "keyword missing (page over 1 MB)"
)

// checkMonitor runs one check, spending at most timeoutMS. It classifies the
// answer and does nothing else: no state, no alerts. The host follows
// redirects, so the final status counts.
func checkMonitor(m monitor, timeoutMS int) result {
	method := http.MethodHead
	if m.Keyword != "" {
		method = http.MethodGet
	}
	start := now()
	res, err := fetch(fetchReq{Method: method, URL: m.URL, TimeoutMS: timeoutMS})
	if err == nil && method == http.MethodHead && (res.Status == http.StatusMethodNotAllowed || res.Status == http.StatusNotImplemented) {
		left := timeoutMS - elapsedMS(start)
		if left <= 0 {
			err = hostErr(sdk.ErrTimeout, "call timed out")
		} else {
			res, err = fetch(fetchReq{Method: http.MethodGet, URL: m.URL, TimeoutMS: left})
		}
	}
	ms := elapsedMS(start)
	if err != nil {
		return failedFetch(m, err, ms, timeoutMS)
	}
	if res.Status < 200 || res.Status >= 400 {
		return result{Status: statusDown, HTTP: res.Status, MS: ms, Cause: fmt.Sprintf("HTTP %d", res.Status)}
	}
	if m.Keyword != "" && !strings.Contains(res.Body, m.Keyword) {
		cause := causeKeyword
		if res.Truncated {
			cause = causeKeywordCut
		}
		return result{Status: statusDown, HTTP: res.Status, MS: ms, Cause: cause}
	}
	status := statusUp
	if m.MaxMS > 0 && ms > m.MaxMS {
		status = statusSlow
	}
	return result{Status: status, HTTP: res.Status, MS: ms}
}

// failedFetch classifies a host error.
func failedFetch(m monitor, err error, ms, timeoutMS int) result {
	r := result{Status: statusDown, MS: ms}
	switch code := hostErrCode(err); code {
	case sdk.ErrTimeout:
		r.Cause = fmt.Sprintf("timeout after %d ms", timeoutMS)
	case sdk.ErrInternal:
		r.Cause = causeConnect
		if day, ok := expiredOn(m); ok {
			r.Cause = "certificate expired on " + day
		}
	case sdk.ErrBlocked, sdk.ErrCapabilityDenied:
		// The monitor cannot run as configured: not an outage.
		r.Status = statusConfig
		r.Cause = hostErrMessage(err)
	default:
		// rate_limited, unavailable, invalid_argument and the like say
		// something about the host or this plugin, not the site: no verdict,
		// try again next tick.
		r.Skip = true
	}
	return r
}

// expiredOn reports the day the cached certificate of an https monitor's host
// ran out, when that day has passed.
func expiredOn(m monitor) (string, bool) {
	u, err := url.Parse(m.URL)
	if err != nil || u.Scheme != "https" {
		return "", false
	}
	c, ok, err := getCert(strings.ToLower(u.Hostname()))
	if err != nil || !ok || c.NotAfter == nil || !c.NotAfter.Before(now()) {
		return "", false
	}
	return c.NotAfter.UTC().Format("2006-01-02"), true
}

func elapsedMS(start time.Time) int { return int(now().Sub(start) / time.Millisecond) }
