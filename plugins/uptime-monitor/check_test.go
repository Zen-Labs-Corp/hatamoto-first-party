package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// answer is what the fake web answers to one http.fetch.
type answer struct {
	status    int
	body      string
	truncated bool
	err       error
	takes     time.Duration
}

// checkHost fakes http.fetch with the answers in order and moves the clock by
// each answer's takes; certs answers getCert (db.query).
func checkHost(t *testing.T, answers []answer, certs string) (*fakeHost, *time.Time) {
	t.Helper()
	clock := setNow(t, "2026-02-01T12:00:00Z")
	i := 0
	f := newFakeHost(t, func(c call, out any) error {
		switch c.Fn {
		case "http.fetch":
			if i >= len(answers) {
				t.Fatalf("unexpected fetch %d", i+1)
			}
			a := answers[i]
			i++
			clock = clock.Add(a.takes)
			now = func() time.Time { return clock }
			if a.err != nil {
				return a.err
			}
			b, _ := json.Marshal(map[string]any{"status": a.status, "body": a.body, "truncated": a.truncated})
			return json.Unmarshal(b, out)
		case "db.query":
			return cell(out, certs)
		}
		return nil
	})
	return f, &clock
}

func fetches(f *fakeHost) []call {
	var out []call
	for _, c := range f.calls {
		if c.Fn == "http.fetch" {
			out = append(out, c)
		}
	}
	return out
}

var site = monitor{ID: 1, Name: "Site", URL: "https://example.org/", IntervalS: 300}

func TestCheckUpAndRedirects(t *testing.T) {
	for _, status := range []int{200, 204, 301, 302, 399} {
		f, _ := checkHost(t, []answer{{status: status, takes: 120 * time.Millisecond}}, `[]`)
		r := checkMonitor(site, 8000)
		if r.Status != statusUp || r.HTTP != status || r.MS != 120 || r.Cause != "" || r.Skip {
			t.Fatalf("%d: %+v", status, r)
		}
		fs := fetches(f)
		if len(fs) != 1 || fs[0].Args["method"] != "HEAD" || fs[0].Args["timeout_ms"] != float64(8000) {
			t.Fatalf("%d: fetch %v", status, fs)
		}
	}
}

func TestCheckSlowAboveMaxMS(t *testing.T) {
	m := site
	m.MaxMS = 1000
	checkHost(t, []answer{{status: 200, takes: 1000 * time.Millisecond}}, `[]`)
	if r := checkMonitor(m, 8000); r.Status != statusUp {
		t.Fatalf("equal to max_ms is up: %+v", r)
	}
	checkHost(t, []answer{{status: 200, takes: 1001 * time.Millisecond}}, `[]`)
	if r := checkMonitor(m, 8000); r.Status != statusSlow || r.Cause != "" {
		t.Fatalf("over max_ms is slow: %+v", r)
	}
	checkHost(t, []answer{{status: 200, takes: 20 * time.Second}}, `[]`)
	if r := checkMonitor(site, 8000); r.Status != statusUp {
		t.Fatalf("no max_ms is never slow: %+v", r)
	}
}

func TestCheckHTTPErrorIsDown(t *testing.T) {
	for _, status := range []int{400, 404, 500, 503} {
		checkHost(t, []answer{{status: status}}, `[]`)
		r := checkMonitor(site, 8000)
		if r.Status != statusDown || r.HTTP != status || r.Cause != fmt.Sprintf("HTTP %d", status) {
			t.Fatalf("%d: %+v", status, r)
		}
	}
	// A failing page is down even when the keyword rule would also fail.
	m := site
	m.Keyword = "welcome"
	checkHost(t, []answer{{status: 503, body: "oops"}}, `[]`)
	if r := checkMonitor(m, 8000); r.Cause != "HTTP 503" {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckHeadFallsBackToGet(t *testing.T) {
	for _, status := range []int{405, 501} {
		f, _ := checkHost(t, []answer{
			{status: status, takes: 100 * time.Millisecond},
			{status: 200, takes: 200 * time.Millisecond},
		}, `[]`)
		r := checkMonitor(site, 8000)
		if r.Status != statusUp || r.HTTP != 200 || r.MS != 300 {
			t.Fatalf("%d: %+v", status, r)
		}
		fs := fetches(f)
		if len(fs) != 2 || fs[0].Args["method"] != "HEAD" || fs[1].Args["method"] != "GET" {
			t.Fatalf("%d: %v", status, fs)
		}
		if fs[1].Args["timeout_ms"] != float64(7900) {
			t.Fatalf("the second request gets what is left: %v", fs[1].Args["timeout_ms"])
		}
	}
	// A GET that also answers 405 is a plain HTTP error.
	checkHost(t, []answer{{status: 405}, {status: 405}}, `[]`)
	if r := checkMonitor(site, 8000); r.Status != statusDown || r.Cause != "HTTP 405" {
		t.Fatalf("got %+v", r)
	}
	// No time left for the second request.
	checkHost(t, []answer{{status: 405, takes: 8 * time.Second}}, `[]`)
	if r := checkMonitor(site, 8000); r.Status != statusDown || r.Cause != "timeout after 8000 ms" {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckKeyword(t *testing.T) {
	m := site
	m.Keyword = "Welcome"
	f, _ := checkHost(t, []answer{{status: 200, body: "<h1>Welcome home</h1>"}}, `[]`)
	if r := checkMonitor(m, 8000); r.Status != statusUp {
		t.Fatalf("keyword present: %+v", r)
	}
	if fs := fetches(f); fs[0].Args["method"] != "GET" {
		t.Fatalf("a keyword needs the body, so GET: %v", fs)
	}
	checkHost(t, []answer{{status: 200, body: "<h1>Error</h1>"}}, `[]`)
	if r := checkMonitor(m, 8000); r.Status != statusDown || r.Cause != "keyword missing" || r.HTTP != 200 {
		t.Fatalf("keyword missing: %+v", r)
	}
	checkHost(t, []answer{{status: 200, body: "<h1>Error</h1>", truncated: true}}, `[]`)
	if r := checkMonitor(m, 8000); r.Status != statusDown || r.Cause != "keyword missing (page over 1 MB)" {
		t.Fatalf("keyword missing in a truncated page: %+v", r)
	}
	checkHost(t, []answer{{status: 200, body: "Welcome", truncated: true}}, `[]`)
	if r := checkMonitor(m, 8000); r.Status != statusUp {
		t.Fatalf("keyword found before the cut: %+v", r)
	}
	// A slow page with the keyword is slow.
	m.MaxMS = 500
	checkHost(t, []answer{{status: 200, body: "Welcome", takes: time.Second}}, `[]`)
	if r := checkMonitor(m, 8000); r.Status != statusSlow {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckTimeout(t *testing.T) {
	checkHost(t, []answer{{err: hostErr(sdk.ErrTimeout, "call timed out"), takes: 6 * time.Second}}, `[]`)
	r := checkMonitor(site, 6000)
	if r.Status != statusDown || r.Cause != "timeout after 6000 ms" || r.HTTP != 0 || r.MS != 6000 {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckInternalIsOneCause(t *testing.T) {
	// No certificate row, a future expiry, a lookup that failed and an http
	// monitor all give the generic cause.
	for name, certs := range map[string]string{
		"unknown host": `[]`,
		"valid":        `[{"host":"example.org","not_after":"2026-03-01T00:00:00+00:00"}]`,
		"no expiry":    `[{"host":"example.org","not_after":null,"lookup_error":"unknown"}]`,
	} {
		checkHost(t, []answer{{err: hostErr(sdk.ErrInternal, "internal error")}}, certs)
		r := checkMonitor(site, 8000)
		if r.Status != statusDown || r.Cause != "could not connect (DNS, certificate or refused)" {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	plain := site
	plain.URL = "http://example.org/"
	f, _ := checkHost(t, []answer{{err: hostErr(sdk.ErrInternal, "internal error")}},
		`[{"host":"example.org","not_after":"2026-01-01T00:00:00+00:00"}]`)
	if r := checkMonitor(plain, 8000); r.Cause != "could not connect (DNS, certificate or refused)" {
		t.Fatalf("http monitor: %+v", r)
	}
	if f.count("db.query") != 0 {
		t.Fatal("an http monitor has no certificate to look up")
	}
}

func TestCheckStaleCertificateRowDoesNotUpgradeInternal(t *testing.T) {
	// Looked up more than 24 h ago: the certificate may have been renewed since.
	checkHost(t, []answer{{err: hostErr(sdk.ErrInternal, "internal error")}},
		`[{"host":"example.org","not_after":"2026-01-20T08:30:00+00:00","looked_up_at":"2026-01-31T11:00:00+00:00","lookup_error":"unknown"}]`)
	m := site
	m.URL = "https://example.org/"
	if r := checkMonitor(m, 8000); r.Cause != causeConnect {
		t.Fatalf("got %+v", r)
	}
	checkHost(t, []answer{{err: hostErr(sdk.ErrInternal, "internal error")}},
		`[{"host":"example.org","not_after":"2026-01-20T08:30:00+00:00"}]`)
	if r := checkMonitor(m, 8000); r.Cause != causeConnect {
		t.Fatalf("never looked up: %+v", r)
	}
}

func TestCheckExpiredCertificateUpgradesInternal(t *testing.T) {
	f, _ := checkHost(t, []answer{{err: hostErr(sdk.ErrInternal, "internal error")}},
		`[{"host":"example.org","not_after":"2026-01-20T08:30:00+00:00","issuer":"R3","looked_up_at":"2026-02-01T06:00:00+00:00"}]`)
	m := site
	m.URL = "https://Example.ORG:443/path"
	r := checkMonitor(m, 8000)
	if r.Status != statusDown || r.Cause != "certificate expired on 2026-01-20" {
		t.Fatalf("got %+v", r)
	}
	if p := f.calls[len(f.calls)-1].params(); len(p) != 1 || p[0] != "example.org" {
		t.Fatalf("certificate looked up for %v", p)
	}
	// A certificate store that fails keeps the generic cause.
	newFakeHost(t, func(c call, out any) error {
		if c.Fn == "http.fetch" {
			return hostErr(sdk.ErrInternal, "internal error")
		}
		return hostErr(sdk.ErrUnavailable, "no database")
	})
	if r := checkMonitor(site, 8000); r.Cause != "could not connect (DNS, certificate or refused)" {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckConfigIsNotDown(t *testing.T) {
	for code, msg := range map[string]string{
		sdk.ErrBlocked:          "fetch blocked: 10.0.0.5 resolves to a blocked address (10.0.0.5)",
		sdk.ErrCapabilityDenied: "capability not granted: http.fetch.any",
	} {
		checkHost(t, []answer{{err: hostErr(code, msg)}}, `[]`)
		r := checkMonitor(site, 8000)
		if r.Status != statusConfig || r.Cause != msg || r.Skip {
			t.Fatalf("%s: %+v", code, r)
		}
	}
}

func TestCheckRateLimitedIsSkipped(t *testing.T) {
	checkHost(t, []answer{{err: hostErr(sdk.ErrRateLimited, "rate limited")}}, `[]`)
	r := checkMonitor(site, 8000)
	if !r.Skip || r.Cause != "" {
		t.Fatalf("got %+v", r)
	}
}

func TestCheckRepeatingHostErrorsAreConfigNotSilent(t *testing.T) {
	for code, err := range map[string]error{
		sdk.ErrUnavailable: hostErr(sdk.ErrUnavailable, "x"), sdk.ErrInvalidArgument: hostErr(sdk.ErrInvalidArgument, "x"),
		"reentrant": hostErr("reentrant", "x"),
	} {
		f, _ := checkHost(t, []answer{{err: err}}, `[]`)
		r := checkMonitor(site, 8000)
		if r.Skip || r.Status != statusConfig || r.Cause != "host refused the check: "+code {
			t.Fatalf("%s: got %+v", code, r)
		}
		if n := len(f.logs()); n != 1 || !strings.Contains(f.logs()[0], "host=example.org") {
			t.Fatalf("%s: logs %v", code, f.logs())
		}
	}
}

func TestTickSkippedCheckSendsNoMailAndKeepsState(t *testing.T) {
	h := newTickHost(t, 1)
	h.st.AlertEmails = []string{"owner@example.org"}
	h.st.FailThreshold = 1
	queued := 0
	newFakeHost(t, func(c call, out any) error {
		switch {
		case c.Fn == "http.fetch":
			return hostErr(sdk.ErrRateLimited, "rate limited")
		case strings.HasPrefix(c.sql(), "INSERT INTO outbox"):
			queued++
		}
		return h.reply(c, out)
	})
	for range 3 {
		checkTick()
		h.clock = h.clock.Add(time.Minute)
	}
	if queued != 0 || len(h.checks) != 0 || h.monitors[1].Status != statusPending || h.monitors[1].FailCount != 0 {
		t.Fatalf("queued %d checks %v %+v", queued, h.checks, h.monitors[1])
	}
}
