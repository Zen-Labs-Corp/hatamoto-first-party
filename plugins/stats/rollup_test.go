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

// rollupDB answers the oldest-month query from a list and records the rest.
type rollupDB struct {
	months []string // months still to fold, oldest first
	execs  []hostCall
	reads  []hostCall
}

func (r *rollupDB) install(t *testing.T) {
	callHost = func(fn string, a, out any) error {
		m := a.(map[string]any)
		c := hostCall{fn, m["sql"].(string), m["args"].([]any)}
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

func TestRollupStopsWhenNothingOld(t *testing.T) {
	db := &rollupDB{}
	db.install(t)
	n, err := rollupOnce(date(2026, 10, 28))
	if n != 0 || err != nil || len(db.execs) != 0 || len(db.reads) != 1 {
		t.Fatalf("n=%d err=%v execs=%d reads=%d", n, err, len(db.execs), len(db.reads))
	}
	if db.reads[0].args[0] != "2025-09-01" {
		t.Fatalf("cutoff arg %v", db.reads[0].args)
	}
	// Re-running after "no rows" does no writes either.
	if n, _ := rollupOnce(date(2026, 10, 28)); n != 0 || len(db.execs) != 0 {
		t.Fatal("second run wrote")
	}
}

func TestRollupFoldsOldestFirstAndIsBounded(t *testing.T) {
	db := &rollupDB{months: []string{"2024-01-01", "2024-02-01", "2024-03-01", "2024-04-01"}}
	db.install(t)
	n, err := rollupOnce(date(2026, 10, 28))
	if n != 3 || err != nil || len(db.execs) != 3 {
		t.Fatalf("n=%d err=%v execs=%d", n, err, len(db.execs))
	}
	for i, want := range []string{"2024-01-01", "2024-02-01", "2024-03-01"} {
		if db.execs[i].args[0] != want || db.execs[i].sql != foldMonth {
			t.Fatalf("exec %d: %+v", i, db.execs[i])
		}
	}
	if len(db.months) != 1 {
		t.Fatalf("remaining %v", db.months)
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

func TestFoldMonthStatement(t *testing.T) {
	if !sqlGuardOK(foldMonth) || !strings.HasPrefix(foldMonth, "WITH ") {
		t.Fatal("not a single guarded WITH statement")
	}
	for _, part := range []string{
		"DELETE FROM installs_daily", "RETURNING", "ON CONFLICT (install, month) DO UPDATE",
		"DISTINCT ON (install)", "ORDER BY install, day DESC",
	} {
		if !strings.Contains(foldMonth, part) {
			t.Errorf("missing %q", part)
		}
	}
	if strings.Count(strings.ToUpper(foldMonth), "DELETE") != 1 || strings.Contains(strings.ToLower(foldMonth), "now()") {
		t.Fatal("unexpected clock or second delete")
	}
}

func TestCronRegisteredAndReturnsError(t *testing.T) {
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
