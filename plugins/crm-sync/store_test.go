package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func testJob() job {
	return job{SubmissionID: "s1", FormSlug: "contact", Provider: "hubspot", Email: "ann@example.com", Attempts: 0}
}

func (h *fakeHost) execs(prefix string) []hostCallRec {
	var out []hostCallRec
	for _, c := range h.calls {
		if c.fn == "db.exec" && strings.HasPrefix(c.sql, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func TestBackoffSchedule(t *testing.T) {
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 2 * time.Hour, 2 * time.Hour, 2 * time.Hour, 2 * time.Hour}
	for i, w := range want {
		if got := backoff(i); got != w {
			t.Errorf("backoff(%d) = %v, want %v", i, got, w)
		}
	}
	if backoff(-1) != 2*time.Hour {
		t.Errorf("backoff(-1) = %v", backoff(-1))
	}
}

func TestFailedStatusDeletesRow(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	settle(testJob(), result{kind: kindFail, status: 400, reason: "rejected"})
	if len(h.execs("DELETE FROM queue")) != 1 || len(h.execs("UPDATE")) != 0 {
		t.Fatalf("calls = %+v", h.calls)
	}
	rows := h.logRows()
	if len(rows) != 1 || rows[0] != [2]string{"failed", "rejected"} {
		t.Fatalf("log = %v", rows)
	}
	if got := h.execs("INSERT INTO log")[0].args[7]; got != 400 {
		t.Errorf("code = %v", got)
	}
}

func TestRetryableStatusKeepsRow(t *testing.T) {
	cases := []result{
		{kind: kindRetry, status: 503},
		{kind: kindRetry, status: 429},
		{kind: kindRetry, status: 408},
		{kind: kindRetry, reason: "network"},
	}
	for _, r := range cases {
		t.Run(fmt.Sprint(r.status, r.reason), func(t *testing.T) {
			h := &fakeHost{}
			h.install(t)
			j := testJob()
			j.Attempts = 2
			settle(j, r)
			if len(h.execs("DELETE")) != 0 {
				t.Fatal("row deleted")
			}
			up := h.execs("UPDATE queue SET attempts = attempts + 1")
			if len(up) != 1 {
				t.Fatalf("calls = %+v", h.calls)
			}
			if want := ts(now().Add(20 * time.Minute)); up[0].args[1] != want {
				t.Errorf("next = %v, want %v", up[0].args[1], want)
			}
			rows := h.logRows()
			if len(rows) != 1 || rows[0][0] != "retrying" || !strings.HasPrefix(rows[0][1], "attempt 3") {
				t.Fatalf("log = %v", rows)
			}
			if r.reason != "" && !strings.HasSuffix(rows[0][1], ", "+r.reason) {
				t.Errorf("message = %q", rows[0][1])
			}
		})
	}
}

func TestBudgetCutReleasesLease(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	settle(testJob(), result{kind: kindBudget})
	if len(h.logRows()) != 0 || len(h.execs("DELETE")) != 0 {
		t.Fatalf("calls = %+v", h.calls)
	}
	up := h.execs("UPDATE queue SET lease_until")
	if len(up) != 1 || up[0].args[1] != ts(now()) || strings.Contains(up[0].sql, "attempts") {
		t.Fatalf("calls = %+v", h.calls)
	}
}

func TestCreatedRecordsRecordIDAndURL(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	settle(testJob(), result{kind: kindCreated, recordID: "42", recordURL: "https://app.example.com/42", message: "rejected fields: a, b"})
	if len(h.execs("DELETE FROM queue")) != 1 {
		t.Fatal("row not deleted")
	}
	a := h.execs("INSERT INTO log")[0].args
	if a[1] != "s1" || a[2] != "contact" || a[3] != "hubspot" || a[4] != "created" || a[5] != "42" || a[6] != "https://app.example.com/42" || a[7] != nil || a[8] != "rejected fields: a, b" {
		t.Fatalf("args = %v", a)
	}
}

func TestSettleNoteFailureMessage(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	settle(testJob(), result{kind: kindUpdated, recordID: "7", message: "note failed 503"})
	rows := h.logRows()
	if len(rows) != 1 || rows[0] != [2]string{"updated", "note failed 503"} {
		t.Fatalf("log = %v", rows)
	}
	if len(h.execs("DELETE FROM queue")) != 1 || len(h.execs("UPDATE")) != 0 {
		t.Fatalf("calls = %+v", h.calls)
	}
}

func TestEnqueueDuplicateReportsFalse(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	j := testJob()
	j.Fields = map[string]string{"name": "Ann"}
	j.SubmittedAt = now()
	fresh, err := enqueue(j)
	if err != nil || !fresh {
		t.Fatalf("fresh = %v, err = %v", fresh, err)
	}
	ins := h.execs("INSERT INTO queue")[0]
	if s, ok := ins.args[4].(string); !ok || s != `{"name":"Ann"}` {
		t.Errorf("fields arg = %#v", ins.args[4])
	}
	h.dup = true
	fresh, err = enqueue(j)
	if err != nil || fresh {
		t.Fatalf("duplicate: fresh = %v, err = %v", fresh, err)
	}
}

func TestLogRowsHoldNoEmail(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	j := testJob()
	record(j, "retrying", 503, "attempt 1")
	settle(j, result{kind: kindFail, status: 400, reason: "rejected"})
	settle(j, result{kind: kindRetry, reason: "network"})
	settle(j, result{kind: kindCreated})
	for _, c := range h.execs("INSERT INTO log") {
		for _, a := range c.args {
			if s, ok := a.(string); ok && strings.Contains(s, "example.com") {
				t.Errorf("log arg %q holds the email", s)
			}
		}
	}
	for _, l := range h.hostLogs() {
		if strings.Contains(l, "example.com") {
			t.Errorf("host log %q holds the email", l)
		}
	}
}
