package main

import (
	"strings"
	"testing"
)

func TestSegmentWhere(t *testing.T) {
	cases := []struct {
		name string
		seg  segment
		want []string // substrings of the condition
		args string
		not  []string
	}{
		{"everyone", segment{}, []string{"o.status IN ('confirmed', 'paid')", "o.anonymised_at IS NULL", "o.email <> ''"}, `[]`,
			[]string{"o.event_id", "a.type_id", "checked_in_at", "total_cents", "o.created_at <"}},
		{"event", segment{EventID: "e1"}, []string{"AND o.event_id = $1"}, `["e1"]`, []string{"a.type_id", "checked_in_at"}},
		{"type", segment{EventID: "e1", TypeID: 5}, []string{"o.event_id = $1", "a.status = 'active' AND a.type_id = $2"}, `["e1",5]`, []string{"checked_in_at"}},
		{"checked in", segment{EventID: "e1", CheckedIn: "yes"}, []string{"o.event_id = $1", "a.checked_in_at IS NOT NULL"}, `["e1"]`, []string{"a.type_id"}},
		{"not checked in", segment{EventID: "e1", CheckedIn: "no"}, []string{"a.checked_in_at IS NULL"}, `["e1"]`, nil},
		{"bought before", segment{BoughtBefore: "2027-01-15"}, []string{"o.created_at < $1::timestamptz"}, `["2027-01-15T00:00:00Z"]`, []string{"o.event_id"}},
		{"rsvp only", segment{EventID: "e1", RSVPOnly: true}, []string{"o.event_id = $1", "o.total_cents = 0"}, `["e1"]`, nil},
		{"all together", segment{EventID: "e1", TypeID: 5, CheckedIn: "no", BoughtBefore: "2027-01-15", RSVPOnly: true},
			[]string{"o.event_id = $1", "a.type_id = $2", "a.checked_in_at IS NULL", "o.total_cents = 0", "o.created_at < $3::timestamptz"}, `["e1",5,"2027-01-15T00:00:00Z"]`, nil},
		{"not bought", segment{EventID: "e1", NotBought: true},
			[]string{"o.event_id <> $1", "NOT EXISTS (SELECT 1 FROM orders o2 JOIN attendees a2", "o2.event_id = $1", "lower(o2.email) = lower(o.email)"}, `["e1"]`, []string{"o.event_id = $1", "total_cents"}},
		{"not bought before", segment{EventID: "e1", NotBought: true, BoughtBefore: "2027-01-15"},
			[]string{"o.event_id <> $1", "o2.event_id = $1", "o.created_at < $2::timestamptz"}, `["e1","2027-01-15T00:00:00Z"]`, nil},
	}
	for _, c := range cases {
		if msg := c.seg.validate(); msg != "" {
			t.Errorf("%s: %s", c.name, msg)
			continue
		}
		cond, args := c.seg.where(1)
		for _, w := range c.want {
			if !strings.Contains(cond, w) {
				t.Errorf("%s: condition lacks %q:\n%s", c.name, w, cond)
			}
		}
		for _, n := range c.not {
			if strings.Contains(cond, n) {
				t.Errorf("%s: condition has %q:\n%s", c.name, n, cond)
			}
		}
		if jsonText(args) != c.args {
			t.Errorf("%s: args %s, want %s", c.name, jsonText(args), c.args)
		}
		if !strings.Contains(cond, "NOT EXISTS (SELECT 1 FROM unsubscribes") {
			t.Errorf("%s: unsubscribed people are not excluded", c.name)
		}
		// no value of the segment is part of the SQL text
		for _, v := range []string{"e1", "2027-01-15"} {
			if strings.Contains(cond, v) {
				t.Errorf("%s: %q is interpolated: %s", c.name, v, cond)
			}
		}
	}
}

func TestSegmentWhereNumbersFromStart(t *testing.T) {
	cond, args := segment{EventID: "e1", TypeID: 5}.where(4)
	if !strings.Contains(cond, "o.event_id = $4") || !strings.Contains(cond, "a.type_id = $5") || len(args) != 2 {
		t.Errorf("%s %v", cond, args)
	}
}

func TestSegmentValidate(t *testing.T) {
	bad := []segment{
		{EventID: "e 1"},
		{EventID: "e1'; DROP TABLE orders; --"},
		{EventID: "e1", TypeID: -1},
		{EventID: "e1", CheckedIn: "maybe"},
		{TypeID: 5},        // needs an event
		{CheckedIn: "yes"}, // needs an event
		{NotBought: true},  // needs an event
		{EventID: "e1", NotBought: true, TypeID: 5},
		{EventID: "e1", NotBought: true, CheckedIn: "no"},
		{EventID: "e1", NotBought: true, RSVPOnly: true},
		{BoughtBefore: "15/01/2027"},
		{BoughtBefore: "2027-13-01"},
		{BoughtBefore: "2027-01-15' OR 1=1"},
	}
	for _, g := range bad {
		if g.validate() == "" {
			t.Errorf("%+v passed validation", g)
		}
	}
	good := []segment{{}, {RSVPOnly: true}, {BoughtBefore: "2027-01-15"}, {EventID: "e1", NotBought: true, BoughtBefore: "2027-01-15"},
		{EventID: "e1", TypeID: 3, CheckedIn: "yes", RSVPOnly: true}}
	for _, g := range good {
		if msg := g.validate(); msg != "" {
			t.Errorf("%+v: %s", g, msg)
		}
	}
}

func TestCountSegmentSharesTheCondition(t *testing.T) {
	h := &eventHost{}
	h.install(t)
	callHost0 := callHost
	var sql string
	var args []any
	callHost = func(fn string, a, out any) error {
		m, _ := a.(map[string]any)
		if q, _ := m["sql"].(string); strings.Contains(q, "count(DISTINCT lower(o.email))") {
			sql, args = q, m["args"].([]any)
			return jsonInto(`{"rows":[[42]]}`, out)
		}
		return callHost0(fn, a, out)
	}
	g := segment{EventID: "e1", TypeID: 5}
	n, err := countSegment(g)
	if err != nil || n != 42 {
		t.Fatalf("%d %v", n, err)
	}
	cond, wantArgs := g.where(1)
	if !strings.HasSuffix(sql, "WHERE "+cond) || jsonText(args) != jsonText(wantArgs) {
		t.Errorf("the count does not use where(): %s %v", sql, args)
	}
}
