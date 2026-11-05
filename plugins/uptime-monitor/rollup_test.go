package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

type checkRow struct {
	monitor int64
	at      time.Time
	ok      *bool // nil: configuration problem
	ms      int
}

type dailyRow struct {
	checks, up int
	pct        float64
	avg        int
}

// rollupHost evaluates the daily upsert over fake check rows and records the
// retention statements.
type rollupHost struct {
	t       *testing.T
	checks  []checkRow
	daily   map[string]dailyRow // "monitor|day"
	kv      map[string]json.RawMessage
	urls    []string
	deletes map[string][]any // statement prefix -> params
	dbErr   error
	f       *fakeHost
}

func newRollupHost(t *testing.T, clock string) *rollupHost {
	t.Helper()
	h := &rollupHost{t: t, daily: map[string]dailyRow{}, kv: map[string]json.RawMessage{}, deletes: map[string][]any{}}
	setNow(t, clock)
	h.f = newFakeHost(t, h.reply)
	return h
}

func (h *rollupHost) reply(c call, out any) error {
	sql := c.sql()
	oneStatement(h.t, sql)
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
	case c.Fn == "db.exec" || c.Fn == "db.query":
		if h.dbErr != nil {
			return h.dbErr
		}
		return h.db(c, sql, out)
	}
	return nil
}

func (h *rollupHost) db(c call, sql string, out any) error {
	p := c.params()
	switch {
	case strings.HasPrefix(sql, "INSERT INTO daily"):
		from, _ := time.Parse(time.RFC3339, p[1].(string))
		to, _ := time.Parse(time.RFC3339, p[2].(string))
		type agg struct{ n, up, ms, good int }
		per := map[int64]*agg{}
		for _, r := range h.checks {
			if r.ok == nil || r.at.Before(from) || !r.at.Before(to) {
				continue
			}
			a := per[r.monitor]
			if a == nil {
				a = &agg{}
				per[r.monitor] = a
			}
			a.n++
			if *r.ok {
				a.up++
				a.ms += r.ms // the average counts good checks only
				a.good++
			}
		}
		for id, a := range per {
			key := strings.Join([]string{string(rune('0' + id)), p[0].(string)}, "|")
			h.daily[key] = dailyRow{a.n, a.up, float64(a.up) * 100 / float64(a.n), (a.ms + a.good/2) / max(a.good, 1)}
		}
	case strings.HasPrefix(sql, "DELETE FROM"):
		h.deletes[strings.Join(strings.Fields(sql)[:3], " ")] = p
	case strings.Contains(sql, "FROM monitors m"):
		var ms []monitor
		for i, u := range h.urls {
			ms = append(ms, monitor{ID: int64(i + 1), URL: u})
		}
		b, _ := json.Marshal(ms)
		return cell(out, string(b))
	}
	return nil
}

func tm(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func yes() *bool { t := true; return &t }
func no() *bool  { f := false; return &f }

func TestRollupUTCDayBoundary(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:00:00Z")
	h.kv[rollupKey] = json.RawMessage(`"2026-03-08"`)
	h.checks = []checkRow{
		{1, tm("2026-03-08T23:59:59Z"), yes(), 100},
		{1, tm("2026-03-09T00:00:00Z"), no(), 300},
		{1, tm("2026-03-09T23:59:59Z"), yes(), 100},
		{1, tm("2026-03-10T00:00:00Z"), yes(), 50},
	}
	runRollup(sdk.CronEvent{})
	want := map[string]dailyRow{
		"1|2026-03-08": {1, 1, 100, 100},
		"1|2026-03-09": {2, 1, 50, 100}, // the failed 300 ms check is not averaged
		"1|2026-03-10": {1, 1, 100, 50},
	}
	if len(h.daily) != 3 {
		t.Fatalf("%v", h.daily)
	}
	for k, w := range want {
		if h.daily[k] != w {
			t.Fatalf("%s: got %+v want %+v", k, h.daily[k], w)
		}
	}
	var last string
	_ = json.Unmarshal(h.kv[rollupKey], &last)
	if last != "2026-03-10" {
		t.Fatalf("rollup_day %q", last)
	}
}

func TestRollupUsesUTCNotTheLocalZone(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:00:00Z")
	zone := time.FixedZone("far east", 13*3600)
	now = func() time.Time { return tm("2026-03-10T08:00:00Z").In(zone) } // already 11 Mar locally
	h.kv[rollupKey] = json.RawMessage(`"2026-03-10"`)
	runRollup(sdk.CronEvent{})
	var days []any
	for _, c := range h.f.calls {
		if strings.HasPrefix(c.sql(), "INSERT INTO daily") {
			days = append(days, c.params()[:3]...)
		}
	}
	want := []any{"2026-03-10", "2026-03-10T00:00:00Z", "2026-03-11T00:00:00Z"}
	if len(days) != 3 || days[0] != want[0] || days[1] != want[1] || days[2] != want[2] {
		t.Fatalf("%v", days)
	}
}

func TestRollupTwiceGivesTheSameStatementsAndRows(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:00:00Z")
	h.checks = []checkRow{{1, tm("2026-03-09T10:00:00Z"), yes(), 120}, {1, tm("2026-03-10T07:00:00Z"), no(), 80}}
	runRollup(sdk.CronEvent{})
	first := map[string]dailyRow{}
	for k, v := range h.daily {
		first[k] = v
	}
	runRollup(sdk.CronEvent{})
	if len(h.daily) != len(first) {
		t.Fatalf("rows %v vs %v", h.daily, first)
	}
	for k, v := range first {
		if h.daily[k] != v {
			t.Fatalf("%s changed: %+v -> %+v", k, v, h.daily[k])
		}
	}
	if !strings.Contains(rollupDay, "ON CONFLICT (monitor_id, day) DO UPDATE") {
		t.Fatal("not an upsert")
	}
}

func TestRollupAverageCountsGoodChecksOnly(t *testing.T) {
	if !strings.Contains(rollupDay, "avg(ms) FILTER (WHERE ok)") {
		t.Fatal("avg_ms must average good checks, as the overview does")
	}
}

func TestRollupLeavesConfigRowsOut(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:00:00Z")
	h.checks = []checkRow{
		{1, tm("2026-03-10T01:00:00Z"), yes(), 100},
		{1, tm("2026-03-10T02:00:00Z"), nil, 0},
		{1, tm("2026-03-10T03:00:00Z"), nil, 0},
		{2, tm("2026-03-10T03:00:00Z"), nil, 0},
	}
	runRollup(sdk.CronEvent{})
	if got := h.daily["1|2026-03-10"]; got.checks != 1 || got.pct != 100 {
		t.Fatalf("%+v", got)
	}
	if _, ok := h.daily["2|2026-03-10"]; ok {
		t.Fatal("a monitor with only config rows got a daily row")
	}
	if !strings.Contains(rollupDay, "ok IS NOT NULL") {
		t.Fatal("config rows not excluded in SQL")
	}
}

func TestRollupFirstRunDoesYesterdayAndToday(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:00:00Z")
	h.checks = []checkRow{{1, tm("2026-03-08T10:00:00Z"), yes(), 1}, {1, tm("2026-03-09T10:00:00Z"), yes(), 1}}
	runRollup(sdk.CronEvent{})
	if len(h.daily) != 1 {
		t.Fatalf("%v", h.daily)
	}
	if _, ok := h.daily["1|2026-03-09"]; !ok {
		t.Fatalf("%v", h.daily)
	}
}

func TestRollupCatchUpIsBounded(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:00:00Z")
	h.kv[rollupKey] = json.RawMessage(`"2025-01-01"`)
	f := h.f
	runRollup(sdk.CronEvent{})
	n := 0
	for _, s := range f.sqls() {
		if strings.HasPrefix(s, "INSERT INTO daily") {
			n++
		}
	}
	if n != maxRollupDays+1 {
		t.Fatalf("%d day statements", n)
	}
}

func TestRetentionCutoffsFollowTheClock(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:30:00Z")
	runRollup(sdk.CronEvent{})
	if got := h.deletes["DELETE FROM checks"]; len(got) != 1 || got[0] != "2026-02-08T08:30:00Z" {
		t.Fatalf("checks cutoff %v", got)
	}
	if got := h.deletes["DELETE FROM incidents"]; len(got) != 1 || got[0] != "2025-02-10T08:30:00Z" {
		t.Fatalf("incidents cutoff %v", got)
	}
	if got := h.deletes["DELETE FROM daily"]; len(got) != 1 || got[0] != "2025-02-10" {
		t.Fatalf("daily cutoff %v", got)
	}
}

func TestRetentionKeepsOpenIncidentsAndDeletesOrphanCerts(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:30:00Z")
	h.urls = []string{"https://Example.org/a", "http://plain.example.net/", "https://example.org:8443/"}
	f := h.f
	runRollup(sdk.CronEvent{})
	var inc, certs string
	for _, s := range f.sqls() {
		if strings.HasPrefix(s, "DELETE FROM incidents") {
			inc = s
		}
		if strings.HasPrefix(s, "DELETE FROM certs") {
			certs = s
		}
	}
	if !strings.Contains(inc, "ended_at IS NOT NULL") || certs == "" {
		t.Fatalf("incidents %q certs %q", inc, certs)
	}
	got := h.deletes["DELETE FROM certs"]
	if len(got) != 1 || got[0] != `["example.org","plain.example.net","example.org"]` {
		t.Fatalf("hosts %v", got)
	}
}

func TestRollupWithoutDBDoesNothingAndDoesNotPanic(t *testing.T) {
	h := newRollupHost(t, "2026-03-10T08:30:00Z")
	h.dbErr = hostErr(sdk.ErrCapabilityDenied, "db not granted")
	f := h.f
	if err := runRollup(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	if f.count("kv.set") != 0 || len(h.deletes) != 0 || len(h.daily) != 0 {
		t.Fatalf("did work without db: %v", f.calls)
	}
	if len(f.logs()) == 0 {
		t.Fatal("not logged")
	}
}
