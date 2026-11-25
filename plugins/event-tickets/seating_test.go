package main

import (
	"reflect"
	"strings"
	"testing"
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
