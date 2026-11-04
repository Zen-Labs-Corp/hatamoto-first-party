package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// fetchHost answers http.fetch from a script: one answer per call.
type answer struct {
	status int
	err    error
}

func fetchHost(t *testing.T, answers ...answer) *fakeHost {
	i := 0
	return newFakeHost(t, func(c call, out any) error {
		if c.Fn != "http.fetch" {
			t.Fatalf("unexpected host call %q", c.Fn)
		}
		if i >= len(answers) {
			t.Fatalf("unexpected extra request %v", c.Args)
		}
		a := answers[i]
		i++
		if a.err != nil {
			return a.err
		}
		return json.Unmarshal([]byte(`{"status":`+itoa(a.status)+`,"headers":{"content-type":"text/html"}}`), out)
	})
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func methods(f *fakeHost) string {
	var m []string
	for _, c := range f.calls {
		if c.Fn == "http.fetch" {
			m = append(m, c.Args["method"].(string))
		}
	}
	return strings.Join(m, ",")
}

func TestCheckOutboundStatuses(t *testing.T) {
	for _, c := range []struct {
		status int
		want   string
		delay  time.Duration
	}{
		{200, statusOK, 0}, {204, statusOK, 0}, {301, statusOK, 0}, {399, statusOK, 0},
		{404, statusBroken, 0}, {410, statusBroken, 0}, {403, statusBroken, 0}, {500, statusBroken, 0}, {502, statusBroken, 0},
		{429, statusUnknown, retryLater}, {503, statusUnknown, retryLater},
	} {
		f := fetchHost(t, answer{status: c.status})
		got := checkOutbound("https://example.org/x", 5*time.Second, 0)
		if got.Status != c.want || got.HTTPStatus != c.status || got.Delay != c.delay || got.Keep {
			t.Errorf("%d: got %+v", c.status, got)
		}
		if methods(f) != "HEAD" {
			t.Errorf("%d: methods %s", c.status, methods(f))
		}
	}
}

func TestCheckOutboundFallsBackToGET(t *testing.T) {
	for _, refused := range []int{405, 501} {
		f := fetchHost(t, answer{status: refused}, answer{status: 200})
		got := checkOutbound("https://example.org/x", time.Second, 0)
		if got.Status != statusOK || got.HTTPStatus != 200 || methods(f) != "HEAD,GET" {
			t.Errorf("%d: %+v %s", refused, got, methods(f))
		}
	}
	f := fetchHost(t, answer{status: 405}, answer{status: 404})
	if got := checkOutbound("https://example.org/x", time.Second, 0); got.Status != statusBroken || got.HTTPStatus != 404 || methods(f) != "HEAD,GET" {
		t.Errorf("GET result must win: %+v", got)
	}
}

func TestCheckOutboundTwoStrikes(t *testing.T) {
	for code, text := range map[string]string{sdk.ErrTimeout: "timed out", sdk.ErrInternal: "connection failed"} {
		fetchHost(t, answer{err: &sdk.HostError{Code: code}})
		first := checkOutbound("https://example.org/x", time.Second, 0)
		if first.Status != statusUnknown || first.FailCount != 1 || first.Err != text || first.Delay != retryLater {
			t.Errorf("%s first: %+v", code, first)
		}
		fetchHost(t, answer{err: &sdk.HostError{Code: code}})
		second := checkOutbound("https://example.org/x", time.Second, 1)
		if second.Status != statusBroken || second.FailCount != 2 || second.Err != text {
			t.Errorf("%s second: %+v", code, second)
		}
	}
}

func TestCheckOutboundOtherHostErrors(t *testing.T) {
	fetchHost(t, answer{err: &sdk.HostError{Code: sdk.ErrBlocked, Message: "fetch blocked: example.org resolves to a blocked address (10.0.0.1)"}})
	got := checkOutbound("http://example.org/", time.Second, 0)
	if got.Status != statusBroken || got.Err != "blocked address" {
		t.Errorf("blocked: %+v", got)
	}
	// Policy refusals that say nothing about the link are unknown, not broken.
	for _, msg := range []string{"host example.org:8443 is not in the plugin's allowed_hosts", "more than 5 redirects", ""} {
		fetchHost(t, answer{err: &sdk.HostError{Code: sdk.ErrBlocked, Message: msg}})
		got = checkOutbound("http://example.org/", time.Second, 0)
		if got.Status != statusUnknown || got.Delay != retryLater || got.Keep {
			t.Errorf("blocked %q: %+v", msg, got)
		}
	}
	for _, e := range []error{
		&sdk.HostError{Code: sdk.ErrCapabilityDenied},
		&sdk.HostError{Code: sdk.ErrRateLimited},
		&sdk.HostError{Code: sdk.ErrUnavailable},
		errors.New("boom"),
	} {
		f := fetchHost(t, answer{err: e})
		if got := checkOutbound("https://example.org/x", time.Second, 1); !got.Keep {
			t.Errorf("%v: want no state change, got %+v", e, got)
		}
		if f.count("http.fetch") != 1 {
			t.Errorf("%v: one request expected", e)
		}
	}
}

func TestCheckOutboundNeverSendsCookies(t *testing.T) {
	f := fetchHost(t, answer{status: 405}, answer{status: 200})
	checkOutbound("https://example.org/x", 3*time.Second, 0)
	for _, c := range f.calls {
		h, _ := c.Args["headers"].(map[string]any)
		for k := range h {
			if strings.EqualFold(k, "cookie") || strings.EqualFold(k, "authorization") {
				t.Fatalf("header %s must never be sent", k)
			}
		}
		if h["Accept"] != "*/*" || c.Args["timeout_ms"] != float64(3000) || c.Args["url"] != "https://example.org/x" {
			t.Fatalf("args %v", c.Args)
		}
		if _, ok := c.Args["body"]; ok {
			t.Fatal("no body expected")
		}
	}
}
