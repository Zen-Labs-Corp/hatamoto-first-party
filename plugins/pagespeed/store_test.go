package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type dbCall struct {
	Fn, SQL string
	Args    []any
}

// fakeDB serves db.query with answer(sql) (one jsonb cell) and records every db call.
func fakeDB(t *testing.T, calls *[]dbCall, answer func(sql string) string) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		m := args.(map[string]any)
		*calls = append(*calls, dbCall{fn, m["sql"].(string), m["args"].([]any)})
		if fn == "db.query" {
			cell, _ := json.Marshal(answer(m["sql"].(string)))
			return json.Unmarshal([]byte(`{"rows":[[`+string(cell)+`]]}`), out)
		}
		return nil
	}
}

func TestCutoffDay(t *testing.T) {
	tests := []struct{ now, want string }{
		{"2026-11-04", "2025-10-01"},
		{"2026-01-31", "2024-12-01"},
		{"2024-02-29", "2023-01-01"},
		{"2026-12-31", "2025-11-01"},
	}
	for _, tc := range tests {
		d, _ := time.Parse(dateFmt, tc.now)
		if got := cutoffDay(d).Format(dateFmt); got != tc.want {
			t.Errorf("cutoffDay(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
}

func TestRetentionDeletes(t *testing.T) {
	var calls []dbCall
	fakeDB(t, &calls, nil)
	d, _ := time.Parse(dateFmt, "2026-11-04")
	if err := runRetention(d); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	for i, want := range []string{"2025-10-01", "2025-10-01", "2026-10-28"} {
		if !strings.HasPrefix(calls[i].SQL, "DELETE FROM") || calls[i].Args[0] != want {
			t.Errorf("call %d = %+v, want arg %s", i, calls[i], want)
		}
	}
}

func progressJSON(rows ...progressRow) func(string) string {
	return func(sql string) string {
		b, _ := json.Marshal(append([]progressRow{}, rows...))
		return string(b)
	}
}

func TestNextWorkOrderAndRetry(t *testing.T) {
	var calls []dbCall
	targets, strategies := []string{"/a", "/b"}, []string{"mobile"}
	next := func(rows ...progressRow) (workItem, bool) {
		fakeDB(t, &calls, progressJSON(rows...))
		w, ok, err := nextWork("2026-11-04", targets, strategies)
		if err != nil {
			t.Fatal(err)
		}
		return w, ok
	}
	if w, ok := next(); !ok || w != (workItem{"/a", "mobile", stepCrux}) {
		t.Fatalf("fresh day: %+v", w)
	}
	if w, ok := next(progressRow{URL: "/a", Strategy: "mobile", CruxDone: true, LabState: labPending}); !ok || w != (workItem{"/b", "mobile", stepCrux}) {
		t.Fatalf("crux before lab: %+v", w)
	}
	both := func(a, b progressRow) []progressRow { return []progressRow{a, b} }
	rows := both(progressRow{URL: "/a", Strategy: "mobile", CruxDone: true, LabState: labTimeout, LabAttempts: 1},
		progressRow{URL: "/b", Strategy: "mobile", CruxDone: true, LabState: labPending})
	if w, ok := next(rows...); !ok || w != (workItem{"/b", "mobile", stepLab}) {
		t.Fatalf("pending lab before retry: %+v", w)
	}
	rows[1].LabState = labOK
	if w, ok := next(rows...); !ok || w != (workItem{"/a", "mobile", stepLab}) {
		t.Fatalf("first retry: %+v", w)
	}
	rows[0].LabAttempts = 2
	if w, ok := next(rows...); !ok || w != (workItem{"/a", "mobile", stepLab}) {
		t.Fatalf("second retry: %+v", w)
	}
	rows[0].LabAttempts = maxLabAttempts
	if w, ok := next(rows...); ok {
		t.Fatalf("done day returned %+v", w)
	}
}

func TestUpsertsKeepTheirOwnColumns(t *testing.T) {
	var calls []dbCall
	fakeDB(t, &calls, nil)
	v := 2600.0
	if err := upsertField("2026-11-04", "/a", "mobile", fieldResult{Status: fieldOK, LCP: &v}); err != nil {
		t.Fatal(err)
	}
	if err := upsertLab("2026-11-04", "/a", "mobile", labResult{Status: labTimeout}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls[0].SQL, "lab_") || calls[0].Args[6] != catNeeds {
		t.Errorf("field upsert = %+v", calls[0])
	}
	if strings.Contains(calls[1].SQL, "lcp_ms") || calls[1].Args[3] != nil || calls[1].Args[4] != labTimeout {
		t.Errorf("lab upsert = %+v", calls[1])
	}
}

func TestFailedLabNeverReplacesFinishedOne(t *testing.T) {
	var calls []dbCall
	fakeDB(t, &calls, nil)
	if err := upsertLab("2026-11-04", "/a", "mobile", labResult{Status: labTimeout}); err != nil {
		t.Fatal(err)
	}
	if err := markLab("2026-11-04", "/a", "mobile", labTimeout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls[0].SQL, "WHERE metrics.lab_status <> 'ok' OR EXCLUDED.lab_status = 'ok'") {
		t.Errorf("lab upsert has no guard: %s", calls[0].SQL)
	}
	if !strings.Contains(calls[1].SQL, "progress.lab_state = 'ok' AND EXCLUDED.lab_state <> 'ok'") {
		t.Errorf("markLab has no guard: %s", calls[1].SQL)
	}
}
