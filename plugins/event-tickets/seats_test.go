package main

import (
	"reflect"
	"strings"
	"testing"
)

func rowsOf(states ...string) []availRow {
	out := make([]availRow, len(states))
	for i, s := range states {
		out[i] = availRow{L: string(rune('A' + i)), S: s}
	}
	return out
}

func TestBestSeats(t *testing.T) {
	cases := []struct {
		name string
		rows []availRow
		n    int
		want [][2]int
	}{
		{"one seat takes the middle", rowsOf("ooooo"), 1, [][2]int{{0, 3}}},
		{"contiguous block nearest the centre", rowsOf("oooooooooo"), 2, [][2]int{{0, 5}, {0, 6}}},
		{"odd block in an even row", rowsOf("oooooooooo"), 3, [][2]int{{0, 4}, {0, 5}, {0, 6}}},
		{"front row first", rowsOf("ooooo", "ooooo"), 2, [][2]int{{0, 2}, {0, 3}}},
		{"front row full: next row", rowsOf("xxxxx", "ooooo"), 2, [][2]int{{1, 2}, {1, 3}}},
		{"block around a taken centre", rowsOf("ooxoooo"), 2, [][2]int{{0, 4}, {0, 5}}},
		{"blocked and off seats are skipped", rowsOf("obooon"), 2, [][2]int{{0, 3}, {0, 4}}},
		{"a later row with a block beats singles in the front", rowsOf("oxoxo", "oooxx"), 2, [][2]int{{1, 2}, {1, 3}}},
		{"singles centre-out when no row has a block", rowsOf("oxoxo"), 3, [][2]int{{0, 3}, {0, 1}, {0, 5}}},
		{"singles run over rows", rowsOf("oxo", "xox"), 3, [][2]int{{0, 1}, {0, 3}, {1, 2}}},
		{"more than free", rowsOf("oxo", "xxx"), 3, nil},
		{"nothing free", rowsOf("xxx", "bbb"), 1, nil},
		{"zero", rowsOf("ooo"), 0, nil},
		{"no rows", nil, 1, nil},
	}
	for _, c := range cases {
		if got := bestSeats(c.rows, c.n); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: bestSeats = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSeatAvailability(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{`[
 {"id":9,"kind":"grid","name":"Stalls","tier":"Std","type_id":4,"price_cents":2500,"left":3,"rows":[{"l":"A","s":"oxb"},{"l":"B","s":"ooo"}]},
 {"id":10,"kind":"zone","name":"Standing","tier":"","type_id":5,"price_cents":1000,"left":80,"rows":null}]`, `[]`}
	m, err := seatAvailability("e1", "USD")
	if err != nil || m == nil || len(m.Sections) != 2 {
		t.Fatalf("map = %+v, %v", m, err)
	}
	if len(h.calls) != 1 {
		t.Errorf("%d host calls, want one", len(h.calls))
	}
	g, z := m.Sections[0], m.Sections[1]
	if g.Price != "25.00 USD" || len(g.Rows) != 2 || g.Rows[0].S != "oxb" || g.Left != 0 || g.TypeID != 4 {
		t.Errorf("grid = %+v", g)
	}
	if z.Kind != "zone" || z.Left != 80 || z.Rows != nil || z.Price != "10.00 USD" {
		t.Errorf("zone = %+v", z)
	}
	sql := h.calls[0].sql
	mustContain(t, sql, "h.expires_at > now()", "a.status IN ('pending', 'active')", "a.event_id = $1", "h.event_id = $1",
		"WHEN st.blocked THEN 'b'", "WHEN NOT t.on_sale THEN 'n'", "WHERE t.event_id = $1", "JOIN map_sections s ON s.id = t.section_id")
	for _, banned := range []string{"orders", "o.email", "o.name", "code"} {
		if strings.Contains(sql, banned) {
			t.Errorf("the shared reply must not read buyer data: %q", banned)
		}
	}
	if h.calls[0].args[0] != "e1" {
		t.Errorf("args = %v", h.calls[0].args)
	}
	// no bound type: no map
	if m, err := seatAvailability("e1", "USD"); m != nil || err != nil {
		t.Errorf("unbound event: %+v, %v", m, err)
	}
}
