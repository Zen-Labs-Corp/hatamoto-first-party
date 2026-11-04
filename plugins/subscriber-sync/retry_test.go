package main

import (
	"encoding/json"
	"sort"
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

func TestBudgetCutJobDoesNotBlockTheQueue(t *testing.T) {
	newQueueHost(t)
	base := now()
	mem := map[string]time.Time{"x": base.Add(-3 * time.Minute), "a": base.Add(-2 * time.Minute), "b": base.Add(-time.Minute)}
	ids := map[string]int{"x": 1, "a": 2, "b": 3}
	inner := callHost
	callHost = func(fn string, a, out any) error {
		m, _ := a.(map[string]any)
		sql, _ := m["sql"].(string)
		switch {
		case fn == "db.query" && strings.Contains(sql, "FOR UPDATE SKIP LOCKED"):
			if !strings.Contains(sql, "ORDER BY next_attempt_at, id") {
				t.Fatalf("claim order: %s", sql)
			}
			var subs []string
			for s := range mem {
				subs = append(subs, s)
			}
			sort.Slice(subs, func(i, j int) bool {
				if !mem[subs[i]].Equal(mem[subs[j]]) {
					return mem[subs[i]].Before(mem[subs[j]])
				}
				return ids[subs[i]] < ids[subs[j]]
			})
			rows := []map[string]any{}
			for _, s := range subs {
				rows = append(rows, map[string]any{"id": ids[s], "submission_id": s, "form_slug": "contact", "email": "ada@example.com"})
			}
			b, _ := json.Marshal(rows)
			return json.Unmarshal([]byte(`{"rows":[[`+string(b)+`]]}`), out)
		case fn == "db.exec" && strings.HasPrefix(sql, "UPDATE queue SET lease_until") && strings.Contains(sql, "next_attempt_at = $2"):
			tm, _ := time.Parse(time.RFC3339Nano, m["args"].([]any)[1].(string))
			mem[m["args"].([]any)[0].(string)] = tm
		case fn == "db.exec" && strings.HasPrefix(sql, "DELETE FROM queue WHERE submission_id"):
			delete(mem, m["args"].([]any)[0].(string))
		}
		return inner(fn, a, out)
	}
	clock := base
	now = func() time.Time { return clock }
	var order []string
	deliverFn = func(_ cfg, j job) result {
		order = append(order, j.SubmissionID)
		switch j.SubmissionID {
		case "x":
			clock = clock.Add(3 * time.Second)
			return result{kind: kindSent}
		case "a":
			clock = clock.Add(5 * time.Second) // the budget runs out inside the call
			return result{kind: kindBudget, reason: "timeout"}
		}
		return result{kind: kindSent}
	}
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	clock = base.Add(10 * time.Second)
	order = nil
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	if len(order) == 0 || order[0] != "b" {
		t.Fatalf("second run order %v, want b first", order)
	}
}

func TestSlowCut(t *testing.T) {
	cut := result{kind: kindBudget, reason: "timeout"}
	if r := slowCut(cut, cronBudget, cronBudget); r.kind != kindRetry || r.reason != "too slow" {
		t.Fatalf("fresh budget: %+v", r)
	}
	if r := slowCut(cut, cronBudget/2, cronBudget); r != cut {
		t.Fatalf("spent budget: %+v", r)
	}
	if r := slowCut(result{kind: kindSent}, cronBudget, cronBudget); r.kind != kindSent {
		t.Fatalf("%+v", r)
	}
}

func TestFreshJobAlwaysCutIsCountedAndBackedOff(t *testing.T) {
	q := newQueueHost(t)
	q.due = 1
	deliverFn = func(cfg, job) result { return result{kind: kindBudget, reason: "timeout"} }
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	rows := q.logRows()
	if len(rows) != 1 || rows[0][0] != "retrying" || !strings.Contains(rows[0][1], "too slow") {
		t.Fatalf("log rows %+v", rows)
	}
}

func TestTagsAreCappedAtTen(t *testing.T) {
	if got := loadManifest(t).Schema.Properties["tags"]["maxItems"]; got != float64(10) {
		t.Fatalf("tags maxItems %v", got)
	}
}
