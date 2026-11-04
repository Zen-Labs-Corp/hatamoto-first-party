package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

type hostCallRec struct {
	fn   string
	sql  string
	args []any
	msg  string
}

// fakeHost answers settings.get, records every other call and reports one
// affected row for inserts unless dup is set.
type fakeHost struct {
	settings string
	dup      bool
	calls    []hostCallRec
}

func (h *fakeHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, a, out any) error {
		switch fn {
		case "settings.get":
			return json.Unmarshal([]byte(h.settings), out)
		case "db.exec":
			m := a.(map[string]any)
			h.calls = append(h.calls, hostCallRec{fn: fn, sql: m["sql"].(string), args: m["args"].([]any)})
			if out != nil {
				n := 1
				if h.dup {
					n = 0
				}
				return json.Unmarshal([]byte(`{"rows_affected":`+string(rune('0'+n))+`}`), out)
			}
		case "log":
			h.calls = append(h.calls, hostCallRec{fn: fn, msg: a.(map[string]string)["message"]})
		default:
			t.Fatalf("unexpected host call %q", fn)
		}
		return nil
	}
	now = func() time.Time { return time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC) }
	budgetEnd = time.Time{}
	t.Cleanup(func() { now = time.Now; deliverFn = deliver; budgetEnd = time.Time{} })
}

const baseSettings = `{"provider":"mailchimp","api_key":"SECRETKEY-us1","list_id":"abc","forms":["contact"],"first_name_field":"first","last_name_field":"last"}`

func event(slug string, fields map[string]any) sdk.FormSubmittedEvent {
	return sdk.FormSubmittedEvent{FormSlug: slug, SubmissionID: "sub-1", Fields: fields}
}

// logRows returns the (status, reason) of every log insert.
func (h *fakeHost) logRows() [][2]string {
	var rows [][2]string
	for _, c := range h.calls {
		if c.fn == "db.exec" && strings.HasPrefix(c.sql, "INSERT INTO log") {
			rows = append(rows, [2]string{c.args[2].(string), c.args[3].(string)})
		}
	}
	return rows
}

func (h *fakeHost) dbWrites() int {
	n := 0
	for _, c := range h.calls {
		if c.fn == "db.exec" {
			n++
		}
	}
	return n
}

func TestConsentMissingIsSkipped(t *testing.T) {
	h := &fakeHost{settings: strings.Replace(baseSettings, `"forms"`, `"consent_field":"agree","forms"`, 1)}
	h.install(t)
	deliverFn = func(cfg, job) result { t.Fatal("delivered without consent"); return result{} }
	for _, v := range []any{nil, false, "no", "", float64(0), []any{}} {
		h.calls = nil
		if err := onFormSubmitted(event("contact", map[string]any{"email": "a@b.co", "agree": v})); err != nil {
			t.Fatal(err)
		}
		rows := h.logRows()
		if len(rows) != 1 || rows[0] != [2]string{"skipped", "no consent"} || h.dbWrites() != 1 {
			t.Fatalf("consent %v: rows %v writes %d", v, rows, h.dbWrites())
		}
	}
}

func TestConsentTruthyValuesPass(t *testing.T) {
	h := &fakeHost{settings: strings.Replace(baseSettings, `"forms"`, `"consent_field":"agree","forms"`, 1)}
	h.install(t)
	for _, v := range []any{true, "YES", " on ", "Checked", "y", "1", "true", float64(1)} {
		sent := false
		deliverFn = func(cfg, job) result { sent = true; return result{kind: kindSent, status: 200} }
		if err := onFormSubmitted(event("contact", map[string]any{"email": "a@b.co", "agree": v})); err != nil {
			t.Fatal(err)
		}
		if !sent {
			t.Fatalf("consent %v was not accepted", v)
		}
	}
}

func TestUnwatchedAndEmptyFormsDoNothing(t *testing.T) {
	for name, settings := range map[string]string{
		"unwatched":   baseSettings,
		"empty forms": strings.Replace(baseSettings, `["contact"]`, `[]`, 1),
		"no forms":    `{"provider":"mailchimp","api_key":"k-us1","list_id":"x"}`,
	} {
		h := &fakeHost{settings: settings}
		h.install(t)
		deliverFn = func(cfg, job) result { t.Fatal("delivered"); return result{} }
		if err := onFormSubmitted(event("other", map[string]any{"email": "a@b.co"})); err != nil {
			t.Fatal(err)
		}
		if name == "empty forms" {
			if err := onFormSubmitted(event("contact", map[string]any{"email": "a@b.co"})); err != nil {
				t.Fatal(err)
			}
		}
		if len(h.calls) != 0 {
			t.Fatalf("%s: host calls %+v", name, h.calls)
		}
	}
}

func TestIncompleteSettingsLogOneRow(t *testing.T) {
	for _, s := range []string{
		`{"forms":["contact"]}`,
		`{"provider":"mailchimp","list_id":"x","forms":["contact"]}`,
		`{"provider":"nope","api_key":"k","list_id":"x","forms":["contact"]}`,
	} {
		h := &fakeHost{settings: s}
		h.install(t)
		if err := onFormSubmitted(event("contact", map[string]any{"email": "a@b.co"})); err != nil {
			t.Fatal(err)
		}
		rows := h.logRows()
		if len(rows) != 1 || rows[0] != [2]string{"skipped", "not configured"} || h.dbWrites() != 1 {
			t.Fatalf("%s: rows %v", s, rows)
		}
	}
}

func TestInvalidEmailIsSkipped(t *testing.T) {
	h := &fakeHost{settings: baseSettings}
	h.install(t)
	deliverFn = func(cfg, job) result { t.Fatal("delivered"); return result{} }
	for _, e := range []any{nil, "", "nope", "a@b", "a@@b.co", "a b@c.de", "@b.co", "a@.co", strings.Repeat("a", 250) + "@b.co", 5.0} {
		h.calls = nil
		if err := onFormSubmitted(event("contact", map[string]any{"email": e})); err != nil {
			t.Fatal(err)
		}
		rows := h.logRows()
		if len(rows) != 1 || rows[0] != [2]string{"skipped", "invalid email"} {
			t.Fatalf("email %v: rows %v", e, rows)
		}
	}
}

func TestSuccessQueuesDeliversAndClears(t *testing.T) {
	h := &fakeHost{settings: baseSettings}
	h.install(t)
	var got job
	deliverFn = func(c cfg, j job) result {
		got = j
		if !budgetEnd.Equal(now().Add(hookBudget)) {
			t.Fatalf("budget end %v", budgetEnd)
		}
		return result{kind: kindUpdated, status: 200}
	}
	fields := map[string]any{"email": " Ada@Example.com ", "first": "Ada", "last": "Lovelace"}
	guestIn, _ := json.Marshal(sdk.FormSubmittedEvent{FormSlug: "contact", SubmissionID: "sub-1", Fields: fields})
	if _, err := guest.Handle(sdk.FormSubmitted.Export(), guestIn); err != nil {
		t.Fatal(err)
	}
	if got.Email != "Ada@Example.com" || got.FirstName != "Ada" || got.LastName != "Lovelace" || got.SubmissionID != "sub-1" {
		t.Fatalf("job %+v", got)
	}
	if !strings.HasPrefix(h.calls[0].sql, "INSERT INTO queue") || !strings.HasPrefix(h.calls[1].sql, "DELETE FROM queue") {
		t.Fatalf("calls %+v", h.calls)
	}
	if rows := h.logRows(); len(rows) != 1 || rows[0][0] != "updated" {
		t.Fatalf("rows %v", rows)
	}
}

func TestAlreadyQueuedSubmissionIsNotDeliveredTwice(t *testing.T) {
	h := &fakeHost{settings: baseSettings, dup: true}
	h.install(t)
	deliverFn = func(cfg, job) result { t.Fatal("delivered twice"); return result{} }
	if err := onFormSubmitted(event("contact", map[string]any{"email": "a@b.co"})); err != nil {
		t.Fatal(err)
	}
}

func TestRetryAndFailureOutcomes(t *testing.T) {
	h := &fakeHost{settings: baseSettings}
	h.install(t)
	settle(job{SubmissionID: "s", FormSlug: "contact", Attempts: 1}, result{kind: kindRetry, status: 503})
	settle(job{SubmissionID: "s", FormSlug: "contact"}, result{kind: kindFail, status: 400, reason: "rejected"})
	rows := h.logRows()
	if len(rows) != 2 || rows[0] != [2]string{"retrying", "attempt 2"} || rows[1] != [2]string{"failed", "rejected"} {
		t.Fatalf("rows %v", rows)
	}
	up := h.calls[0]
	if !strings.HasPrefix(up.sql, "UPDATE queue") || up.args[1] != "2026-11-04T10:10:00Z" {
		t.Fatalf("reschedule %+v", up)
	}
	var deleted bool
	for _, c := range h.calls {
		deleted = deleted || strings.HasPrefix(c.sql, "DELETE FROM queue")
	}
	if !deleted {
		t.Fatal("failed job stayed in the queue")
	}
}

func TestBackoffDoublesToTwoHours(t *testing.T) {
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 2 * time.Hour, 2 * time.Hour}
	for i, w := range want {
		if got := backoff(i); got != w {
			t.Fatalf("backoff(%d) = %v, want %v", i, got, w)
		}
	}
	if backoff(60) != 2*time.Hour {
		t.Fatal("large attempt count not capped")
	}
}

// No email, name or key may reach a log row or a host log line.
func TestNoPersonalDataInLogs(t *testing.T) {
	h := &fakeHost{settings: baseSettings}
	h.install(t)
	secrets := []string{"ada@example.com", "Ada", "Lovelace", "SECRETKEY"}
	for _, r := range []result{
		{kind: kindSent, status: 200}, {kind: kindUpdated, status: 200},
		{kind: kindRetry, status: 503}, {kind: kindRetry, reason: "network"},
		{kind: kindFail, status: 400, reason: "rejected"},
	} {
		deliverFn = func(cfg, job) result { return r }
		h.calls = nil
		fields := map[string]any{"email": "ada@example.com", "first": "Ada", "last": "Lovelace"}
		if err := onFormSubmitted(event("contact", fields)); err != nil {
			t.Fatal(err)
		}
		for _, c := range h.calls {
			if strings.HasPrefix(c.sql, "INSERT INTO queue") {
				continue // the queue holds the email until delivery, by design
			}
			dump := c.msg + c.sql + toJSON(c.args)
			for _, s := range secrets {
				if strings.Contains(dump, s) {
					t.Fatalf("%q leaked into %+v", s, c)
				}
			}
		}
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestTruthyAndValidEmail(t *testing.T) {
	if truthy("no") || truthy(nil) || truthy(2.0) || !truthy("Y") {
		t.Fatal("truthy")
	}
	if !truthy([]any{"yes"}) || !truthy([]any{"", "newsletter"}) || truthy([]any{}) || truthy([]any{""}) || truthy([]any{2.0}) {
		t.Fatal("truthy list")
	}
	for _, ok := range []string{"a@b.co", "first.last+tag@sub.example.org"} {
		if !validEmail(ok) {
			t.Fatalf("%q rejected", ok)
		}
	}
	if validEmail("Ada <a@b.co>") || validEmail("a@b..co") {
		t.Fatal("bad address accepted")
	}
}
