package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const hubspotSettings = `{"provider":"hubspot","access_token":"tok","forms":["contact"]}`

func rowJSON(id, sub, provider string, attempts int) string {
	return `{"id":` + id + `,"submission_id":"` + sub + `","form_slug":"contact","provider":"` + provider +
		`","email":"ann@example.com","fields":{"name":"Ann"},"source_path":"/","submitted_at":"2026-11-05T09:59:00Z","attempts":` +
		string(rune('0'+attempts)) + `,"next_attempt_at":"2026-11-05T09:59:00Z"}`
}

func queueRows(rows ...string) string { return `[` + strings.Join(rows, ",") + `]` }

// stubDeliver replaces deliverFn and records the submission ids it saw.
func stubDeliver(t *testing.T, fn func(j job) result) *[]string {
	t.Helper()
	var seen []string
	old := deliverFn
	deliverFn = func(_ cfg, j job, _ []mapRow) result {
		seen = append(seen, j.SubmissionID)
		return fn(j)
	}
	t.Cleanup(func() { deliverFn = old })
	return &seen
}

type stubProvider struct{ refreshed *int }

func (stubProvider) upsert(job, cfg, []mapRow, fetcher) result { return result{kind: kindCreated} }
func (stubProvider) test(cfg, fetcher) (string, result)        { return "", result{kind: kindUpdated} }
func (stubProvider) fields(cfg, fetcher) ([]crmField, result)  { return nil, result{kind: kindUpdated} }
func (s stubProvider) refresh(cfg, fetcher) result {
	if s.refreshed != nil {
		*s.refreshed++
	}
	return result{kind: kindUpdated}
}

func TestExpireDeletesOldRowsAndLogsInOneStatement(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	expire(now())
	if len(h.calls) != 1 || h.calls[0].fn != "db.exec" || !strings.HasPrefix(h.calls[0].sql, "WITH d AS (DELETE FROM queue") {
		t.Fatalf("calls = %+v", h.calls)
	}
	if got, want := h.calls[0].args[0], ts(now().Add(-24*time.Hour)); got != want {
		t.Errorf("cutoff = %v, want %v", got, want)
	}
}

func TestClaimOrdersByNextAttemptThenID(t *testing.T) {
	h := &fakeHost{queryRows: []string{`[]`}}
	h.install(t)
	if _, err := claim(now(), true); err != nil {
		t.Fatal(err)
	}
	sql := h.calls[0].sql
	if !strings.Contains(sql, "ORDER BY next_attempt_at, id") || !strings.Contains(sql, "FOR UPDATE SKIP LOCKED") {
		t.Errorf("sql = %s", sql)
	}
}

func TestClaimSeparatesNewFromRetries(t *testing.T) {
	h := &fakeHost{queryRows: []string{`[]`, `[]`}}
	h.install(t)
	claim(now(), true)
	claim(now(), false)
	if !strings.Contains(h.calls[0].sql, "(attempts = 0) = $5") {
		t.Errorf("sql = %s", h.calls[0].sql)
	}
	if h.calls[0].args[4] != true || h.calls[1].args[4] != false {
		t.Errorf("args = %v, %v", h.calls[0].args, h.calls[1].args)
	}
}

func TestRunQueueDeliversAndSettles(t *testing.T) {
	h := &fakeHost{settings: hubspotSettings, queryRows: []string{queueRows(rowJSON("1", "a", "hubspot", 0), rowJSON("2", "b", "hubspot", 0))}}
	h.install(t)
	seen := stubDeliver(t, func(j job) result {
		if j.SubmissionID == "a" {
			return result{kind: kindCreated, recordID: "9"}
		}
		return result{kind: kindFail, status: 400, reason: "rejected"}
	})
	if err := onDeliver(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 2 {
		t.Fatalf("seen = %v", *seen)
	}
	rows := h.logRows()
	if len(rows) != 2 || rows[0][0] != "created" || rows[1][0] != "failed" {
		t.Fatalf("log = %v", rows)
	}
	if len(h.execs("DELETE FROM queue")) != 2 {
		t.Errorf("calls = %+v", h.calls)
	}
}

func TestProviderChangedRowIsDropped(t *testing.T) {
	h := &fakeHost{settings: hubspotSettings, queryRows: []string{queueRows(rowJSON("1", "a", "pipedrive", 0))}}
	h.install(t)
	seen := stubDeliver(t, func(job) result { return result{kind: kindCreated} })
	onDeliver(sdk.CronEvent{})
	if len(*seen) != 0 {
		t.Fatalf("delivered %v", *seen)
	}
	rows := h.logRows()
	if len(rows) != 1 || rows[0] != [2]string{"skipped", "provider changed"} {
		t.Fatalf("log = %v", rows)
	}
	if len(h.execs("DELETE FROM queue")) != 1 {
		t.Errorf("calls = %+v", h.calls)
	}
}

func TestAlwaysCutJobDoesNotBlockQueue(t *testing.T) {
	h := &fakeHost{settings: hubspotSettings, queryRows: []string{queueRows(rowJSON("1", "a", "hubspot", 0), rowJSON("2", "b", "hubspot", 0), rowJSON("3", "c", "hubspot", 0))}}
	h.install(t)
	stubDeliver(t, func(j job) result {
		if j.SubmissionID == "a" {
			return result{kind: kindBudget}
		}
		return result{kind: kindCreated}
	})
	onDeliver(sdk.CronEvent{})
	up := h.execs("UPDATE queue SET attempts = attempts + 1")
	if len(up) != 1 || up[0].args[0] != "a" {
		t.Fatalf("calls = %+v", h.calls)
	}
	rows := h.logRows()
	if len(rows) != 3 || rows[0] != [2]string{"retrying", "attempt 1, too slow"} || rows[1][0] != "created" || rows[2][0] != "created" {
		t.Fatalf("log = %v", rows)
	}
}

func TestSlowCut(t *testing.T) {
	cut := result{kind: kindBudget}
	if r := slowCut(cut, cronBudget, cronBudget); r.kind != kindRetry || r.reason != "too slow" {
		t.Errorf("fresh budget: %+v", r)
	}
	if r := slowCut(cut, cronBudget-minCall-time.Millisecond, cronBudget); r.kind != kindBudget {
		t.Errorf("spent budget: %+v", r)
	}
	if r := slowCut(result{kind: kindCreated}, cronBudget, cronBudget); r.kind != kindCreated {
		t.Errorf("other kind: %+v", r)
	}
}

func TestUnreachedRowsAreReleased(t *testing.T) {
	h := &fakeHost{settings: hubspotSettings, queryRows: []string{queueRows(rowJSON("1", "a", "hubspot", 0), rowJSON("2", "b", "hubspot", 0), rowJSON("3", "c", "hubspot", 0))}}
	h.install(t)
	stubDeliver(t, func(job) result {
		now = func() time.Time { return time.Date(2026, 11, 5, 10, 0, 8, 0, time.UTC) }
		return result{kind: kindCreated}
	})
	onDeliver(sdk.CronEvent{})
	rel := h.execs("UPDATE queue SET lease_until")
	if len(rel) != 2 || rel[0].args[0] != "b" || rel[1].args[0] != "c" {
		t.Fatalf("calls = %+v", h.calls)
	}
	if rows := h.logRows(); len(rows) != 1 || rows[0][0] != "created" {
		t.Fatalf("log = %v", rows)
	}
}

func TestBudgetResetAfterCron(t *testing.T) {
	h := &fakeHost{settings: hubspotSettings, queryRows: []string{`[]`, `[]`}}
	h.install(t)
	providers["hubspot"] = stubProvider{}
	t.Cleanup(func() { delete(providers, "hubspot") })
	for name, fn := range map[string]func(sdk.CronEvent) error{"deliver": onDeliver, "retry": onRetry, "refresh": onRefresh} {
		if err := fn(sdk.CronEvent{}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !budgetEnd.IsZero() {
			t.Errorf("%s left budgetEnd set", name)
		}
	}
}

func TestOnRefreshIgnoresUnconfigured(t *testing.T) {
	for _, s := range []string{`{}`, `{"provider":"hubspot"}`, `{"provider":"salesforce","client_id":"x"}`} {
		h := &fakeHost{settings: s}
		h.install(t)
		n := 0
		providers["hubspot"] = stubProvider{refreshed: &n}
		providers["salesforce"] = stubProvider{refreshed: &n}
		if err := onRefresh(sdk.CronEvent{}); err != nil || n != 0 || h.fetchCalls() != 0 {
			t.Errorf("%s: err=%v refreshed=%d", s, err, n)
		}
		delete(providers, "hubspot")
		delete(providers, "salesforce")
	}
}

func TestCronsFailOpen(t *testing.T) {
	h := &fakeHost{settings: `not json`}
	h.install(t)
	for name, fn := range map[string]func(sdk.CronEvent) error{"deliver": onDeliver, "retry": onRetry, "refresh": onRefresh} {
		if err := fn(sdk.CronEvent{}); err != nil {
			t.Errorf("%s returned %v", name, err)
		}
	}
	callHost = func(string, any, any) error { return errors.New("host down") }
	for name, fn := range map[string]func(sdk.CronEvent) error{"deliver": onDeliver, "retry": onRetry, "refresh": onRefresh} {
		if err := fn(sdk.CronEvent{}); err != nil {
			t.Errorf("%s returned %v", name, err)
		}
	}
}
