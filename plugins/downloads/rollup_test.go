package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.UTC) }

// rollupDB answers the oldest-month query from a list and records the rest; a
// fold removes the month, as the real statement does.
type rollupDB struct {
	months []string // months still to fold, oldest first
	execs  []hostCall
	reads  []hostCall
}

func (r *rollupDB) install(t *testing.T) {
	callHost = func(fn string, a, out any) error {
		m := a.(map[string]any)
		c := hostCall{fn: fn, sql: m["sql"].(string), args: m["args"].([]any)}
		if fn == "db.exec" {
			r.execs = append(r.execs, c)
			r.months = r.months[1:]
			return nil
		}
		r.reads = append(r.reads, c)
		cell := `{"month":null}`
		if len(r.months) > 0 {
			cell = `{"month":"` + r.months[0] + `"}`
		}
		return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
	}
}

func TestRollupNothingOldDoesNothing(t *testing.T) {
	db := &rollupDB{}
	db.install(t)
	n, err := rollupOnce(date(2026, 10, 28))
	if n != 0 || err != nil || len(db.execs) != 0 || len(db.reads) != 1 {
		t.Fatalf("n=%d err=%v execs=%d reads=%d", n, err, len(db.execs), len(db.reads))
	}
	if db.reads[0].args[0] != "2025-09-01" {
		t.Fatalf("cutoff arg %v", db.reads[0].args)
	}
	if n, _ := rollupOnce(date(2026, 10, 28)); n != 0 || len(db.execs) != 0 {
		t.Fatal("second run wrote")
	}
}

func TestRollupFoldsOldestFirstBoundedAndRerunIsEmpty(t *testing.T) {
	db := &rollupDB{months: []string{"2024-01-01", "2024-02-01", "2024-03-01", "2024-04-01"}}
	db.install(t)
	n, err := rollupOnce(date(2026, 10, 28))
	if n != 3 || err != nil || len(db.execs) != 3 {
		t.Fatalf("n=%d err=%v execs=%d", n, err, len(db.execs))
	}
	for i, want := range []string{"2024-01-01", "2024-02-01", "2024-03-01"} {
		if db.execs[i].args[0] != want || db.execs[i].sql != foldMonth || len(db.execs[i].args) != 1 {
			t.Fatalf("exec %d: %+v", i, db.execs[i])
		}
	}
	if len(db.months) != 1 {
		t.Fatalf("remaining %v", db.months)
	}
	// Last month folds next run, then a rerun finds nothing and adds nothing.
	if n, _ := rollupOnce(date(2026, 10, 29)); n != 1 {
		t.Fatalf("next run folded %d", n)
	}
	if n, _ := rollupOnce(date(2026, 10, 30)); n != 0 || len(db.execs) != 4 {
		t.Fatalf("rerun folded %d, execs %d", n, len(db.execs))
	}
}

func TestRollupStopsOnError(t *testing.T) {
	boom := errors.New("boom")
	callHost = func(fn string, _, out any) error {
		if fn == "db.exec" {
			return boom
		}
		return json.Unmarshal([]byte(`{"rows":[[{"month":"2024-01-01"}]]}`), out)
	}
	if n, err := rollupOnce(date(2026, 10, 28)); n != 0 || !errors.Is(err, boom) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCutoffMonth(t *testing.T) {
	for in, want := range map[time.Time]string{date(2026, 10, 28): "2025-09-01", date(2026, 1, 1): "2024-12-01"} {
		if got := cutoffMonth(in); got != want {
			t.Errorf("cutoffMonth(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestFoldMonthIsOneStatement(t *testing.T) {
	if !strings.HasPrefix(foldMonth, "WITH ") || strings.Contains(foldMonth, ";") {
		t.Fatal("not a single WITH statement")
	}
	for _, part := range []string{
		"DELETE FROM downloads_daily", "RETURNING media_id, count", "INSERT INTO downloads_monthly",
		"sum(count)", "ON CONFLICT (media_id, month) DO UPDATE SET count = downloads_monthly.count + EXCLUDED.count",
	} {
		if !strings.Contains(foldMonth, part) {
			t.Errorf("missing %q", part)
		}
	}
	if strings.Count(foldMonth, "DELETE") != 1 || strings.Contains(strings.ToLower(foldMonth), "now()") {
		t.Fatal("unexpected clock or second delete")
	}
}

func TestCronRegistered(t *testing.T) {
	old := now
	defer func() { now = old }()
	now = func() time.Time { return date(2026, 10, 28) }
	db := &rollupDB{months: []string{"2024-01-01"}}
	db.install(t)
	in, _ := json.Marshal(sdk.CronEvent{})
	if _, err := guest.Handle(guest.CronExport("rollup"), in); err != nil {
		t.Fatal(err)
	}
	if len(db.execs) != 1 {
		t.Fatalf("%d execs", len(db.execs))
	}
}
