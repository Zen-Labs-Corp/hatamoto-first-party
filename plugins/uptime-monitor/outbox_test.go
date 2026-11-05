package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// mailHost fakes the outbox table, kv and mail.send. sendErr, when set,
// answers mail.send with an error.
type mailHost struct {
	rows    []outboxRow
	kv      map[string]json.RawMessage
	sent    []sentMail
	sendErr error
	dropped int
	deleted []int64
	bumped  []int64
}

func newMailHost(t *testing.T) (*mailHost, *fakeHost) {
	t.Helper()
	h := &mailHost{kv: map[string]json.RawMessage{}}
	f := newFakeHost(t, func(c call, out any) error {
		sql := c.sql()
		switch {
		case c.Fn == "kv.set":
			h.kv[c.Args["key"].(string)], _ = json.Marshal(c.Args["value"])
		case c.Fn == "kv.get":
			v, ok := h.kv[c.Args["key"].(string)]
			if !ok {
				return json.Unmarshal([]byte(`{"found":false}`), out)
			}
			b, _ := json.Marshal(map[string]any{"found": true, "value": v})
			return json.Unmarshal(b, out)
		case c.Fn == "mail.send":
			if h.sendErr != nil {
				return h.sendErr
			}
			var to []string
			for _, a := range c.Args["to"].([]any) {
				to = append(to, a.(string))
			}
			h.sent = append(h.sent, sentMail{c.Args["subject"].(string), c.Args["text"].(string), to})
		case strings.HasPrefix(sql, "WITH d AS (DELETE FROM outbox"):
			cut, _ := time.Parse(time.RFC3339, c.params()[0].(string))
			var keep []outboxRow
			n := 0
			for _, r := range h.rows {
				if r.CreatedAt.Before(cut) {
					n++
				} else {
					keep = append(keep, r)
				}
			}
			h.rows, h.dropped = keep, h.dropped+n
			return cell(out, fmt.Sprint(n))
		case strings.Contains(sql, "FROM outbox ORDER BY id"):
			b, _ := json.Marshal(h.rows)
			return cell(out, string(b))
		case strings.HasPrefix(sql, "DELETE FROM outbox"):
			id := int64(c.params()[0].(float64))
			h.deleted = append(h.deleted, id)
			for i, r := range h.rows {
				if r.ID == id {
					h.rows = append(h.rows[:i], h.rows[i+1:]...)
					break
				}
			}
		case strings.HasPrefix(sql, "UPDATE outbox"):
			h.bumped = append(h.bumped, int64(c.params()[0].(float64)))
		}
		return nil
	})
	return h, f
}

func (h *mailHost) add(id int64, age time.Duration, recipients int) {
	var to []string
	for i := range recipients {
		to = append(to, fmt.Sprintf("a%d@example.org", i))
	}
	h.rows = append(h.rows, outboxRow{ID: id, CreatedAt: now().Add(-age), Subject: fmt.Sprintf("mail %d", id), Body: "body", Recipients: to})
}

func TestEnqueueMailNeedsRecipients(t *testing.T) {
	f := newFakeHost(t, nil)
	if err := enqueueMail("s", "b", nil); err != nil || len(f.calls) != 0 {
		t.Fatalf("no recipients must queue nothing: %v %v", err, f.calls)
	}
	if err := enqueueMail("s", "b", []string{"a@example.org"}); err != nil || len(f.sqls()) != 1 {
		t.Fatalf("%v %v", err, f.sqls())
	}
	placeholders(t, f.calls[0])
}

func TestFlushSendsOldestFirstAndDeletes(t *testing.T) {
	setNow(t, "2026-02-01T12:00:00Z")
	h, _ := newMailHost(t)
	h.add(1, time.Minute, 2)
	h.add(2, time.Second, 1)
	if err := flushOutbox(now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(h.sent) != 2 || h.sent[0].subject != "mail 1" || len(h.sent[0].to) != 2 || h.sent[1].subject != "mail 2" {
		t.Fatalf("sent %+v", h.sent)
	}
	if len(h.rows) != 0 || len(h.deleted) != 2 {
		t.Fatalf("rows %v deleted %v", h.rows, h.deleted)
	}
}

func TestFlushSlidingWindowAllowsSixRecipientsPerMinute(t *testing.T) {
	clock := setNow(t, "2026-02-01T12:00:00Z")
	h, _ := newMailHost(t)
	h.add(1, 0, 5)
	h.add(2, 0, 5)
	if err := flushOutbox(clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(h.sent) != 1 || h.sent[0].subject != "mail 1" || len(h.rows) != 1 || h.rows[0].ID != 2 {
		t.Fatalf("5 + 5 recipients: first goes, second waits: sent %+v rows %v", h.sent, h.rows)
	}
	// Still inside the minute.
	now = func() time.Time { return clock.Add(59 * time.Second) }
	_ = flushOutbox(clock.Add(time.Hour))
	if len(h.sent) != 1 {
		t.Fatalf("59 s later: sent %d", len(h.sent))
	}
	// The window slides.
	now = func() time.Time { return clock.Add(61 * time.Second) }
	_ = flushOutbox(clock.Add(time.Hour))
	if len(h.sent) != 2 || len(h.rows) != 0 {
		t.Fatalf("61 s later: sent %d rows %v", len(h.sent), h.rows)
	}
}

func TestFlushSkipsARowThatDoesNotFitAndSendsASmallerOne(t *testing.T) {
	clock := setNow(t, "2026-02-01T12:00:00Z")
	h, _ := newMailHost(t)
	h.add(1, 0, 5)
	h.add(2, 0, 5)
	h.add(3, 0, 1)
	_ = flushOutbox(clock.Add(time.Hour))
	if len(h.sent) != 2 || h.sent[1].subject != "mail 3" || len(h.rows) != 1 || h.rows[0].ID != 2 {
		t.Fatalf("sent %+v rows %v", h.sent, h.rows)
	}
}

func TestFlushRateLimitedKeepsTheRowAndStops(t *testing.T) {
	clock := setNow(t, "2026-02-01T12:00:00Z")
	h, f := newMailHost(t)
	h.sendErr = hostErr(sdk.ErrRateLimited, "mail rate limit")
	h.add(1, 0, 1)
	h.add(2, 0, 1)
	if err := flushOutbox(clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if f.count("mail.send") != 1 || len(h.rows) != 2 || len(h.bumped) != 1 || h.bumped[0] != 1 {
		t.Fatalf("sends %d rows %d bumped %v", f.count("mail.send"), len(h.rows), h.bumped)
	}
	if len(h.kv) != 0 {
		t.Fatal("a refused mail must not use up the window")
	}
	h.sendErr = nil
	_ = flushOutbox(clock.Add(time.Hour))
	if len(h.sent) != 2 || len(h.rows) != 0 {
		t.Fatalf("next tick: sent %d rows %d", len(h.sent), len(h.rows))
	}
}

func TestFlushMailNotConfiguredKeepsRows(t *testing.T) {
	clock := setNow(t, "2026-02-01T12:00:00Z")
	h, f := newMailHost(t)
	h.sendErr = hostErr(sdk.ErrUnavailable, "mail is not configured")
	h.add(1, 0, 1)
	h.add(2, 0, 1)
	_ = flushOutbox(clock.Add(time.Hour))
	if f.count("mail.send") != 1 || len(h.rows) != 2 || len(h.bumped) != 0 {
		t.Fatalf("sends %d rows %d bumped %v", f.count("mail.send"), len(h.rows), h.bumped)
	}
}

func TestFlushOtherErrorCountsAnAttemptAndContinues(t *testing.T) {
	clock := setNow(t, "2026-02-01T12:00:00Z")
	h, f := newMailHost(t)
	h.sendErr = hostErr(sdk.ErrInvalidArgument, "bad address")
	h.add(1, 0, 1)
	h.add(2, 0, 1)
	_ = flushOutbox(clock.Add(time.Hour))
	if f.count("mail.send") != 2 || len(h.bumped) != 2 || len(h.rows) != 2 {
		t.Fatalf("sends %d bumped %v", f.count("mail.send"), h.bumped)
	}
}

func TestFlushDropsMailOlderThan24Hours(t *testing.T) {
	clock := setNow(t, "2026-02-01T12:00:00Z")
	h, f := newMailHost(t)
	h.add(1, 24*time.Hour+time.Minute, 1)
	h.add(2, 23*time.Hour, 1)
	_ = flushOutbox(clock.Add(time.Hour))
	if h.dropped != 1 || len(h.sent) != 1 || h.sent[0].subject != "mail 2" {
		t.Fatalf("dropped %d sent %+v", h.dropped, h.sent)
	}
	logs := strings.Join(f.logs(), "\n")
	if !strings.Contains(logs, "dropped 1 alert mails") || strings.Contains(logs, "@example.org") {
		t.Fatalf("log must give the count only: %q", logs)
	}
}

func TestFlushStopsAtTheDeadline(t *testing.T) {
	clock := setNow(t, "2026-02-01T12:00:00Z")
	h, _ := newMailHost(t)
	h.add(1, 0, 1)
	_ = flushOutbox(clock)
	if len(h.sent) != 0 || len(h.rows) != 1 {
		t.Fatal("no mail may start after the deadline")
	}
}

func TestSendTestAlert(t *testing.T) {
	setNow(t, "2026-02-01T12:00:00Z")
	h, _ := newMailHost(t)
	to := []string{"a@example.org", "b@example.org"}
	n, reason := sendTestAlert(to)
	if n != 2 || reason != "" || len(h.sent) != 1 || h.sent[0].subject != "[Test] Uptime monitor alert test" {
		t.Fatalf("%d %q %+v", n, reason, h.sent)
	}
	if n, reason := sendTestAlert(nil); n != 0 || reason == "" {
		t.Fatalf("no recipients: %d %q", n, reason)
	}
	// 2 used; 5 more would exceed 6 in the minute: the limiter applies.
	five := []string{"a", "b", "c", "d", "e"}
	if n, reason := sendTestAlert(five); n != 0 || !strings.Contains(reason, "6 recipients per minute") || len(h.sent) != 1 {
		t.Fatalf("limiter: %d %q", n, reason)
	}
	h.sendErr = hostErr(sdk.ErrUnavailable, "no mail")
	now = func() time.Time { return time.Date(2026, 2, 1, 13, 0, 0, 0, time.UTC) }
	if n, reason := sendTestAlert(to); n != 0 || !strings.Contains(reason, "not configured") {
		t.Fatalf("unavailable: %d %q", n, reason)
	}
	h.sendErr = hostErr(sdk.ErrRateLimited, "x")
	if n, reason := sendTestAlert(to); n != 0 || !strings.Contains(reason, "limit") {
		t.Fatalf("host limit: %d %q", n, reason)
	}
}
