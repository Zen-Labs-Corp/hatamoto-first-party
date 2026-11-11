package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// mailDB answers the queue select with n pending rows and records updates.
func mailDB(n int) *fakeHost {
	return &fakeHost{db: func(c hostCall, out any) error {
		switch {
		case c.fn == "db.query":
			var rows []queuedMail
			for i := 1; i <= n; i++ {
				rows = append(rows, queuedMail{ID: int64(i), To: "dev@example.org", Subject: "s", Body: "b"})
			}
			if lim, ok := c.args[1].(int); ok && lim < len(rows) {
				rows = rows[:lim]
			}
			b, _ := json.Marshal(rows)
			return cell(out, string(b))
		default:
			return affected(out, 1)
		}
	}}
}

func countSQL(f *fakeHost, sub string) int {
	n := 0
	for _, c := range f.sqlCalls() {
		if strings.Contains(c.sql, sub) {
			n++
		}
	}
	return n
}

func TestQueueThenDrainSends(t *testing.T) {
	f := mailDB(1)
	f.install(t)
	x := &hostX{}
	x.install(t)
	queueMail("dev@example.org", "Hello\nBcc: x@example.org", "body")
	if got := f.sqlCalls()[0]; !strings.Contains(got.sql, "INSERT INTO mail_queue") || got.args[1] != "Hello Bcc: x@example.org" {
		t.Fatalf("queue call %+v", got)
	}
	if n := drainMail(3); n != 1 || len(x.mailTo) != 1 || x.mailTo[0] != "dev@example.org" {
		t.Fatalf("sent %d to %v", n, x.mailTo)
	}
	if countSQL(f, "SET sent_at") != 1 {
		t.Error("row not marked sent")
	}
	requireGuard(t, f)
}

func TestQueueMailSkipsBadAddresses(t *testing.T) {
	f := &fakeHost{}
	f.install(t)
	for _, a := range []string{"", "nobody", "a@b\nBcc: c@d", "a b@example.org"} {
		queueMail(a, "s", "b")
	}
	if len(f.sqlCalls()) != 0 {
		t.Fatalf("queued %d", len(f.sqlCalls()))
	}
}

func TestRateLimitedStopsAndKeepsTheRow(t *testing.T) {
	f := mailDB(3)
	f.install(t)
	x := &hostX{mailErr: hostErr(sdk.ErrRateLimited)}
	x.install(t)
	if n := drainMail(5); n != 0 {
		t.Fatalf("sent %d", n)
	}
	if countSQL(f, "UPDATE mail_queue") != 0 {
		t.Error("rate limited mail must stay queued untouched")
	}
}

func TestOtherMailErrorCountsAnAttempt(t *testing.T) {
	f := mailDB(2)
	f.install(t)
	x := &hostX{mailErr: errPlain}
	x.install(t)
	drainMail(5)
	if countSQL(f, "attempts = attempts + 1") != 2 {
		t.Error("each failed row counts one attempt")
	}
}

func TestNotifyIgnoresMailFailure(t *testing.T) {
	f := mailDB(1)
	f.install(t)
	x := &hostX{mailErr: hostErr(sdk.ErrUnavailable)}
	x.install(t)
	notify("dev@example.org", "s", "b")
	if countSQL(f, "INSERT INTO mail_queue") != 1 {
		t.Error("notify must queue first")
	}
}

func TestMailCronDrainsAtMostFive(t *testing.T) {
	f := mailDB(9)
	f.install(t)
	x := &hostX{}
	x.install(t)
	if err := mailCron(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	if len(x.mailTo) != 5 {
		t.Fatalf("sent %d", len(x.mailTo))
	}
	requireGuard(t, f)
}

func TestMailCronSurvivesMailErrorsButNotAnUnreadableQueue(t *testing.T) {
	f := mailDB(1)
	f.install(t)
	(&hostX{mailErr: hostErr(sdk.ErrRateLimited)}).install(t)
	if err := mailCron(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	(&fakeHost{db: func(hostCall, any) error { return errPlain }}).install(t)
	if err := mailCron(sdk.CronEvent{}); err == nil {
		t.Fatal("unreadable queue must error")
	}
}
