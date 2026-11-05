package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// certHost fakes monitors, cert rows, crt.sh and the mail outbox.
type certHost struct {
	t       *testing.T
	clock   time.Time
	urls    []string
	certs   map[string]*cert
	st      settings
	crt     func(host string) (fetchRes, error) // crt.sh answer
	fetched []string
	mails   []string // outbox subjects
	bodies  []string
	takes   time.Duration
}

func newCertHost(t *testing.T, urls ...string) *certHost {
	t.Helper()
	h := &certHost{t: t, urls: urls, certs: map[string]*cert{}, takes: 200 * time.Millisecond,
		st: settings{AlertEmails: []string{"ops@example.org"}, FailThreshold: 2, SSLWarnDays: []int{21, 7, 1}}}
	h.clock = setNow(t, "2026-03-01T12:00:00Z")
	newFakeHost(t, h.reply)
	now = func() time.Time { return h.clock }
	return h
}

func (h *certHost) reply(c call, out any) error {
	sql := c.sql()
	switch {
	case c.Fn == "settings.get":
		b, _ := json.Marshal(h.st)
		return json.Unmarshal(b, out)
	case c.Fn == "http.fetch":
		u := c.Args["url"].(string)
		h.fetched = append(h.fetched, u)
		h.clock = h.clock.Add(h.takes)
		host := strings.TrimPrefix(strings.SplitN(u, "&", 2)[0], "https://crt.sh/?q=")
		res, err := h.crt(host)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(res)
		return json.Unmarshal(b, out)
	case strings.Contains(sql, "FROM monitors m"):
		var ms []monitor
		for i, u := range h.urls {
			ms = append(ms, monitor{ID: int64(i + 1), URL: u})
		}
		b, _ := json.Marshal(ms)
		return cell(out, string(b))
	case strings.Contains(sql, "FROM certs c WHERE"):
		host := c.params()[0].(string)
		if row, ok := h.certs[host]; ok {
			b, _ := json.Marshal([]cert{*row})
			return cell(out, string(b))
		}
		return cell(out, `[]`)
	case strings.HasPrefix(sql, "INSERT INTO certs"):
		p := c.params()
		row := cert{Host: p[0].(string)}
		if s, ok := p[1].(string); ok {
			t, _ := time.Parse(time.RFC3339, s)
			row.NotAfter = &t
		}
		row.Issuer = p[2].(string)
		lt, _ := time.Parse(time.RFC3339, p[3].(string))
		row.LookedUpAt = &lt
		row.LookupError = p[4].(string)
		_ = json.Unmarshal([]byte(p[5].(string)), &row.Warned)
		h.certs[row.Host] = &row
	case strings.HasPrefix(sql, "INSERT INTO outbox"):
		h.mails = append(h.mails, c.params()[0].(string))
		h.bodies = append(h.bodies, c.params()[1].(string))
	case strings.Contains(sql, "FROM outbox ORDER BY id"):
		return cell(out, `[]`)
	case strings.HasPrefix(sql, "WITH d AS (DELETE FROM outbox"):
		return cell(out, `0`)
	}
	return nil
}

func crtBody(rows ...crtRow) fetchRes {
	b, _ := json.Marshal(rows)
	return fetchRes{Status: 200, Body: string(b)}
}

func row(names, notAfter, issuer string) crtRow {
	return crtRow{IssuerName: issuer, NameValue: names, NotAfter: notAfter}
}

func TestCertLookupNewestCoveringRowWins(t *testing.T) {
	h := newCertHost(t, "https://www.example.org/")
	h.crt = func(string) (fetchRes, error) {
		return crtBody(
			row("other.example.net", "2027-01-01T00:00:00", "CN=Other"),
			row("example.org\nWWW.Example.org", "2026-04-01T10:00:00", "CN=Old CA"),
			row("*.example.org", "2026-06-10T10:00:00", "CN=New CA"),
			row("*.sub.example.org", "2027-02-01T00:00:00", "CN=Deep"),
		), nil
	}
	certsTick()
	c := h.certs["www.example.org"]
	if c == nil || c.NotAfter == nil || c.NotAfter.Format(time.RFC3339) != "2026-06-10T10:00:00Z" || c.Issuer != "CN=New CA" || c.LookupError != "" {
		t.Fatalf("%+v", c)
	}
	if want := "https://crt.sh/?q=www.example.org&output=json&exclude=expired"; len(h.fetched) != 1 || h.fetched[0] != want {
		t.Fatalf("fetched %v", h.fetched)
	}
}

func TestCertExactNameAndIssuer(t *testing.T) {
	h := newCertHost(t, "https://example.org/")
	h.crt = func(string) (fetchRes, error) {
		return crtBody(row("example.org\nwww.example.org", "2026-09-01T00:00:00", "C=US, O=Some CA, CN=X1")), nil
	}
	certsTick()
	if c := h.certs["example.org"]; c == nil || c.Issuer != "C=US, O=Some CA, CN=X1" {
		t.Fatalf("%+v", c)
	}
}

func TestCertLookupFailuresAreUnknownAndKeepTheOldExpiry(t *testing.T) {
	answers := map[string]func() (fetchRes, error){
		"empty list": func() (fetchRes, error) { return fetchRes{Status: 200, Body: "[]"}, nil },
		"502":        func() (fetchRes, error) { return fetchRes{Status: 502, Body: "<html>"}, nil },
		"truncated": func() (fetchRes, error) {
			return fetchRes{Status: 200, Body: `[{"issuer_name":"x","name_value":"example.org","not_after":"2030-01-01T00:00:00"}]`, Truncated: true}, nil
		},
		"not json": func() (fetchRes, error) { return fetchRes{Status: 200, Body: "oops"}, nil },
		"timeout":  func() (fetchRes, error) { return fetchRes{}, hostErr(sdk.ErrTimeout, "call timed out") },
		"no cover": func() (fetchRes, error) {
			return crtBody(row("other.example.net", "2030-01-01T00:00:00", "CN=X")), nil
		},
	}
	for name, ans := range answers {
		t.Run(name, func(t *testing.T) {
			h := newCertHost(t, "https://example.org/")
			old := h.clock.Add(100 * 24 * time.Hour)
			ago := h.clock.Add(-13 * time.Hour)
			h.certs["example.org"] = &cert{Host: "example.org", NotAfter: &old, Issuer: "CN=Kept", LookedUpAt: &ago}
			h.crt = func(string) (fetchRes, error) { return ans() }
			certsTick()
			c := h.certs["example.org"]
			if c.LookupError != "unknown" || c.NotAfter == nil || !c.NotAfter.Equal(old) || c.Issuer != "CN=Kept" || !c.LookedUpAt.After(ago) {
				t.Fatalf("%+v", c)
			}
			if len(h.mails) != 0 {
				t.Fatalf("mails %v", h.mails)
			}
		})
	}
}

func TestCertLookupCachedFor12Hours(t *testing.T) {
	h := newCertHost(t, "https://example.org/")
	h.crt = func(string) (fetchRes, error) {
		return crtBody(row("example.org", "2026-09-01T00:00:00", "CN=X")), nil
	}
	certsTick()
	certsTick()
	if len(h.fetched) != 1 {
		t.Fatalf("second tick fetched again: %v", h.fetched)
	}
	h.clock = h.clock.Add(12 * time.Hour)
	certsTick()
	if len(h.fetched) != 2 {
		t.Fatalf("no lookup after 12 h: %v", h.fetched)
	}
}

func TestCertFailedLookupIsAlsoCached(t *testing.T) {
	h := newCertHost(t, "https://example.org/")
	h.crt = func(string) (fetchRes, error) { return fetchRes{Status: 502}, nil }
	certsTick()
	certsTick()
	if len(h.fetched) != 1 {
		t.Fatalf("fetched %v", h.fetched)
	}
}

func TestCertOnlyDistinctHTTPSHostsAreLookedUp(t *testing.T) {
	h := newCertHost(t, "http://plain.example.org/", "https://a.example.org/x", "https://A.example.org/y", "https://192.0.2.7/", "https://b.example.org:8443/")
	h.crt = func(string) (fetchRes, error) { return fetchRes{Status: 502}, nil }
	certsTick()
	if len(h.fetched) != 2 || !strings.Contains(h.fetched[0], "q=a.example.org&") || !strings.Contains(h.fetched[1], "q=b.example.org&") {
		t.Fatalf("fetched %v", h.fetched)
	}
}

func TestCertOldestLookupFirstAndBudget(t *testing.T) {
	var urls []string
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		urls = append(urls, "https://"+n+".example.org/")
	}
	h := newCertHost(t, urls...)
	h.takes = 2 * time.Second
	h.crt = func(string) (fetchRes, error) { return fetchRes{Status: 502}, nil }
	old := h.clock.Add(-48 * time.Hour)
	older := h.clock.Add(-72 * time.Hour)
	h.certs["h.example.org"] = &cert{Host: "h.example.org", LookedUpAt: &old}
	h.certs["g.example.org"] = &cert{Host: "g.example.org", LookedUpAt: &older}
	start := h.clock
	certsTick()
	// Never looked up first (a, b, ...), then oldest; the budget ends it.
	if len(h.fetched) != 3 || !strings.Contains(h.fetched[0], "q=a.") || !strings.Contains(h.fetched[2], "q=c.") {
		t.Fatalf("fetched %v", h.fetched)
	}
	if h.clock.Sub(start) > tickBudget+time.Second {
		t.Fatalf("took %v", h.clock.Sub(start))
	}
}

func daysFromNow(h *certHost, d float64) string {
	return h.clock.Add(time.Duration(d * float64(24*time.Hour))).Format("2006-01-02T15:04:05")
}

func TestCertWarnsOncePerThreshold(t *testing.T) {
	h := newCertHost(t, "https://example.org/")
	notAfter := ""
	h.crt = func(string) (fetchRes, error) {
		return crtBody(row("example.org", notAfter, "CN=Test CA")), nil
	}
	lookup := func() {
		h.clock = h.clock.Add(13 * time.Hour)
		certsTick()
	}
	notAfter = daysFromNow(h, 30)
	lookup()
	if len(h.mails) != 0 {
		t.Fatalf("30 days left mails %v", h.mails)
	}
	notAfter = daysFromNow(h, 20.5) // fixed instant; ~20 days left at lookup time
	lookup()
	if len(h.mails) != 1 || !strings.HasPrefix(h.mails[0], "[SSL] example.org certificate expires in 19 days") {
		t.Fatalf("mails %v", h.mails)
	}
	if !strings.Contains(h.bodies[0], "Issuer: CN=Test CA") || !strings.Contains(h.bodies[0], "Source: crt.sh") || !strings.Contains(h.bodies[0], "Expires: 20") {
		t.Fatalf("body %q", h.bodies[0])
	}
	lookup() // same threshold again: silent
	if len(h.mails) != 1 {
		t.Fatalf("repeat mails %v", h.mails)
	}
	// Six days left crosses 7 only; one mail, and 21 stays marked.
	h.crt = func(string) (fetchRes, error) {
		return crtBody(row("example.org", h.clock.Add(6*24*time.Hour+13*time.Hour+time.Hour).Format("2006-01-02T15:04:05"), "CN=Test CA")), nil
	}
	lookup()
	if len(h.mails) != 2 || !strings.Contains(h.mails[1], "in 6 days") {
		t.Fatalf("mails %v", h.mails)
	}
	if got := h.certs["example.org"].Warned; len(got) != 2 {
		t.Fatalf("warned %v", got)
	}
}

func TestCertJumpSendsOneMailForTheSmallestThreshold(t *testing.T) {
	h := newCertHost(t, "https://example.org/")
	h.crt = func(string) (fetchRes, error) {
		return crtBody(row("example.org", daysFromNow(h, 0.5), "CN=Test CA")), nil
	}
	certsTick()
	if len(h.mails) != 1 || !strings.Contains(h.mails[0], "in 0 days") {
		t.Fatalf("mails %v", h.mails)
	}
	if got := h.certs["example.org"].Warned; len(got) != 3 {
		t.Fatalf("warned %v", got)
	}
}

func TestCertRenewalResetsWarnings(t *testing.T) {
	h := newCertHost(t, "https://example.org/")
	soon := h.clock.Add(5 * 24 * time.Hour)
	h.crt = func(string) (fetchRes, error) {
		return crtBody(row("example.org", soon.Format("2006-01-02T15:04:05"), "CN=Test CA")), nil
	}
	certsTick()
	if len(h.mails) != 1 {
		t.Fatalf("mails %v", h.mails)
	}
	renewed := h.clock.Add(6 * 24 * time.Hour)
	h.crt = func(string) (fetchRes, error) {
		return crtBody(row("example.org", soon.Format("2006-01-02T15:04:05"), "CN=Old"),
			row("example.org", renewed.Format("2006-01-02T15:04:05"), "CN=New")), nil
	}
	h.clock = h.clock.Add(13 * time.Hour)
	certsTick()
	if len(h.mails) != 2 || h.certs["example.org"].Issuer != "CN=New" {
		t.Fatalf("mails %v cert %+v", h.mails, h.certs["example.org"])
	}
}

func TestCertNoAlertEmailsQueuesNothing(t *testing.T) {
	h := newCertHost(t, "https://example.org/")
	h.st.AlertEmails = nil
	h.crt = func(string) (fetchRes, error) {
		return crtBody(row("example.org", daysFromNow(h, 3), "CN=Test CA")), nil
	}
	certsTick()
	if len(h.mails) != 0 {
		t.Fatalf("mails %v", h.mails)
	}
	if c := h.certs["example.org"]; c == nil || len(c.Warned) != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestCertHTTPMonitorIsIgnored(t *testing.T) {
	h := newCertHost(t, "http://example.org/")
	h.crt = func(string) (fetchRes, error) { t.Fatal("looked up an http host"); return fetchRes{}, nil }
	certsTick()
	if len(h.fetched) != 0 || len(h.certs) != 0 {
		t.Fatalf("fetched %v", h.fetched)
	}
}

func TestCertTickFailsOpenAndLogsOnlyHostAndCode(t *testing.T) {
	f := newFakeHost(t, func(c call, out any) error { return hostErr(sdk.ErrUnavailable, "db secret detail") })
	certsTick() // no db: must not panic
	for _, l := range f.logs() {
		if strings.Contains(l, "secret") {
			t.Fatalf("log %q", l)
		}
	}
	h := newCertHost(t, "https://example.org/")
	h.crt = func(string) (fetchRes, error) { return fetchRes{}, hostErr(sdk.ErrInternal, "private detail") }
	fh := callHost
	var logs []string
	callHost = func(fn string, args, out any) error {
		if fn == "log" {
			logs = append(logs, args.(map[string]string)["message"])
		}
		return fh(fn, args, out)
	}
	certsTick()
	if len(logs) == 0 || strings.Contains(strings.Join(logs, "|"), "private") || !strings.Contains(logs[0], "host=example.org code=internal") {
		t.Fatalf("logs %v", logs)
	}
}

func TestCertOldestLookupGoesBeforeNewer(t *testing.T) {
	h := newCertHost(t, "https://g.example.org/", "https://h.example.org/")
	h.crt = func(string) (fetchRes, error) { return fetchRes{Status: 502}, nil }
	newer := h.clock.Add(-24 * time.Hour)
	older := h.clock.Add(-72 * time.Hour)
	h.certs["g.example.org"] = &cert{Host: "g.example.org", LookedUpAt: &newer}
	h.certs["h.example.org"] = &cert{Host: "h.example.org", LookedUpAt: &older}
	certsTick()
	if len(h.fetched) != 2 || !strings.Contains(h.fetched[0], "q=h.") {
		t.Fatalf("fetched %v", h.fetched)
	}
}
