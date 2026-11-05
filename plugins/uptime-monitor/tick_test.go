package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const heartbeatSecret = "https://hb.example.net/ping/s3cr3t-token"

// tickHost fakes the monitors table, kv, the web and mail for the check tick.
// A fetch advances the clock by the time the target takes: its table entry, or
// the requested timeout for a target listed in hang.
type tickHost struct {
	t        *testing.T
	clock    time.Time
	monitors map[int64]*monitor
	kv       map[string]json.RawMessage
	st       settings
	takes    time.Duration  // default time one fetch takes
	hang     bool           // every monitor fetch takes its whole timeout
	status   map[string]int // URL -> HTTP status (default 200)
	fetched  []fetchRecord  // every http.fetch, in order
	checks   []int64        // monitor ids with a recorded check
	hbErr    error          // heartbeat answer
	dbErr    error          // db.query / db.exec answer
}

type fetchRecord struct {
	url       string
	timeoutMS int
	startedAt time.Time
}

func newTickHost(t *testing.T, n int) *tickHost {
	t.Helper()
	h := &tickHost{
		t: t, monitors: map[int64]*monitor{}, kv: map[string]json.RawMessage{}, status: map[string]int{},
		st:    settings{FailThreshold: 2, SSLWarnDays: []int{21, 7, 1}, HeartbeatURL: heartbeatSecret},
		takes: 100 * time.Millisecond,
	}
	h.clock = setNow(t, "2026-02-01T12:00:00Z")
	for i := 1; i <= n; i++ {
		h.monitors[int64(i)] = &monitor{ID: int64(i), Name: fmt.Sprintf("M%d", i), URL: fmt.Sprintf("http://m%d.example.org/", i),
			IntervalS: 300, NextCheck: h.clock.Add(-time.Hour), Status: statusPending}
	}
	newFakeHost(t, h.reply)
	now = func() time.Time { return h.clock }
	return h
}

func (h *tickHost) reply(c call, out any) error {
	sql := c.sql()
	switch {
	case c.Fn == "settings.get":
		b, _ := json.Marshal(h.st)
		return json.Unmarshal(b, out)
	case c.Fn == "kv.set":
		h.kv[c.Args["key"].(string)], _ = json.Marshal(c.Args["value"])
	case c.Fn == "kv.get":
		v, ok := h.kv[c.Args["key"].(string)]
		if !ok {
			return json.Unmarshal([]byte(`{"found":false}`), out)
		}
		b, _ := json.Marshal(map[string]any{"found": true, "value": v})
		return json.Unmarshal(b, out)
	case c.Fn == "http.fetch":
		return h.fetch(c, out)
	case c.Fn == "db.query" || c.Fn == "db.exec":
		if h.dbErr != nil {
			return h.dbErr
		}
		return h.db(c, sql, out)
	}
	return nil
}

func (h *tickHost) fetch(c call, out any) error {
	url := c.Args["url"].(string)
	timeout := int(c.Args["timeout_ms"].(float64))
	h.fetched = append(h.fetched, fetchRecord{url, timeout, h.clock})
	if url == heartbeatSecret {
		h.clock = h.clock.Add(50 * time.Millisecond)
		return h.hbErr
	}
	if h.hang {
		h.clock = h.clock.Add(time.Duration(timeout) * time.Millisecond)
		return hostErr(sdk.ErrTimeout, "call timed out")
	}
	h.clock = h.clock.Add(h.takes)
	status := 200
	if s, ok := h.status[url]; ok {
		status = s
	}
	return json.Unmarshal([]byte(fmt.Sprintf(`{"status":%d}`, status)), out)
}

func (h *tickHost) db(c call, sql string, out any) error {
	switch {
	case strings.Contains(sql, "FROM monitors m WHERE m.next_check"):
		cut, _ := time.Parse(time.RFC3339, c.params()[0].(string))
		var due []monitor
		for id := int64(1); id <= int64(len(h.monitors)); id++ {
			if m := h.monitors[id]; !m.NextCheck.After(cut) {
				due = append(due, *m)
			}
		}
		b, _ := json.Marshal(due)
		return cell(out, string(b))
	case strings.HasPrefix(sql, "UPDATE monitors SET status"):
		p := c.params()
		m := h.monitors[int64(p[0].(float64))]
		m.Status = p[1].(string)
		m.NextCheck, _ = time.Parse(time.RFC3339, p[3].(string))
		m.FailCount = int(p[7].(float64))
		m.AlertedDown = p[9].(bool)
	case strings.HasPrefix(sql, "INSERT INTO checks"):
		h.checks = append(h.checks, int64(c.params()[0].(float64)))
	case strings.Contains(sql, "FROM outbox ORDER BY id"), strings.Contains(sql, "FROM incidents"):
		return cell(out, `[]`)
	case strings.HasPrefix(sql, "WITH d AS (DELETE FROM outbox"):
		return cell(out, `0`)
	}
	return nil
}

func (h *tickHost) heartbeats() int {
	n := 0
	for _, f := range h.fetched {
		if f.url == heartbeatSecret {
			n++
		}
	}
	return n
}

func (h *tickHost) cursor() int64 {
	var id int64
	_ = json.Unmarshal(h.kv[cursorKey], &id)
	return id
}

func TestTickChecksEveryDueMonitorAndSchedulesTheNext(t *testing.T) {
	h := newTickHost(t, 3)
	start := h.clock
	checkTick()
	if len(h.checks) != 3 || h.cursor() != 3 {
		t.Fatalf("checks %v cursor %d", h.checks, h.cursor())
	}
	for _, m := range h.monitors {
		if m.Status != statusUp || !m.NextCheck.After(start.Add(299*time.Second)) || m.NextCheck.After(h.clock.Add(300*time.Second)) {
			t.Fatalf("%+v", m)
		}
	}
	if h.heartbeats() != 0 {
		t.Fatal("no monitor is the site")
	}
}

func TestTickNeverStartsARequestItCannotFinish(t *testing.T) {
	h := newTickHost(t, 20)
	h.hang = true // every request runs into its whole timeout
	start := h.clock
	checkTick()
	if elapsed := h.clock.Sub(start); elapsed >= 10*time.Second || elapsed > tickBudget {
		t.Fatalf("tick took %v", elapsed)
	}
	if len(h.fetched) == 0 || len(h.fetched) >= 20 {
		t.Fatalf("%d requests", len(h.fetched))
	}
	for _, f := range h.fetched {
		left := start.Add(tickBudget).Sub(f.startedAt)
		if time.Duration(f.timeoutMS)*time.Millisecond > left-tickMargin || f.timeoutMS < minRequestMS || f.timeoutMS > maxRequestMS {
			t.Fatalf("request with timeout %d ms started with %v left", f.timeoutMS, left)
		}
	}
}

func TestTickStopsWhenLessThanTheMinimumTimeoutRemains(t *testing.T) {
	h := newTickHost(t, 20)
	h.takes = 1500 * time.Millisecond
	start := h.clock
	checkTick()
	// Starts at 0, 1.5, 3.0 and 4.5 s leave 3500, 2000, ... ms: the fifth would
	// get 1500 ms (8 - 6 - 0.5) and is not started.
	if len(h.checks) != 4 {
		t.Fatalf("checked %v after %v", h.checks, h.clock.Sub(start))
	}
	if h.fetched[0].timeoutMS != 7500 {
		t.Fatalf("first timeout %d, want budget minus margin", h.fetched[0].timeoutMS)
	}
}

func TestTickResumesAfterTheCursor(t *testing.T) {
	h := newTickHost(t, 20)
	h.takes = 1500 * time.Millisecond
	checkTick()
	first := append([]int64(nil), h.checks...)
	if fmt.Sprint(first) != "[1 2 3 4]" || h.cursor() != 4 {
		t.Fatalf("first tick %v cursor %d", first, h.cursor())
	}
	h.checks = nil
	h.clock = h.clock.Add(time.Minute)
	checkTick()
	if fmt.Sprint(h.checks) != "[5 6 7 8]" || h.cursor() != 8 {
		t.Fatalf("second tick %v cursor %d", h.checks, h.cursor())
	}
}

func TestTickRoundRobinWhenAllAreDueTogether(t *testing.T) {
	h := newTickHost(t, 5)
	same := h.clock.Add(-time.Minute)
	for _, m := range h.monitors {
		m.NextCheck = same
	}
	h.kv[cursorKey] = json.RawMessage(`3`)
	checkTick()
	if fmt.Sprint(h.checks) != "[4 5 1 2 3]" {
		t.Fatalf("order %v", h.checks)
	}
	// A more overdue monitor still goes first.
	h2 := newTickHost(t, 3)
	h2.monitors[2].NextCheck = h2.clock.Add(-3 * time.Hour)
	checkTick()
	if fmt.Sprint(h2.checks) != "[2 1 3]" {
		t.Fatalf("overdue first: %v", h2.checks)
	}
}

func TestTickHonoursEachInterval(t *testing.T) {
	h := newTickHost(t, 2)
	h.monitors[1].IntervalS = 60
	h.monitors[2].IntervalS = 300
	checkTick()
	h.checks = nil
	h.clock = h.clock.Add(time.Minute)
	checkTick()
	if fmt.Sprint(h.checks) != "[1]" {
		t.Fatalf("after 1 min: %v", h.checks)
	}
	h.checks = nil
	h.clock = h.clock.Add(4 * time.Minute)
	checkTick()
	if len(h.checks) != 2 {
		t.Fatalf("after 5 min: %v", h.checks)
	}
}

func TestTickRateLimitedCheckIsRetriedNextTick(t *testing.T) {
	h := newTickHost(t, 1)
	before := h.monitors[1].NextCheck
	newFakeHost(t, func(c call, out any) error {
		if c.Fn == "http.fetch" {
			return hostErr(sdk.ErrRateLimited, "rate limited")
		}
		return h.reply(c, out)
	})
	checkTick()
	if len(h.checks) != 0 || !h.monitors[1].NextCheck.Equal(before) {
		t.Fatalf("checks %v next %v", h.checks, h.monitors[1].NextCheck)
	}
}

func TestTickWithoutDatabaseDoesNothing(t *testing.T) {
	h := newTickHost(t, 2)
	h.dbErr = hostErr(sdk.ErrCapabilityDenied, "db not granted")
	if err := runCheckTick(sdk.CronEvent{}); err != nil {
		t.Fatalf("a cron job must not fail: %v", err)
	}
	if len(h.fetched) != 0 {
		t.Fatalf("requests without a database: %v", h.fetched)
	}
}

func TestTickSurvivesBrokenSettingsAndKV(t *testing.T) {
	h := newTickHost(t, 2)
	newFakeHost(t, func(c call, out any) error {
		if c.Fn == "settings.get" || strings.HasPrefix(c.Fn, "kv.") {
			return hostErr(sdk.ErrUnavailable, "down")
		}
		return h.reply(c, out)
	})
	checkTick()
	if len(h.checks) != 2 {
		t.Fatalf("checks %v", h.checks)
	}
}

func TestTickDownMonitorQueuesOneMailAtTheThreshold(t *testing.T) {
	h := newTickHost(t, 1)
	h.st.AlertEmails = []string{"owner@example.org"}
	h.status["http://m1.example.org/"] = 503
	var queued int
	newFakeHost(t, func(c call, out any) error {
		if strings.HasPrefix(c.sql(), "INSERT INTO outbox") {
			queued++
		}
		return h.reply(c, out)
	})
	checkTick()
	h.clock = h.clock.Add(6 * time.Minute)
	checkTick()
	h.clock = h.clock.Add(6 * time.Minute)
	checkTick()
	if queued != 1 || !h.monitors[1].AlertedDown || h.monitors[1].Status != statusDown {
		t.Fatalf("queued %d %+v", queued, h.monitors[1])
	}
}
