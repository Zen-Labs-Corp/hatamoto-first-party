package main

import (
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func TestDoFetchRefusesWhenBudgetSpent(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	budgetEnd = now().Add(time.Second)
	if r := doFetch(fetchReq{Method: "GET", URL: "https://api.hubapi.com/x"}); r.Code != budgetCode {
		t.Fatalf("code %q", r.Code)
	}
	if h.fetchCalls() != 0 {
		t.Fatal("a request was started with no budget left")
	}
}

func TestDoFetchCutsTimeoutToBudget(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	var timeout int
	inner := callHost
	callHost = func(fn string, a, out any) error {
		if fn != "http.fetch" {
			return inner(fn, a, out)
		}
		timeout = a.(map[string]any)["timeout_ms"].(int)
		return nil
	}
	budgetEnd = now().Add(2 * time.Second)
	doFetch(fetchReq{Method: "GET", URL: "https://api.hubapi.com/x"})
	if timeout <= 0 || timeout > 2000 {
		t.Fatalf("timeout_ms %d", timeout)
	}
	budgetEnd = time.Time{}
	doFetch(fetchReq{Method: "GET", URL: "https://api.hubapi.com/x"})
	if timeout != int(callTimeout/time.Millisecond) {
		t.Fatalf("timeout_ms %d without a budget", timeout)
	}
}

func TestHostResultMapping(t *testing.T) {
	cases := []struct {
		code, kind, reason string
	}{
		{sdk.ErrCapabilityDenied, kindFail, "blocked"},
		{sdk.ErrBlocked, kindFail, "blocked"},
		{sdk.ErrInvalidArgument, kindFail, "rejected"},
		{sdk.ErrTimeout, kindRetry, "timeout"},
		{sdk.ErrUnavailable, kindRetry, "network"},
		{sdk.ErrInternal, kindRetry, "network"},
		{budgetCode, kindBudget, "timeout"},
	}
	for _, tc := range cases {
		if r := hostResult(tc.code); r.kind != tc.kind || r.reason != tc.reason {
			t.Errorf("%s: %+v", tc.code, r)
		}
	}
}
