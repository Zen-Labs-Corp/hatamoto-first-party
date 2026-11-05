package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// sentMail is one row the plugin queued.
type sentMail struct {
	subject, body string
	to            []string
}

// alertDB is a small fake of the incident and outbox tables, kept by the host
// fake. It also records every check row.
type alertDB struct {
	incidents []incident
	mails     []sentMail
	checks    []call
	states    []call
}

func newAlertDB(t *testing.T) (*alertDB, *fakeHost) {
	t.Helper()
	d := &alertDB{}
	f := newFakeHost(t, func(c call, out any) error {
		sql := c.sql()
		switch {
		case strings.HasPrefix(sql, "INSERT INTO incidents"):
			at, _ := time.Parse(time.RFC3339, c.params()[1].(string))
			d.incidents = append(d.incidents, incident{ID: int64(len(d.incidents) + 1), MonitorID: int64(c.params()[0].(float64)), StartedAt: at, Cause: c.params()[2].(string)})
		case strings.Contains(sql, "FROM incidents"):
			var open []incident
			for _, i := range d.incidents {
				if i.EndedAt == nil {
					open = append(open, i)
				}
			}
			b, _ := json.Marshal(open)
			return cell(out, string(b))
		case strings.HasPrefix(sql, "UPDATE incidents"):
			at, _ := time.Parse(time.RFC3339, c.params()[1].(string))
			for i := range d.incidents {
				if d.incidents[i].EndedAt == nil {
					d.incidents[i].EndedAt = &at
				}
			}
		case strings.HasPrefix(sql, "INSERT INTO outbox"):
			var to []string
			_ = json.Unmarshal([]byte(c.params()[2].(string)), &to)
			d.mails = append(d.mails, sentMail{c.params()[0].(string), c.params()[1].(string), to})
		case strings.HasPrefix(sql, "INSERT INTO checks"):
			d.checks = append(d.checks, c)
		case strings.HasPrefix(sql, "UPDATE monitors"):
			d.states = append(d.states, c)
		}
		return nil
	})
	return d, f
}

var (
	alertSettings = settings{AlertEmails: []string{"owner@example.org"}, FailThreshold: 3, SSLWarnDays: []int{21, 7, 1}}
	downResult    = result{Status: statusDown, HTTP: 503, MS: 40, Cause: "HTTP 503"}
	upResult      = result{Status: statusUp, HTTP: 200, MS: 90}
)

// run applies each result one minute apart, as ticks would.
func run(t *testing.T, m monitor, st settings, start time.Time, results ...result) monitor {
	t.Helper()
	at := start
	for _, r := range results {
		now = func() time.Time { return at }
		var err error
		if m, err = applyResult(m, r, st); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Minute)
	}
	return m
}

var t0 = time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)

func alertMonitor() monitor {
	return monitor{ID: 5, Name: "Shop", URL: "https://shop.example.org/", IntervalS: 60}
}

func TestAlertOneMailAtThreshold(t *testing.T) {
	d, _ := newAlertDB(t)
	m := run(t, alertMonitor(), alertSettings, t0, downResult, downResult)
	if len(d.mails) != 0 || len(d.incidents) != 0 || m.AlertedDown || m.FailCount != 2 || m.Status != statusDown {
		t.Fatalf("before the threshold: mails %d incidents %d %+v", len(d.mails), len(d.incidents), m)
	}
	m = run(t, m, alertSettings, t0.Add(2*time.Minute), downResult)
	if len(d.mails) != 1 || len(d.incidents) != 1 || !m.AlertedDown {
		t.Fatalf("at the threshold: mails %d incidents %d %+v", len(d.mails), len(d.incidents), m)
	}
	mail := d.mails[0]
	if mail.subject != "[Down] Shop is down" || len(mail.to) != 1 || mail.to[0] != "owner@example.org" {
		t.Fatalf("mail %+v", mail)
	}
	for _, want := range []string{"https://shop.example.org/", "Cause: HTTP 503", "Since: 2026-02-01 12:00 UTC", "Checks failed: 3"} {
		if !strings.Contains(mail.body, want) {
			t.Fatalf("body lacks %q:\n%s", want, mail.body)
		}
	}
	if !d.incidents[0].StartedAt.Equal(t0) {
		t.Fatalf("incident starts at the first failure, got %v", d.incidents[0].StartedAt)
	}
	run(t, m, alertSettings, t0.Add(3*time.Minute), downResult, downResult, downResult)
	if len(d.mails) != 1 || len(d.incidents) != 1 {
		t.Fatalf("further failures: mails %d incidents %d", len(d.mails), len(d.incidents))
	}
}

func TestAlertRecoveryMailOnce(t *testing.T) {
	d, _ := newAlertDB(t)
	m := run(t, alertMonitor(), alertSettings, t0, downResult, downResult, downResult)
	m = run(t, m, alertSettings, t0.Add(125*time.Minute), upResult)
	if len(d.mails) != 2 || m.AlertedDown || m.FailCount != 0 || m.DownSince != nil || m.Status != statusUp {
		t.Fatalf("mails %d %+v", len(d.mails), m)
	}
	mail := d.mails[1]
	if mail.subject != "[Up] Shop is back up" {
		t.Fatalf("subject %q", mail.subject)
	}
	for _, want := range []string{"Downtime: 2 h 5 min", "Cause: HTTP 503", "https://shop.example.org/"} {
		if !strings.Contains(mail.body, want) {
			t.Fatalf("body lacks %q:\n%s", want, mail.body)
		}
	}
	if d.incidents[0].EndedAt == nil {
		t.Fatal("incident not closed")
	}
	run(t, m, alertSettings, t0.Add(130*time.Minute), upResult, upResult)
	if len(d.mails) != 2 {
		t.Fatalf("another recovery mail: %d", len(d.mails))
	}
}

func TestAlertRecoveryBeforeThresholdIsSilent(t *testing.T) {
	d, _ := newAlertDB(t)
	m := run(t, alertMonitor(), alertSettings, t0, downResult, downResult, upResult)
	if len(d.mails) != 0 || len(d.incidents) != 0 || m.FailCount != 0 || m.DownSince != nil {
		t.Fatalf("mails %d incidents %d %+v", len(d.mails), len(d.incidents), m)
	}
	// The counter starts again: two more failures are still below the threshold.
	run(t, m, alertSettings, t0.Add(10*time.Minute), downResult, downResult)
	if len(d.mails) != 0 {
		t.Fatal("failures must not add up across a recovery")
	}
}

func TestAlertSlowCountsAsUp(t *testing.T) {
	d, _ := newAlertDB(t)
	m := run(t, alertMonitor(), alertSettings, t0, downResult, downResult, downResult, result{Status: statusSlow, HTTP: 200, MS: 9000})
	if m.Status != statusSlow || m.AlertedDown || len(d.mails) != 2 {
		t.Fatalf("slow recovers an incident: mails %d %+v", len(d.mails), m)
	}
}

func TestAlertExpiredCertificateAlertsAtOnce(t *testing.T) {
	d, _ := newAlertDB(t)
	expired := result{Status: statusDown, Cause: "certificate expired on 2026-01-20"}
	m := run(t, alertMonitor(), alertSettings, t0, expired)
	if len(d.mails) != 1 || !m.AlertedDown || m.FailCount != 1 {
		t.Fatalf("mails %d %+v", len(d.mails), m)
	}
	if !strings.Contains(d.mails[0].body, "certificate expired on 2026-01-20") {
		t.Fatal(d.mails[0].body)
	}
	run(t, m, alertSettings, t0.Add(time.Minute), expired)
	if len(d.mails) != 1 {
		t.Fatal("only one alert per incident")
	}
}

func TestAlertConfigIsNeverAnAlert(t *testing.T) {
	d, f := newAlertDB(t)
	cfg := result{Status: statusConfig, Cause: "fetch blocked: host is not allowed"}
	m := run(t, alertMonitor(), alertSettings, t0, cfg, cfg, cfg, cfg)
	if len(d.mails) != 0 || len(d.incidents) != 0 || m.FailCount != 0 || m.Status != statusConfig || m.AlertedDown {
		t.Fatalf("mails %d %+v", len(d.mails), m)
	}
	if len(d.checks) != 4 || d.checks[0].params()[4] != nil {
		t.Fatalf("config checks are stored with ok NULL: %v", d.checks[0].params())
	}
	_ = f
	// A config check in the middle of an outage keeps the counter and the incident.
	d2, _ := newAlertDB(t)
	m = run(t, alertMonitor(), alertSettings, t0, downResult, downResult, cfg, downResult)
	if len(d2.mails) != 1 || m.FailCount != 3 {
		t.Fatalf("mails %d %+v", len(d2.mails), m)
	}
}

func TestAlertSkipChangesNothing(t *testing.T) {
	d, f := newAlertDB(t)
	m := alertMonitor()
	got := run(t, m, alertSettings, t0, result{Skip: true})
	if got.LastCheck != nil || len(f.sqls()) != 0 || len(d.mails) != 0 {
		t.Fatalf("a skipped check wrote %v", f.sqls())
	}
}

func TestAlertNoEmailsQueuesNothingAndStaysUnalerted(t *testing.T) {
	d, _ := newAlertDB(t)
	st := alertSettings
	st.AlertEmails = nil
	m := run(t, alertMonitor(), st, t0, downResult, downResult, downResult, downResult)
	if len(d.mails) != 0 || len(d.incidents) != 1 || m.AlertedDown {
		t.Fatalf("mails %d incidents %d %+v", len(d.mails), len(d.incidents), m)
	}
	// An address appears while the outage lasts: the down mail is queued once,
	// on the incident that is already open.
	m = run(t, m, alertSettings, t0.Add(10*time.Minute), downResult, downResult)
	if len(d.mails) != 1 || len(d.incidents) != 1 || !m.AlertedDown {
		t.Fatalf("mails %d incidents %d %+v", len(d.mails), len(d.incidents), m)
	}
	run(t, m, alertSettings, t0.Add(time.Hour), upResult)
	if len(d.mails) != 2 || d.incidents[0].EndedAt == nil {
		t.Fatalf("mails %d", len(d.mails))
	}
}

func TestAlertUnalertedIncidentIsClosedOnRecovery(t *testing.T) {
	d, _ := newAlertDB(t)
	st := alertSettings
	st.AlertEmails = nil
	run(t, run(t, alertMonitor(), st, t0, downResult, downResult, downResult), st, t0.Add(time.Hour), upResult)
	if len(d.mails) != 0 || d.incidents[0].EndedAt == nil {
		t.Fatalf("mails %d incident %+v", len(d.mails), d.incidents[0])
	}
}

func TestAlertRetryAfterPartialFailureKeepsOneIncident(t *testing.T) {
	d, f := newAlertDB(t)
	inner := f.reply
	fail := true
	f.reply = func(c call, out any) error {
		if fail && strings.HasPrefix(c.sql(), "INSERT INTO outbox") {
			return hostErr("unavailable", "db down")
		}
		return inner(c, out)
	}
	m := alertMonitor()
	now = func() time.Time { return t0 }
	for range 2 { // the mail cannot be queued: state is not saved, the next tick retries
		if _, err := applyResult(monitorAtThreshold(m), downResult, alertSettings); err == nil {
			t.Fatal("expected the queue error")
		}
	}
	fail = false
	got, err := applyResult(monitorAtThreshold(m), downResult, alertSettings)
	if err != nil || len(d.incidents) != 1 || len(d.mails) != 1 || !got.AlertedDown {
		t.Fatalf("err %v incidents %d mails %d", err, len(d.incidents), len(d.mails))
	}
}

// monitorAtThreshold is m as saved after the failures before the alert.
func monitorAtThreshold(m monitor) monitor {
	m.FailCount, m.DownSince = alertSettings.FailThreshold-1, &t0
	return m
}

func TestAlertStateIsSaved(t *testing.T) {
	d, _ := newAlertDB(t)
	m := run(t, alertMonitor(), alertSettings, t0, downResult)
	p := d.states[0].params()
	// id, status, last_check, next_check, last_ms, last_status, last_error, fail_count, down_since, alerted_down
	if p[1] != "down" || p[2] != "2026-02-01T12:00:00Z" || p[3] != "2026-02-01T12:01:00Z" || p[4] != float64(40) ||
		p[5] != float64(503) || p[6] != "HTTP 503" || p[7] != float64(1) || p[8] != "2026-02-01T12:00:00Z" || p[9] != false {
		t.Fatalf("params %v", p)
	}
	if !m.NextCheck.Equal(t0.Add(time.Minute)) {
		t.Fatalf("next check %v", m.NextCheck)
	}
	for _, c := range d.states {
		oneStatement(t, c.sql())
	}
}

func TestAlertFailsOpenOnDatabaseErrors(t *testing.T) {
	newFakeHost(t, func(call, any) error { return hostErr("unavailable", "no database") })
	now = func() time.Time { return t0 }
	if _, err := applyResult(alertMonitor(), upResult, alertSettings); err == nil {
		t.Fatal("a database error must reach the caller, which logs it")
	}
}

func TestMailSubjectIsOneShortLine(t *testing.T) {
	s := mailSubject("[Down] ", "Shop\r\nBcc: x@example.org", " is down")
	if strings.ContainsAny(s, "\r\n") || s != "[Down] Shop Bcc: x@example.org is down" {
		t.Fatalf("subject %q", s)
	}
	long := mailSubject("[Down] ", strings.Repeat("é", 300), " is down")
	if len(long) > 200 || !strings.HasSuffix(long, " is down") {
		t.Fatalf("long subject %d", len(long))
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second:             "1 min",
		6 * time.Minute:              "6 min",
		60 * time.Minute:             "1 h",
		125 * time.Minute:            "2 h 5 min",
		26*time.Hour + 3*time.Minute: "1 d 2 h 3 min",
		48 * time.Hour:               "2 d",
	} {
		if got := duration(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
