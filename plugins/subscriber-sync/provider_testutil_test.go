package main

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/sdk"
)

// fakeFetch answers requests from a script, one response per call, and records them.
type fakeFetch struct {
	t    *testing.T
	resp []fetchResp
	reqs []fetchReq
}

func (f *fakeFetch) fetch(r fetchReq) fetchResp {
	f.reqs = append(f.reqs, r)
	if len(f.reqs) > len(f.resp) {
		f.t.Fatalf("unexpected extra request %s %s", r.Method, r.URL)
	}
	return f.resp[len(f.reqs)-1]
}

func newFake(t *testing.T, resp ...fetchResp) *fakeFetch { return &fakeFetch{t: t, resp: resp} }

func jsonOf(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("body %q: %v", s, err)
	}
	return m
}

// failureCases are the outcomes every provider must give for a failing first call.
var failureCases = []struct {
	name string
	resp fetchResp
	kind string
}{
	{"400", fetchResp{Status: 400}, kindFail},
	{"401", fetchResp{Status: 401}, kindFail},
	{"404", fetchResp{Status: 404}, kindFail},
	{"422", fetchResp{Status: 422}, kindFail},
	{"408", fetchResp{Status: 408}, kindRetry},
	{"429", fetchResp{Status: 429}, kindRetry},
	{"500", fetchResp{Status: 500}, kindRetry},
	{"503", fetchResp{Status: 503}, kindRetry},
	{"timeout", fetchResp{Code: sdk.ErrTimeout}, kindRetry},
	{"unavailable", fetchResp{Code: sdk.ErrUnavailable}, kindRetry},
	{"rate_limited", fetchResp{Code: sdk.ErrRateLimited}, kindRetry},
	{"capability_denied", fetchResp{Code: sdk.ErrCapabilityDenied}, kindFail},
	{"blocked", fetchResp{Code: sdk.ErrBlocked}, kindFail},
	{"invalid_argument", fetchResp{Code: sdk.ErrInvalidArgument}, kindFail},
}

func testFailureClassification(t *testing.T, p provider, c cfg) {
	t.Helper()
	for _, tc := range failureCases {
		f := newFake(t, tc.resp)
		r := p.upsert(job{Email: "ada@example.com"}, c, f.fetch)
		if r.kind != tc.kind {
			t.Errorf("%s: kind %q, want %q", tc.name, r.kind, tc.kind)
		}
		if tc.resp.Status != 0 && r.status != tc.resp.Status {
			t.Errorf("%s: status %d", tc.name, r.status)
		}
		if len(f.reqs) != 1 {
			t.Errorf("%s: %d requests", tc.name, len(f.reqs))
		}
	}
}
