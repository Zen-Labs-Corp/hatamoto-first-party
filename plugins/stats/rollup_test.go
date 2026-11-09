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
			if c.sql == foldPlugins {
				r.months = r.months[1:]
			}
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
	if n != 3 || err != nil || len(db.execs) != 9 {
		t.Fatalf("n=%d err=%v execs=%d", n, err, len(db.execs))
	}
	for i, want := range []string{"2024-01-01", "2024-02-01", "2024-03-01"} {
		for j, step := range foldSteps {
			if e := db.execs[i*3+j]; e.args[0] != want || e.sql != step {
				t.Fatalf("exec %d/%d: %+v", i, j, e)
			}
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

func TestFoldStatements(t *testing.T) {
	want := map[string][]string{
		foldInstalls: {"DELETE FROM installs_daily", "INSERT INTO installs_monthly", "ON CONFLICT (install, month) DO UPDATE", "DISTINCT ON (install)", "ORDER BY install, day DESC"},
		foldDetail:   {"DELETE FROM detail_daily", "INSERT INTO detail_monthly", "ON CONFLICT (install, month) DO UPDATE", "DISTINCT ON (install)", "ORDER BY install, day DESC"},
		foldPlugins:  {"DELETE FROM plugins_daily", "INSERT INTO plugins_monthly", "ON CONFLICT (install, month, name) DO UPDATE", "sum(errors) OVER (PARTITION BY install, name)"},
	}
	if len(foldSteps) != 3 || foldSteps[0] != foldInstalls {
		t.Fatal("Basic fold must be the first of three separate steps")
	}
	for stmt, parts := range want {
		if !sqlGuardOK(stmt) || !strings.HasPrefix(stmt, "WITH ") {
			t.Fatal("not a single guarded WITH statement")
		}
		for _, part := range append(parts, "RETURNING") {
			if !strings.Contains(stmt, part) {
				t.Errorf("missing %q", part)
			}
		}
		low := strings.ToLower(stmt)
		if strings.Count(low, "delete") != 1 || strings.Contains(low, "current_date") || strings.Contains(low, "now()") {
			t.Fatal("unexpected clock or delete count")
		}
	}
}

// A failure in a Detailed fold must not stop the Basic fold of the same month.
func TestRollupBasicFoldRunsBeforeDetailedFailure(t *testing.T) {
	var ran []string
	callHost = func(fn string, a, out any) error {
		if fn == "db.exec" {
			sql := a.(map[string]any)["sql"].(string)
			ran = append(ran, sql)
			if sql != foldInstalls {
				return errors.New("statement timeout")
			}
			return nil
		}
		return json.Unmarshal([]byte(`{"rows":[[{"month":"2024-01-01"}]]}`), out)
	}
	if _, err := rollupOnce(date(2026, 10, 28)); err == nil || len(ran) < 2 || ran[0] != foldInstalls {
		t.Fatalf("err=%v ran=%d", err, len(ran))
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
	if len(db.execs) != len(foldSteps) {
		t.Fatalf("%d execs", len(db.execs))
	}
}

func TestOldestMonthLooksAtEveryDailyTable(t *testing.T) {
	if !sqlGuardOK(qOldestMonth) {
		t.Fatal("guard rejects qOldestMonth")
	}
	for _, tbl := range []string{"installs_daily", "detail_daily", "plugins_daily"} {
		if !strings.Contains(qOldestMonth, "FROM "+tbl+" WHERE day < $1::date") {
			t.Errorf("qOldestMonth ignores %s", tbl)
		}
	}
}
