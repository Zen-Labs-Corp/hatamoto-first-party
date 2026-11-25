package main

import (
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestParseRows(t *testing.T) {
	good := map[string][]string{
		"A-J":     {"A", "B", "C", "D", "E", "F", "G", "H", "I", "J"},
		"K,L,M":   {"K", "L", "M"},
		"AA-AD":   {"AA", "AB", "AC", "AD"},
		"1-12":    {"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"},
		"A-C, E":  {"A", "B", "C", "E"},
		" B ":     {"B"},
		"AZ-BB":   {"AZ", "BA", "BB"},
		"A1,A2":   {"A1", "A2"},
		"1-60":    nil, // checked by length below
		"A-A":     {"A"},
		"X-Z,1-2": {"X", "Y", "Z", "1", "2"},
	}
	for in, want := range good {
		got, ok := parseRows(in)
		if !ok {
			t.Errorf("parseRows(%q) refused", in)
			continue
		}
		if want != nil && !reflect.DeepEqual(got, want) {
			t.Errorf("parseRows(%q) = %v, want %v", in, got, want)
		}
	}
	if got, _ := parseRows("1-60"); len(got) != 60 {
		t.Errorf("1-60 gave %d rows", len(got))
	}
	for _, in := range []string{"", "A-", "-J", "J-A", "a-j", "1-61", "A-Z,1-35", "A,A", "A-C,B", "A-1", "AA-B", "0-5", "A--B", "ABCDE", "A B", "A-ZZ", "ZZ-AA", "A-Z,AA-AZ,1-9"} {
		if got, ok := parseRows(in); ok {
			t.Errorf("parseRows(%q) = %v, want refused", in, got)
		}
	}
}

func TestSeatLabel(t *testing.T) {
	if got := seatLabel("Stalls", "A", 12); got != "Stalls A-12" {
		t.Errorf("seatLabel = %q", got)
	}
}

func TestValidSection(t *testing.T) {
	grid := section{Kind: "grid", Name: "Stalls", Tier: "Std", Rows: []string{"A", "B"}, SeatsPerRow: 20}
	zone := section{Kind: "zone", Name: "Standing", Capacity: 80}
	if err := validSection(grid); err != nil {
		t.Errorf("grid: %v", err)
	}
	if err := validSection(zone); err != nil {
		t.Errorf("zone: %v", err)
	}
	bad := map[string]func(s section) section{
		"no name":        func(s section) section { s.Name = ""; return s },
		"padded name":    func(s section) section { s.Name = " X"; return s },
		"long name":      func(s section) section { s.Name = strings.Repeat("x", 81); return s },
		"control name":   func(s section) section { s.Name = "a\nb"; return s },
		"long tier":      func(s section) section { s.Tier = strings.Repeat("x", 41); return s },
		"bad position":   func(s section) section { s.Position = 1001; return s },
		"bad kind":       func(s section) section { s.Kind = "box"; return s },
		"grid no rows":   func(s section) section { s.Kind = "grid"; s.Rows = nil; s.SeatsPerRow = 5; return s },
		"grid no seats":  func(s section) section { s.Kind = "grid"; s.Rows = []string{"A"}; s.SeatsPerRow = 0; return s },
		"grid 101 seats": func(s section) section { s.Kind = "grid"; s.Rows = []string{"A"}; s.SeatsPerRow = 101; return s },
		"grid bad row":   func(s section) section { s.Kind = "grid"; s.Rows = []string{"a"}; s.SeatsPerRow = 5; return s },
		"grid dup row":   func(s section) section { s.Kind = "grid"; s.Rows = []string{"A", "A"}; s.SeatsPerRow = 5; return s },
		"grid capacity": func(s section) section {
			s.Kind = "grid"
			s.Rows = []string{"A"}
			s.SeatsPerRow = 5
			s.Capacity = 3
			return s
		},
		"zone empty": func(s section) section { s.Kind = "zone"; s.Capacity = 0; return s },
		"zone huge":  func(s section) section { s.Kind = "zone"; s.Capacity = 100001; return s },
		"zone rows":  func(s section) section { s.Kind = "zone"; s.Capacity = 5; s.Rows = []string{"A"}; return s },
	}
	for name, mod := range bad {
		if err := validSection(mod(grid)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	tooMany := section{Kind: "grid", Name: "Big", SeatsPerRow: 100}
	for _, r := range []string{"A", "B", "C"} {
		tooMany.Rows = append(tooMany.Rows, r)
	}
	if err := validSection(tooMany); err != nil {
		t.Errorf("300 seats: %v", err)
	}
}

// seatHost fakes the database for the seating statements: answers are
// popped per call (a cell for queries, rows_affected for changes).
type seatHost struct {
	cells    []string
	affected []int64
	calls    []call
}

func newSeatHost(t *testing.T) *seatHost {
	h := &seatHost{}
	orig := callHost
	t.Cleanup(func() { callHost = orig })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		h.calls = append(h.calls, call{fn, sql, ar})
		if fn == sdk.FnDBExec {
			var n int64
			if len(h.affected) > 0 {
				n, h.affected = h.affected[0], h.affected[1:]
			}
			return jsonInto(`{"rows_affected":`+itoa(n)+`}`, out)
		}
		cell := `null`
		if len(h.cells) > 0 {
			cell, h.cells = h.cells[0], h.cells[1:]
		}
		return jsonInto(`{"rows":[[`+cell+`]]}`, out)
	}
	return h
}

func mustContain(t *testing.T, sql string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(sql, p) {
			t.Errorf("statement lacks %q:\n%s", p, sql)
		}
	}
}

func TestSaveMapGuards(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{`7`, `0`}
	id, err := saveMap(0, "Main Hall")
	if err != nil || id != 7 {
		t.Fatalf("saveMap = %d, %v", id, err)
	}
	mustContain(t, h.calls[0].sql, "INSERT INTO seat_maps", "count(*) FROM seat_maps) < $2")
	if h.calls[0].args[0] != "Main Hall" || h.calls[0].args[1] != maxMaps {
		t.Errorf("args = %v", h.calls[0].args)
	}
	if _, err := saveMap(0, "x"); err != errTooMany {
		t.Errorf("full: %v", err)
	}
	h.affected = []int64{0}
	if _, err := saveMap(9, "x"); err != errNotFound {
		t.Errorf("rename missing: %v", err)
	}
}

func TestDeleteGuards(t *testing.T) {
	h := newSeatHost(t)
	h.affected = []int64{0, 0}
	h.cells = []string{`true`, `false`}
	if err := deleteMap(3); err != errInUse {
		t.Errorf("deleteMap in use: %v", err)
	}
	mustContain(t, h.calls[0].sql, "DELETE FROM seat_maps", "NOT EXISTS (SELECT 1 FROM ticket_types", "NOT EXISTS (SELECT 1 FROM attendees")
	if err := deleteSection(4); err != errNotFound {
		t.Errorf("deleteSection missing: %v", err)
	}
	mustContain(t, h.calls[2].sql, "DELETE FROM map_sections", "NOT EXISTS (SELECT 1 FROM attendees", "h.expires_at > now()")
	h.affected = []int64{1}
	if err := deleteSection(4); err != nil {
		t.Errorf("deleteSection: %v", err)
	}
}

func TestSaveSectionInsertShape(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{`12`, `0`, `true`}
	s := section{MapID: 2, Kind: "grid", Name: "Stalls", Tier: "Std", Position: 1, Rows: []string{"A", "B"}, SeatsPerRow: 20}
	id, err := saveSection(s)
	if err != nil || id != 12 {
		t.Fatalf("saveSection = %d, %v", id, err)
	}
	mustContain(t, h.calls[0].sql, "INSERT INTO map_sections", "INSERT INTO seats", "< $9", "<= $11", "WITH ORDINALITY", "generate_series(1, sec.seats_per_row)")
	if strings.Contains(h.calls[0].sql, "DELETE") {
		t.Error("an insert must not delete seats")
	}
	want := []any{int64(2), "grid", "Stalls", "Std", 1, 20, `["A","B"]`, 0, maxSectionsPerMap, 2, maxSeatsPerMap}
	if !reflect.DeepEqual(h.calls[0].args, want) {
		t.Errorf("args = %#v, want %#v", h.calls[0].args, want)
	}
	if _, err := saveSection(s); err != errTooMany {
		t.Errorf("full map: %v", err)
	}
}

func TestSaveSectionUpdateShape(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{`12`, `0`, `true`, `0`, `false`}
	s := section{ID: 12, MapID: 2, Kind: "grid", Name: "Stalls", Rows: []string{"A", "B", "C"}, SeatsPerRow: 20}
	if _, err := saveSection(s); err != nil {
		t.Fatal(err)
	}
	sql := h.calls[0].sql
	mustContain(t, sql, "ON CONFLICT (section_id, row_idx, num) DO UPDATE SET row_label = EXCLUDED.row_label",
		"DELETE FROM seats st USING sec", "NOT EXISTS (SELECT 1 FROM want w", "NOT EXISTS (SELECT 1 FROM attendees a JOIN seats st",
		"h.expires_at > now()", "t.taken > ", "UPDATE ticket_types t SET capacity", "<= $10", "c.kind = $2", "FOR UPDATE")
	if want := []any{int64(12), "grid", "Stalls", "", 0, 20, `["A","B","C"]`, 0, 3, maxSeatsPerMap}; !reflect.DeepEqual(h.calls[0].args, want) {
		t.Errorf("args = %#v, want %#v", h.calls[0].args, want)
	}
	// Refused: the section exists, so a guard said no.
	if _, err := saveSection(s); err != errInUse {
		t.Errorf("guarded update: %v", err)
	}
	// A zone takes its capacity from the form.
	h.cells = []string{`5`}
	z := section{ID: 5, Kind: "zone", Name: "Standing", Capacity: 80}
	if _, err := saveSection(z); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.calls[len(h.calls)-1].sql, "t.taken > $8::int")
	if h.calls[len(h.calls)-1].args[6] != `[]` {
		t.Errorf("zone rows arg = %v", h.calls[len(h.calls)-1].args[6])
	}
}

func TestSetBlockedGuards(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{`{"ok":true,"n":2}`, `{"ok":false,"n":0}`, `true`}
	n, err := setBlocked(4, []int64{10, 11}, true)
	if err != nil || n != 2 {
		t.Fatalf("setBlocked = %d, %v", n, err)
	}
	mustContain(t, h.calls[0].sql, "UPDATE seats s SET blocked", "h.expires_at > now()", "a.status IN ('pending', 'active')",
		">= 1", "t.taken >", "UPDATE ticket_types t SET capacity", "(SELECT count(*) FROM ids) = (SELECT count(*) FROM pick)")
	if want := []any{int64(4), `[10,11]`, true}; !reflect.DeepEqual(h.calls[0].args, want) {
		t.Errorf("args = %#v", h.calls[0].args)
	}
	if _, err := setBlocked(4, []int64{10}, true); err != errInUse {
		t.Errorf("refused: %v", err)
	}
}

func TestReadMapGroupsSeats(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{
		`{"id":2,"name":"Hall","seats":3,"sections":[{"id":9,"map_id":2,"kind":"grid","name":"S","tier":"","position":0,"rows":["A"],"seats_per_row":2,"capacity":0},{"id":10,"map_id":2,"kind":"zone","name":"Z","tier":"","position":1,"rows":[],"seats_per_row":0,"capacity":50}]}`,
		`[{"s":9,"c":[[1,0,"A",1,false],[2,0,"A",2,true]]}]`,
	}
	m, cells, err := readMap(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sections) != 2 || len(cells) != 2 || len(cells[0]) != 2 || len(cells[1]) != 0 {
		t.Fatalf("map = %+v cells = %+v", m, cells)
	}
	if c := cells[0][1]; c.ID != 2 || c.Num != 2 || !c.Blocked || c.Label != "A" {
		t.Errorf("cell = %+v", c)
	}
	h.cells = []string{`{}`}
	if _, _, err := readMap(99); err != errNotFound {
		t.Errorf("missing map: %v", err)
	}
}
