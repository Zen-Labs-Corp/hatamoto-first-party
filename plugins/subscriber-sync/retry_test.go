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
	expired []string // form slugs the expiry query returns
	due     int      // rows the claim query returns
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
		case strings.Contains(rec.sql, "DELETE FROM queue"):
			rows := []map[string]any{}
			for _, s := range q.expired {
				rows = append(rows, map[string]any{"form_slug": s})
			}
			cell = rows
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

func TestRetryExpiresOldRowsAndLogsThem(t *testing.T) {
	q := newQueueHost(t)
	q.expired = []string{"contact", "news"}
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	want := ts(now().Add(-24 * time.Hour))
	if got := q.queries[0].args[0]; got != want {
		t.Fatalf("queue cutoff %v, want %v", got, want)
	}
	rows := q.logRows()
	if len(rows) != 2 || rows[0] != [2]string{"expired", "too old"} {
		t.Fatalf("log rows %v", rows)
	}
}

func TestRetryClaimCutoffKeepsRowsUnder24Hours(t *testing.T) {
	q := newQueueHost(t)
	_ = onRetry(sdk.CronEvent{Name: "retry"})
	claimQ := q.queries[1]
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
	claimQ := q.queries[1]
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
	if len(q.queries) != 1 {
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
