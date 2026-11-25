package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestSaveTypeAdds(t *testing.T) {
	h := newAdminHost(t)
	code, body := post(t, "/admin/events/{id}/types", "e1", editor, `{"name":" Standard ","position":2}`)
	if code != 200 || body["ok"] != true {
		t.Fatalf("%d %v", code, body)
	}
	c := h.sawSQL("INSERT INTO ticket_types")
	if c == nil {
		t.Fatal("no insert")
	}
	// defaults: the default capacity of the settings, waitlist and sale on
	if got := jsonText(c.args); got != `["e1","Standard",30,true,true,2,20]` {
		t.Errorf("args %s", got)
	}
	if !strings.Contains(c.sql, "WHERE (SELECT count(*)") {
		t.Error("the number of types is capped in the statement")
	}
	if h.sawSQL("INSERT INTO events") == nil {
		t.Error("the event is snapshotted")
	}
	for _, c := range h.calls {
		if strings.Contains(c.sql, "Standard") {
			t.Error("input must travel as arguments only")
		}
	}
}

func TestSaveTypeValidation(t *testing.T) {
	h := newAdminHost(t)
	for name, body := range map[string]string{
		"unknown key":    `{"name":"x","price":3}`,
		"no name":        `{"name":"  "}`,
		"long name":      `{"name":"` + strings.Repeat("a", 81) + `"}`,
		"control char":   `{"name":"a\u0000b"}`,
		"capacity zero":  `{"name":"x","capacity":0}`,
		"capacity big":   `{"name":"x","capacity":100001}`,
		"negative pos":   `{"name":"x","position":-1}`,
		"not json":       `nope`,
		"trailing data":  `{"name":"x"} {"name":"y"}`,
		"oversized body": `{"name":"x","pad":"` + strings.Repeat("a", 17000) + `"}`,
	} {
		if code, _ := post(t, "/admin/events/{id}/types", "e1", editor, body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if h.sawSQL("INSERT INTO ticket_types") != nil {
		t.Error("an invalid body must store nothing")
	}
}

func TestSaveTypeCapacityBelowTaken(t *testing.T) {
	h := newAdminHost(t)
	h.updateRows, h.exists = 0, true
	code, body := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Standard","capacity":5}`)
	if code != 409 || body["error"] != "capacity below tickets already taken" {
		t.Errorf("%d %v", code, body)
	}
	c := h.sawSQL("UPDATE ticket_types SET name")
	if c == nil || !strings.Contains(c.sql, "taken <= $4") || !strings.Contains(c.sql, "event_id = $2") {
		t.Errorf("the check must sit in the update itself: %+v", c)
	}
	h.exists = false
	if code, _ := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Standard","capacity":5}`); code != 404 {
		t.Errorf("unknown type: %d", code)
	}
}

func TestSaveTypeRaisingCapacityOffersSeats(t *testing.T) {
	newAdminHost(t)
	var freed []int64
	onSeatsFreed = func(id int64) { freed = append(freed, id) }
	t.Cleanup(func() { onSeatsFreed = func(int64) {} })
	if code, _ := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Standard","capacity":90}`); code != 200 {
		t.Fatal(code)
	}
	if fmt.Sprint(freed) != "[7]" {
		t.Errorf("freed %v", freed)
	}
}

func TestSaveTypeTooMany(t *testing.T) {
	h := newAdminHost(t)
	h.insertRows = 0
	if code, _ := post(t, "/admin/events/{id}/types", "e1", editor, `{"name":"x"}`); code != 409 {
		t.Errorf("%d", code)
	}
}

func TestDeleteType(t *testing.T) {
	h := newAdminHost(t)
	h.deleteRows = 1
	code, body := post(t, "/admin/types/{id}/delete", "7", editor, ``)
	if code != 200 || body["closed"] != false {
		t.Errorf("%d %v", code, body)
	}
	h.deleteRows = 0 // attendees or waitlist entries exist: sales close instead
	code, body = post(t, "/admin/types/{id}/delete", "7", editor, ``)
	if code != 200 || body["closed"] != true || h.sawSQL("UPDATE ticket_types SET on_sale = false") == nil {
		t.Errorf("%d %v", code, body)
	}
	c := h.sawSQL("DELETE FROM ticket_types")
	if !strings.Contains(c.sql, "FROM attendees") || !strings.Contains(c.sql, "FROM waitlist") {
		t.Error("only an unused type is deleted")
	}
}

func TestFieldKeyAndValidation(t *testing.T) {
	for label, want := range map[string]string{
		"Dietary needs": "dietary_needs", "  T-shirt size!": "t_shirt_size", "2nd guest": "f_2nd_guest", "年齢": "field",
		"A very very long label that goes past thirty two characters": "a_very_very_long_label_that_goes",
	} {
		if got := fieldKey(label); got != want || !fieldKeyRe.MatchString(got) {
			t.Errorf("fieldKey(%q) = %q, want %q", label, got, want)
		}
	}
	good, err := validateFields([]field{
		{Label: "Dietary needs", Kind: "text"},
		{Label: "Size", Kind: "select", Options: []string{"S", " M "}, Required: true},
		{Key: "kept_key", Label: "Renamed label", Kind: "checkbox", Options: []string{"ignored"}},
	})
	if err != nil || good[0].Key != "dietary_needs" || good[1].Options[1] != "M" || good[2].Key != "kept_key" || len(good[2].Options) != 0 {
		t.Fatalf("%+v %v", good, err)
	}
	many := make([]field, 11)
	for i := range many {
		many[i] = field{Label: fmt.Sprintf("Question %d", i), Kind: "text"}
	}
	opts := make([]string, 21)
	for i := range opts {
		opts[i] = "o"
	}
	for name, in := range map[string][]field{
		"11 fields":    many,
		"duplicate":    {{Label: "Size", Kind: "text"}, {Label: "size", Kind: "text"}},
		"no label":     {{Label: " ", Kind: "text"}},
		"bad kind":     {{Label: "x", Kind: "file"}},
		"select empty": {{Label: "x", Kind: "select"}},
		"21 options":   {{Label: "x", Kind: "select", Options: opts}},
		"empty option": {{Label: "x", Kind: "select", Options: []string{" "}}},
		"control char": {{Label: "x\ny", Kind: "text"}},
	} {
		if _, err := validateFields(in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSaveFieldsRoute(t *testing.T) {
	h := newAdminHost(t)
	code, body := post(t, "/admin/events/{id}/fields", "e1", editor, `{"fields":[{"label":"Size","kind":"select","options":["S","M"],"required":true}]}`)
	if code != 200 || body["ok"] != true {
		t.Fatalf("%d %v", code, body)
	}
	c := h.sawSQL("WITH d AS")
	if c == nil {
		t.Fatal("no replace statement")
	}
	if !strings.Contains(c.sql, "DELETE FROM fields") || !strings.Contains(c.sql, "ON CONFLICT") {
		t.Error("one statement deletes, inserts and updates")
	}
	if c.args[0] != "e1" || !strings.Contains(c.args[1].(string), `"key":"size"`) || !strings.Contains(c.args[1].(string), `"pos":0`) {
		t.Errorf("args %v", c.args)
	}
	for name, b := range map[string]string{
		"unknown key": `{"fields":[],"x":1}`,
		"bad kind":    `{"fields":[{"label":"a","kind":"file"}]}`,
		"field key":   `{"fields":[{"label":"a","kind":"text","extra":1}]}`,
	} {
		if code, _ := post(t, "/admin/events/{id}/fields", "e1", editor, b); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	eleven := `{"fields":[` + strings.TrimSuffix(strings.Repeat(`{"label":"a","kind":"text"},`, 11), ",") + `]}`
	if code, _ := post(t, "/admin/events/{id}/fields", "e1", editor, eleven); code != 400 {
		t.Errorf("11 fields: %d", code)
	}
}
