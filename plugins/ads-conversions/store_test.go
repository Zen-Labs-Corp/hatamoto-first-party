package main

import (
	"strings"
	"testing"
	"time"
)

func argsOf(c call) []any { return c.raw.(map[string]any)["args"].([]any) }
func sqlOf(c call) string { return c.raw.(map[string]any)["sql"].(string) }

func TestBackoff(t *testing.T) {
	want := map[int]time.Duration{0: time.Minute, 1: 2 * time.Minute, 2: 4 * time.Minute, 5: 32 * time.Minute, 6: time.Hour, 7: time.Hour, 40: time.Hour, -1: time.Hour}
	for n, d := range want {
		if got := backoff(n); got != d {
			t.Errorf("backoff(%d) = %s, want %s", n, got, d)
		}
	}
}

func TestCutAndRecordMessage(t *testing.T) {
	if got := cut(strings.Repeat("é", 400), 300); len([]rune(got)) != 300 {
		t.Fatalf("cut = %d runes", len([]rune(got)))
	}
	h := newHost(t)
	record("s", "contact", "tiktok", "failed", "http", "400", strings.Repeat("x", 500))
	msg := argsOf(h.execs("INSERT INTO log")[0])[7].(string)
	if len(msg) != maxMessage {
		t.Fatalf("message length = %d", len(msg))
	}
}

func TestEnqueueArgsAreHashesAndIDsOnly(t *testing.T) {
	h := newHost(t)
	fresh, err := enqueue(job{SubmissionID: "s1", Form: "contact", Platform: platTikTok, EmHash: "h1", PhHash: "h3",
		IDs: clickIDs{"ttclid": "T1"}, PagePath: "/x", Consent: true})
	if err != nil || !fresh {
		t.Fatalf("fresh %v err %v", fresh, err)
	}
	a := argsOf(h.execs("INSERT INTO queue")[0])
	if a[3] != "h1" || a[4] != nil || a[5] != "h3" || a[6] != nil || a[9] != "T1" || a[10] != nil {
		t.Fatalf("args = %v", a)
	}
	if a[13] != ts(testNow) || a[14] != ts(testNow) {
		t.Fatalf("zero SubmittedAt must fall back to now: %v", a[13:])
	}
}

func TestClaimFirstAndRetry(t *testing.T) {
	h := newHost(t)
	h.query["FOR UPDATE SKIP LOCKED"] = `[{"id":7,"submission_id":"s1","form_slug":"contact","platform":"google","em_hash":"h1","gclid":"G1",
	"ttclid":null,"page_path":"/x","consent":true,"submitted_at":"2026-11-05T09:59:00Z","attempts":0,"created_at":"2026-11-05T09:59:00Z","ids_joined":false}]`
	jobs, err := claimFirst(testNow, 50)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v err %v", jobs, err)
	}
	j := jobs[0]
	if j.ID != 7 || j.Platform != "google" || j.EmHash != "h1" || j.IDs["gclid"] != "G1" || len(j.IDs) != 1 || !j.Consent {
		t.Fatalf("job = %+v", j)
	}
	q := h.callsOf("db.query")[0]
	if !strings.Contains(sqlOf(q), "q.attempts = 0") || !strings.Contains(sqlOf(q), "interval '60 seconds'") || !strings.Contains(sqlOf(q), "clicks") {
		t.Fatalf("first sql = %s", sqlOf(q))
	}
	if a := argsOf(q); a[0] != ts(testNow) || a[1] != float64(50) && a[1] != 50 || a[2] != ts(testNow.Add(leaseTime)) {
		t.Fatalf("args = %v", a)
	}
	if _, err := claimRetry(testNow, 10); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlOf(h.callsOf("db.query")[1]), "q.attempts > 0") {
		t.Fatal("retry sql")
	}
}

func TestJoinClicksMergesOverSourcePathAndWritesAllRows(t *testing.T) {
	h := newHost(t)
	h.query["FROM clicks WHERE submission_id"] = `{"gclid":"POSTED","bad":"x","wbraid":"bad value"}`
	j := job{SubmissionID: "s1", IDs: clickIDs{"gclid": "FROMURL", "ttclid": "T1"}}
	if err := joinClicks(&j); err != nil {
		t.Fatal(err)
	}
	if j.IDs["gclid"] != "POSTED" || j.IDs["ttclid"] != "T1" || len(j.IDs) != 2 || !j.IDsJoined {
		t.Fatalf("job = %+v", j)
	}
	u := h.execs("UPDATE queue SET gclid")[0]
	if !strings.Contains(sqlOf(u), "WHERE submission_id = $1") || strings.Contains(sqlOf(u), "platform") {
		t.Fatalf("must update every row of the submission: %s", sqlOf(u))
	}
	if a := argsOf(u); a[1] != "POSTED" || a[2] != nil || a[4] != "T1" {
		t.Fatalf("args = %v", a)
	}
	// A second call is a no-op.
	n := len(h.calls)
	if err := joinClicks(&j); err != nil || len(h.calls) != n {
		t.Fatal("joinClicks must run once per row")
	}
}

func TestSettleSentAndFailedDeleteAndLog(t *testing.T) {
	h := newHost(t)
	j := job{ID: 3, SubmissionID: "s1", Form: "contact", Platform: platGoogle}
	settleSent(j, "200", "")
	settleFailed(j, "http", "400", "bad request")
	if len(h.execs("DELETE FROM queue WHERE id = $1")) != 2 {
		t.Fatal("rows must be deleted")
	}
	logs := h.execs("INSERT INTO log")
	if len(logs) != 2 || argsOf(logs[0])[4] != "sent" || argsOf(logs[1])[4] != "failed" || argsOf(logs[1])[5] != "http" || argsOf(logs[1])[6] != "400" {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestSettleRetryBacksOffAndLogsOnlyTheFirst(t *testing.T) {
	h := newHost(t)
	settleRetry(job{ID: 3, SubmissionID: "s1", Platform: platTikTok, Attempts: 0}, "503", "")
	if len(h.execs("INSERT INTO log")) != 1 {
		t.Fatal("first retry must be logged")
	}
	u := h.execs("UPDATE queue SET attempts = attempts + 1")[0]
	if argsOf(u)[1] != ts(testNow.Add(time.Minute)) {
		t.Fatalf("next attempt = %v", argsOf(u)[1])
	}
	h.calls = nil
	settleRetry(job{ID: 3, SubmissionID: "s1", Platform: platTikTok, Attempts: 3}, "503", "")
	if len(h.execs("INSERT INTO log")) != 0 {
		t.Fatal("later retries must not be logged")
	}
	if got := argsOf(h.execs("UPDATE queue SET attempts")[0])[1]; got != ts(testNow.Add(8*time.Minute)) {
		t.Fatalf("next attempt = %v", got)
	}
}

func TestExpireOldAndPurgeUseTheClock(t *testing.T) {
	h := newHost(t)
	expireOld(testNow)
	e := h.execs("WITH d AS (DELETE FROM queue")[0]
	if argsOf(e)[0] != ts(testNow.Add(-24*time.Hour)) || !strings.Contains(sqlOf(e), "'failed', 'expired'") {
		t.Fatalf("expire = %s %v", sqlOf(e), argsOf(e))
	}
	purge(testNow)
	if argsOf(h.execs("DELETE FROM log")[0])[0] != ts(testNow.Add(-30*24*time.Hour)) {
		t.Fatal("log retention")
	}
	if argsOf(h.execs("DELETE FROM clicks")[0])[0] != ts(testNow.Add(-time.Hour)) {
		t.Fatal("clicks retention")
	}
}

func TestStoreSQLNeverTakesRawContactArgs(t *testing.T) {
	h := newHost(t)
	h.configure()
	_ = run(t, event("contact", contact()))
	for _, c := range h.callsOf("db.exec") {
		for _, bad := range []string{"@example.com", "491512345678", "Jane"} {
			if strings.Contains(c.args, bad) {
				t.Fatalf("SQL args carry %q: %s", bad, c.args)
			}
		}
	}
}

func TestScrubRemovesHashesAndClickIDs(t *testing.T) {
	h64 := sha("jane@example.com")
	got := scrub("bad value "+h64+" and CLICK-9 seen, ttclid TT_1", clickIDs{"gclid": "CLICK-9", "ttclid": "TT_1"})
	if got != "bad value … and … seen, ttclid …" {
		t.Fatalf("scrub = %q", got)
	}
}
