package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestFetchFailuresEndWithOneRunFail(t *testing.T) {
	hostErr := func(code string) error { return &sdk.HostError{Code: code, Message: "detail " + testKey} }
	cases := []struct {
		name   string
		setup  func(h *fakeHost)
		reason string
	}{
		{"missing key", func(h *fakeHost) { h.settings = map[string]any{} }, "Add your Pexels API key in the plugin settings, or switch to Openverse."},
		{"401", func(h *fakeHost) { h.search = status(401) }, "Pexels rejected the API key. Check it in the plugin settings."},
		{"403", func(h *fakeHost) { h.search = status(403) }, "Pexels rejected the API key. Check it in the plugin settings."},
		{"429", func(h *fakeHost) { h.search = status(429) }, "The provider is rate limiting requests. Try again later."},
		{"500", func(h *fakeHost) { h.search = status(500) }, "The provider answered with HTTP 500."},
		{"truncated", func(h *fakeHost) {
			h.search = func(string) (fetchResp, error) {
				return fetchResp{Status: 200, Body: `{"photos":[`, Truncated: true}, nil
			}
		}, "The provider response was too large."},
		{"bad json", func(h *fakeHost) {
			h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: "<html>"}, nil }
		}, "The provider response could not be read."},
		{"empty results", func(h *fakeHost) {
			h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: `{"photos":[]}`}, nil }
		}, `No photos found for "barn".`},
		{"blocked host", func(h *fakeHost) { h.errs["http.fetch"] = hostErr(sdk.ErrBlocked) }, "Tick api.pexels.com / api.openverse.org when you start the run."},
		{"capability denied", func(h *fakeHost) {
			h.errs["http.fetch"] = &sdk.HostError{Code: sdk.ErrCapabilityDenied, Capability: "http.fetch"}
		}, "Grant the http.fetch permission to this plugin."},
		{"host rate limited", func(h *fakeHost) { h.errs["http.fetch"] = hostErr(sdk.ErrRateLimited) }, "Rate limited, try again in a few minutes."},
		{"settings error", func(h *fakeHost) { h.errs["settings.get"] = hostErr(sdk.ErrInternal) }, "Stock photo search failed."},
		{"run.info error", func(h *fakeHost) { h.errs["run.info"] = hostErr(sdk.ErrInternal) }, "Stock photo search failed."},
		{"import error", func(h *fakeHost) {
			h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 3, false)}, nil }
			h.errs["media.import_url"] = hostErr(sdk.ErrRateLimited)
		}, "Rate limited, try again in a few minutes."},
		{"buffer error", func(h *fakeHost) {
			h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 3, false)}, nil }
			h.errs["buffer.put"] = hostErr(sdk.ErrInvalidArgument)
		}, "Stock photo search failed."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t, "fetch", `{"query":"barn","provider":"pexels"}`, pexelsHosts...)
			c.setup(h)
			if err := h.step(""); err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if got := h.failReason(); got != c.reason {
				t.Fatalf("reason = %q, want %q", got, c.reason)
			}
			if len(h.buffer) != 0 {
				t.Fatalf("buffer used: %v", h.buffer)
			}
			for _, call := range h.calls {
				if call.Fn != "http.fetch" && strings.Contains(call.Args, testKey) {
					t.Fatalf("key in %s: %s", call.Fn, call.Args)
				}
			}
		})
	}
}

func TestFailRunReturnsErrorWhenRunFailFails(t *testing.T) {
	h := newHost(t, "fetch", `{"query":""}`)
	h.errs["run.fail"] = &sdk.HostError{Code: sdk.ErrInternal}
	if err := h.step(""); err == nil {
		t.Fatal("step ended with neither fail nor error")
	}
}

func TestPanicBecomesRunFail(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn"}`, pexelsHosts...)
	h.search = func(string) (fetchResp, error) { panic("boom") }
	if err := h.step(""); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if got := h.failReason(); got != "Stock photo search failed." {
		t.Fatalf("reason = %q", got)
	}
}

func TestInvalidInputsFail(t *testing.T) {
	for params, want := range map[string]string{
		`{"query":" "}`:                       "Enter a search term.",
		`{}`:                                  "Enter a search term.",
		`{"query":"x","provider":"unsplash"}`: "Unknown provider.",
	} {
		h := newHost(t, "fetch", params, pexelsHosts...)
		if err := h.step(""); err != nil {
			t.Fatal(err)
		}
		if got := h.failReason(); got != want {
			t.Errorf("%s: reason = %q, want %q", params, got, want)
		}
		if len(h.names("http.fetch")) != 0 {
			t.Errorf("%s: network used", params)
		}
	}
}

func status(code int) func(string) (fetchResp, error) {
	return func(string) (fetchResp, error) { return fetchResp{Status: code}, nil }
}
