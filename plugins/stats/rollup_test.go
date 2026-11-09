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

// rollupDB answers the oldest-month queries from a per-table list and records
// the rest. A fold of a table in failing returns an error and moves nothing.
type rollupDB struct {
	months  map[string][]string // table -> months still to fold, oldest first
	failing map[string]bool     // table -> its fold fails
	execs   []hostCall
	reads   []hostCall
}

func newRollupDB(months ...string) *rollupDB {
	r := &rollupDB{months: map[string][]string{}, failing: map[string]bool{}}
	for _, step := range foldSteps {
		r.months[step.table] = append([]string(nil), months...)
	}
	return r
}

func (r *rollupDB) install(t *testing.T) {
	callHost = func(fn string, a, out any) error {
		m := a.(map[string]any)
		c := hostCall{fn, m["sql"].(string), m["args"].([]any)}
		if fn == "db.exec" {
			r.execs = append(r.execs, c)
			for _, step := range foldSteps {
				if c.sql == step.fold {
					if r.failing[step.table] {
						return errors.New("statement timeout")
					}
					r.months[step.table] = r.months[step.table][1:]
				}
			}
			return nil
		}
		r.reads = append(r.reads, c)
		cell := `{"month":null}`
		for _, step := range foldSteps {
			if c.sql == oldestMonthQuery(step.table) && len(r.months[step.table]) > 0 {
				cell = `{"month":"` + r.months[step.table][0] + `"}`
			}
		}
		return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
	}
}

func TestRollupStopsWhenNothingOld(t *testing.T) {
	db := newRollupDB()
	db.install(t)
	n, err := rollupOnce(date(2026, 10, 28))
	if n != 0 || err != nil || len(db.execs) != 0 || len(db.reads) != 3 {
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
	db := newRollupDB("2024-01-01", "2024-02-01", "2024-03-01", "2024-04-01")
	db.install(t)
	n, err := rollupOnce(date(2026, 10, 28))
	if n != 3 || err != nil || len(db.execs) != 9 {
		t.Fatalf("n=%d err=%v execs=%d", n, err, len(db.execs))
	}
	for i, step := range foldSteps {
		for j, want := range []string{"2024-01-01", "2024-02-01", "2024-03-01"} {
			if e := db.execs[i*3+j]; e.args[0] != want || e.sql != step.fold {
				t.Fatalf("exec %d/%d: %+v", i, j, e)
			}
		}
		if len(db.months[step.table]) != 1 {
			t.Fatalf("%s remaining %v", step.table, db.months[step.table])
		}
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
	if len(foldSteps) != 3 || foldSteps[0].fold != foldInstalls {
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

// A Detailed fold stuck on one month must not stop the Basic fold of that
// month or of later ones, and the cron run still reports the error.
func TestRollupStuckDetailedFoldDoesNotBlockBasic(t *testing.T) {
	db := newRollupDB("2024-01-01", "2024-02-01", "2024-03-01")
	db.failing["detail_daily"] = true
	db.install(t)
	n, err := rollupOnce(date(2026, 10, 28))
	if err == nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(db.months["installs_daily"]) != 0 || len(db.months["plugins_daily"]) != 0 {
		t.Fatalf("Basic %v plugins %v", db.months["installs_daily"], db.months["plugins_daily"])
	}
	if len(db.months["detail_daily"]) != 3 {
		t.Fatalf("detail %v", db.months["detail_daily"])
	}
}

func TestCronRegisteredAndReturnsError(t *testing.T) {
	old := now
	defer func() { now = old }()
	now = func() time.Time { return date(2026, 10, 28) }
	db := newRollupDB("2024-01-01")
	db.install(t)
	in, _ := json.Marshal(sdk.CronEvent{})
	if _, err := guest.Handle(guest.CronExport("rollup"), in); err != nil {
		t.Fatal(err)
	}
	if len(db.execs) != len(foldSteps) {
		t.Fatalf("%d execs", len(db.execs))
	}
}

func TestOldestMonthLooksAtOwnTable(t *testing.T) {
	for _, step := range foldSteps {
		q := oldestMonthQuery(step.table)
		if !sqlGuardOK(q) || !strings.Contains(q, "FROM "+step.table+" WHERE day < $1::date") {
			t.Errorf("bad oldest-month query for %s", step.table)
		}
	}
}
