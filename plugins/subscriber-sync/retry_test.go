package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// queueHost fakes the database for the cron: the queue answers the expiry and
// claim queries, everything else is recorded.
type queueHost struct {
	*fakeHost
	due     int // rows the claim query returns
	queries []hostCallRec
}

func (q *queueHost) install(t *testing.T) {
	q.fakeHost.install(t)
	base := callHost
	callHost = func(fn string, a, out any) error {
		if fn != "db.query" {
			return base(fn, a, out)
		}
		m := a.(map[string]any)
		rec := hostCallRec{fn: fn, sql: m["sql"].(string), args: m["args"].([]any)}
		q.queries = append(q.queries, rec)
		var cell any = []any{}
		switch {
		case strings.Contains(rec.sql, "FOR UPDATE SKIP LOCKED"):
			rows := []map[string]any{}
			for i := 0; i < q.due && i < batchSize; i++ {
				rows = append(rows, map[string]any{"id": i + 1, "submission_id": "s", "form_slug": "contact", "email": "ada@example.com"})
			}
			cell = rows
		}
		b, _ := json.Marshal(cell)
		return json.Unmarshal([]byte(`{"rows":[[`+string(b)+`]]}`), out)
	}
}

func newQueueHost(t *testing.T) *queueHost {
	q := &queueHost{fakeHost: &fakeHost{settings: baseSettings}}
	q.install(t)
	return q
}

func TestRetryExpiresOldRowsAndLogsThemInOneStatement(t *testing.T) {
	q := newQueueHost(t)
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	var stmts []hostCallRec
	for _, c := range q.calls {
		if c.fn == "db.exec" && strings.Contains(c.sql, "DELETE FROM queue") {
			stmts = append(stmts, c)
		}
	}
	if len(stmts) != 1 || !strings.Contains(stmts[0].sql, "INSERT INTO log") {
		t.Fatalf("expiry statements %+v", stmts)
	}
	if want := ts(now().Add(-24 * time.Hour)); stmts[0].args[0] != want {
		t.Fatalf("queue cutoff %v, want %v", stmts[0].args[0], want)
	}
	for _, c := range q.queries {
		if strings.Contains(c.sql, "DELETE FROM queue") {
			t.Fatalf("expiry read back per row: %s", c.sql)
		}
	}
}

func TestRetryClaimCutoffKeepsRowsUnder24Hours(t *testing.T) {
	q := newQueueHost(t)
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	claimQ := q.queries[0]
	if claimQ.args[1] != ts(now().Add(-24*time.Hour)) {
		t.Fatalf("claim cutoff %v", claimQ.args[1])
	}
}

func TestRetryPurgesLogAfter30Days(t *testing.T) {
	q := newQueueHost(t)
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	for _, c := range q.calls {
		if strings.HasPrefix(c.sql, "DELETE FROM log") {
			if c.args[0] != ts(now().Add(-30*24*time.Hour)) {
				t.Fatalf("log cutoff %v", c.args[0])
			}
			return
		}
	}
	t.Fatal("no log purge")
}

func TestRetryClaimIsAtomicLeaseWithBatchCap(t *testing.T) {
	q := newQueueHost(t)
	q.due = 80
	delivered := 0
	deliverFn = func(cfg, job) result { delivered++; return result{kind: kindSent} }
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	claimQ := q.queries[0]
	if !strings.Contains(claimQ.sql, "FOR UPDATE SKIP LOCKED") || !strings.Contains(claimQ.sql, "UPDATE queue") {
		t.Fatalf("claim sql %s", claimQ.sql)
	}
	if claimQ.args[2] != float64(batchSize) && claimQ.args[2] != batchSize {
		t.Fatalf("batch arg %v", claimQ.args[2])
	}
	if claimQ.args[3] != ts(now().Add(leaseTime)) {
		t.Fatalf("lease arg %v", claimQ.args[3])
	}
	if delivered != 50 {
		t.Fatalf("delivered %d", delivered)
	}
}

func TestRetryStopsWhenBudgetIsSpent(t *testing.T) {
	q := newQueueHost(t)
	q.due = 10
	clock := now()
	delivered := 0
	deliverFn = func(cfg, job) result {
		delivered++
		clock = clock.Add(3 * time.Second)
		return result{kind: kindSent}
	}
	now = func() time.Time { return clock }
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	// 8 s budget, 1.5 s minimum per call: starts at 0 s, 3 s, 6 s; 9 s is past it.
	if delivered != 3 {
		t.Fatalf("delivered %d", delivered)
	}
}

func TestRetrySkipsDeliveryWhenNotConfigured(t *testing.T) {
	q := newQueueHost(t)
	q.settings = `{}`
	q.due = 3
	deliverFn = func(cfg, job) result { t.Fatal("delivered"); return result{} }
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	if len(q.queries) != 0 {
		t.Fatalf("queries %d", len(q.queries))
	}
}

func TestRetryLogsNoEmail(t *testing.T) {
	q := newQueueHost(t)
	q.due = 1
	deliverFn = func(cfg, job) result { return result{kind: kindRetry, status: 503} }
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	for _, c := range q.calls {
		if strings.Contains(c.msg, "@") {
			t.Fatalf("email in log line %q", c.msg)
		}
		for _, a := range c.args {
			if s, ok := a.(string); ok && strings.Contains(s, "@") {
				t.Fatalf("email in args of %s", c.sql)
			}
		}
	}
}

func TestSettleBudgetCutReleasesLeaseWithoutAttemptOrLog(t *testing.T) {
	h := &fakeHost{settings: baseSettings}
	h.install(t)
	settle(job{SubmissionID: "sub-1", FormSlug: "contact", Attempts: 2}, result{kind: kindBudget, reason: "timeout"})
	if len(h.logRows()) != 0 || h.dbWrites() != 1 {
		t.Fatalf("calls %+v", h.calls)
	}
	c := h.calls[0]
	if strings.Contains(c.sql, "attempts") || !strings.HasPrefix(c.sql, "UPDATE queue SET lease_until") || c.args[1] != ts(now()) {
		t.Fatalf("%+v", c)
	}
}

func TestKitDeliveryCutByBudgetIsNotAFailure(t *testing.T) {
	f := newFake(t, fetchResp{Status: 201}, fetchResp{Code: budgetCode})
	if r := (kit{}).upsert(job{Email: "a@b.co"}, kitCfg(), f.fetch); r.kind != kindBudget {
		t.Fatalf("%+v", r)
	}
}
