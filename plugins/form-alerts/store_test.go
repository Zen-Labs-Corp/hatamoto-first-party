package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEnqueueReportsDuplicates(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	j := job{SubmissionID: "42", FormSlug: "contact", Channel: chanSlack, Body: "{}"}
	if fresh, err := enqueue(j); err != nil || !fresh {
		t.Fatalf("fresh %v err %v", fresh, err)
	}
	c := h.calls[0]
	if !strings.Contains(c.sql, "ON CONFLICT (submission_id, channel) DO NOTHING") || c.args[2] != chanSlack {
		t.Fatalf("call %+v", c)
	}
	h.dup = true
	if fresh, err := enqueue(j); err != nil || fresh {
		t.Fatalf("duplicate: fresh %v err %v", fresh, err)
	}
}

func TestSettleTouchesOnlyItsChannel(t *testing.T) {
	for _, r := range []result{{kind: kindSent, status: 200}, {kind: kindFail, status: 400}, {kind: kindRetry, status: 503}, {kind: kindBudget}} {
		h := &fakeHost{}
		h.install(t)
		settle(job{SubmissionID: "42", FormSlug: "contact", Channel: chanTeams}, r)
		var writes int
		for _, c := range h.calls {
			if c.fn == "db.exec" && !strings.HasPrefix(c.sql, "INSERT INTO log") {
				writes++
				if !strings.Contains(c.sql, "submission_id = $1 AND channel = $2") || c.args[0] != "42" || c.args[1] != chanTeams {
					t.Fatalf("%s: statement %q args %v", r.kind, c.sql, c.args)
				}
			}
		}
		if writes != 1 {
			t.Fatalf("%s: %d queue statements", r.kind, writes)
		}
	}
}

func TestSettleLogRows(t *testing.T) {
	want := map[string]string{kindSent: "sent", kindFail: "failed", kindRetry: "retrying", kindBudget: ""}
	for kind, status := range want {
		h := &fakeHost{}
		h.install(t)
		settle(job{SubmissionID: "42", FormSlug: "contact", Channel: chanSlack}, result{kind: kind, status: 500})
		var got []string
		for _, c := range h.calls {
			if c.fn == "db.exec" && strings.HasPrefix(c.sql, "INSERT INTO log") {
				got = append(got, c.args[4].(string))
			}
		}
		if status == "" && len(got) != 0 || status != "" && (len(got) != 1 || got[0] != status) {
			t.Fatalf("%s: log rows %v", kind, got)
		}
	}
}

func TestRetrySchedulesWithRetryAfter(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	settle(job{SubmissionID: "42", Channel: chanSlack, Attempts: 0}, result{kind: kindRetry, status: 429, retryAfter: 10 * time.Minute})
	c := h.calls[0]
	if !strings.Contains(c.sql, "attempts = attempts + 1") || c.args[2] != "2026-11-04T10:10:00Z" {
		t.Fatalf("call %+v", c)
	}
}

func TestDelayFor(t *testing.T) {
	for _, c := range []struct {
		attempts int
		after    time.Duration
		want     time.Duration
	}{
		{0, 0, 5 * time.Minute},
		{0, 30 * time.Second, 5 * time.Minute},
		{0, 10 * time.Minute, 10 * time.Minute},
		{0, 3 * time.Hour, time.Hour},
		{5, 0, time.Hour},
		{20, 0, time.Hour},
	} {
		if got := delayFor(c.attempts, c.after); got != c.want {
			t.Errorf("delayFor(%d, %v) = %v, want %v", c.attempts, c.after, got, c.want)
		}
	}
}

func TestExpireAndPruneAreOneStatementEach(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	expire(now())
	if len(h.calls) != 1 || !strings.Contains(h.calls[0].sql, "DELETE FROM queue") || !strings.Contains(h.calls[0].sql, "'expired'") {
		t.Fatalf("expire calls %+v", h.calls)
	}
	if h.calls[0].args[0] != "2026-11-04T04:00:00Z" {
		t.Fatalf("expire cutoff %v", h.calls[0].args[0])
	}
	h.calls = nil
	pruneLog(now())
	if len(h.calls) != 1 || !strings.HasPrefix(h.calls[0].sql, "DELETE FROM log") || h.calls[0].args[0] != "2026-10-05T10:00:00Z" {
		t.Fatalf("prune calls %+v", h.calls)
	}
}

func TestClaimDecodesRows(t *testing.T) {
	h := &fakeHost{query: `[{"id":7,"submission_id":"42","form_slug":"contact","channel":"slack","body":"{}","attempts":2,"created_at":"2026-11-04T09:00:00.5+00:00"}]`}
	h.install(t)
	jobs, err := claim(now())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v err %v", jobs, err)
	}
	if j := jobs[0]; j.ID != 7 || j.Channel != chanSlack || j.Attempts != 2 || j.CreatedAt.Hour() != 9 {
		t.Fatalf("job %+v", j)
	}
	if h.calls[0].args[1] != "2026-11-04T04:00:00Z" {
		t.Fatalf("claim ignores rows older than six hours: %v", h.calls[0].args)
	}
}

func TestClaimStaysUnderTheHostResultCap(t *testing.T) {
	var rows []string
	for i := 1; i <= batchSize; i++ {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"submission_id":"%d","form_slug":"contact","channel":"slack","body":%q,"attempts":0,"created_at":"2026-11-04T09:00:00Z"}`,
			i, i, strings.Repeat(`"`, maxBody)))
	}
	h := &fakeHost{query: "[" + strings.Join(rows, ",") + "]"}
	h.install(t)
	jobs, err := claim(now())
	if err != nil || len(jobs) != batchSize || len(jobs[0].Body) != maxBody {
		t.Fatalf("jobs %d err %v", len(jobs), err)
	}
}

func TestDBJSONReportsATruncatedResult(t *testing.T) {
	h := &fakeHost{query: `"` + strings.Repeat("x", maxDBResultBytes) + `"`}
	h.install(t)
	var v any
	if err := dbJSON(`SELECT 1`, &v); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatal("a truncated result must be an error")
	}
}
