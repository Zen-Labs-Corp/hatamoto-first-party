package main

import (
	"errors"
	"testing"

	"hatamoto.dev/sdk"
)

func TestDoFetchKeepsOnlyTheHostCode(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	callHost = func(fn string, a, out any) error {
		return &sdk.HostError{Code: sdk.ErrTimeout, Message: "https://x/members/0123abcd timed out"}
	}
	if r := doFetch(fetchReq{Method: "GET", URL: "https://api.kit.com/v4/forms"}); r.Code != sdk.ErrTimeout || r.Status != 0 || r.Body != "" {
		t.Fatalf("%+v", r)
	}
	callHost = func(string, any, any) error { return errors.New("boom with a@b.co") }
	if r := doFetch(fetchReq{}); r.Code != sdk.ErrInternal {
		t.Fatalf("%+v", r)
	}
}

func TestHostResultClassification(t *testing.T) {
	for code, kind := range map[string]string{
		sdk.ErrTimeout: kindRetry, sdk.ErrUnavailable: kindRetry, sdk.ErrRateLimited: kindRetry, sdk.ErrInternal: kindRetry,
		sdk.ErrCapabilityDenied: kindFail, sdk.ErrBlocked: kindFail, sdk.ErrInvalidArgument: kindFail,
	} {
		if got := hostResult(code).kind; got != kind {
			t.Fatalf("%s: %s, want %s", code, got, kind)
		}
	}
}

func TestDoFetchStopsWhenBudgetIsSpent(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	callHost = func(string, any, any) error { t.Fatal("request started past the budget"); return nil }
	budgetEnd = now().Add(minCall / 2)
	if r := doFetch(fetchReq{}); r.Code != budgetCode {
		t.Fatalf("%+v", r)
	}
	if got := hostResult(budgetCode).kind; got != kindBudget {
		t.Fatalf("kind %s", got)
	}
}

func TestDoFetchCutsTimeoutToBudget(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	var timeout int
	callHost = func(fn string, a, out any) error {
		timeout = a.(map[string]any)["timeout_ms"].(int)
		return nil
	}
	budgetEnd = now().Add(2 * 1000 * 1000 * 1000)
	doFetch(fetchReq{})
	if timeout != 2000 {
		t.Fatalf("timeout %d", timeout)
	}
}
