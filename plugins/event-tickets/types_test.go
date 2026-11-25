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
	if got := jsonText(c.args); got != `["e1","Standard",30,true,true,2,20,0]` {
		t.Errorf("args %s", got)
	}
	if !strings.Contains(c.sql, "price_cents") {
		t.Error("the price is written")
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
		"negative price": `{"name":"x","price_cents":-1}`,
		"price too big":  `{"name":"x","price_cents":100000000}`,
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
	orig := onSeatsFreed
	onSeatsFreed = func(id int64) { freed = append(freed, id) }
	t.Cleanup(func() { onSeatsFreed = orig })
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

func TestSaveTypeWithPrice(t *testing.T) {
	h := newAdminHost(t)
	if code, body := post(t, "/admin/events/{id}/types", "e1", editor, `{"name":"VIP","price_cents":2500}`); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	c := h.sawSQL("INSERT INTO ticket_types")
	// a priced type has no waitlist, whatever was sent
	if got := jsonText(c.args); got != `["e1","VIP",30,false,true,0,20,2500]` {
		t.Errorf("insert args %s", got)
	}
	if code, _ := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"VIP","capacity":5,"price_cents":99999999}`); code != 200 {
		t.Fatal(code)
	}
	u := h.sawSQL("UPDATE ticket_types SET name")
	if !strings.Contains(u.sql, "price_cents = $8") || jsonText(u.args) != `[7,"e1","VIP",5,false,true,0,99999999]` {
		t.Errorf("update %s %s", u.sql, jsonText(u.args))
	}
}

const boundOK = `{"n":1,"section":true,"type":true,"priced":true,"one_map":true,"free":true,"movable":true,"roomy":true,"room":true}`

func TestSaveBoundAdd(t *testing.T) {
	h := newAdminHost(t)
	h.bound = boundOK
	code, body := post(t, "/admin/events/{id}/types", "e1", editor, `{"name":"Stalls","price_cents":2500,"capacity":999,"section_id":12}`)
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	c := h.sawSQL("WITH sec AS")
	if c == nil {
		t.Fatal("no bound statement")
	}
	// the form capacity never reaches the statement; the section sets it
	if got := jsonText(c.args); got != `["e1","Stalls",true,0,2500,12,20]` {
		t.Errorf("args %s", got)
	}
	for _, want := range []string{"INSERT INTO ticket_types", "sec.cap", "$5::bigint > 0", "os.map_id <> (SELECT map_id FROM sec)",
		"o.section_id = $6", "count(*) FROM ticket_types WHERE event_id = $1) < $7", "NOT st.blocked", "waitlist"} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
	if h.sawSQL("INSERT INTO ticket_types") != nil {
		t.Error("the unbound insert must not run for a bound type")
	}
}

func TestSaveBoundChange(t *testing.T) {
	h := newAdminHost(t)
	h.bound = boundOK
	if code, body := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Stalls","price_cents":2500,"section_id":12}`); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	c := h.sawSQL("WITH sec AS")
	if got := jsonText(c.args); got != `[7,"e1","Stalls",true,0,2500,12]` {
		t.Errorf("args %s", got)
	}
	for _, want := range []string{"c.section_id = $7 OR c.taken = 0", "c.taken <= s.cap", "o.id <> $1", "FOR UPDATE", "section_id = sec.id"} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
}

func TestSaveBoundRefusals(t *testing.T) {
	cases := []struct {
		name, answer string
		code         int
		text         string
	}{
		{"free seats", `{"n":0,"section":true,"type":true,"priced":false,"one_map":true,"free":true,"movable":true,"roomy":true}`, 422, "A seat section needs a price above zero"},
		{"other map", `{"n":0,"section":true,"type":true,"priced":true,"one_map":false,"free":true,"movable":true,"roomy":true}`, 409, "An event uses one map; this section is on another map"},
		{"section used", `{"n":0,"section":true,"type":true,"priced":true,"one_map":true,"free":false,"movable":true,"roomy":true}`, 409, "Another ticket type of this event already uses this section"},
		{"sold", `{"n":0,"section":true,"type":true,"priced":true,"one_map":true,"free":true,"movable":false,"roomy":true}`, 409, "This ticket type has sold tickets; add a new type for another section"},
		{"too small", `{"n":0,"section":true,"type":true,"priced":true,"one_map":true,"free":true,"movable":true,"roomy":false}`, 409, "capacity below tickets already taken"},
		{"no section", `{"n":0,"section":false,"type":true,"priced":true,"one_map":true,"free":true,"movable":true,"roomy":false}`, 404, "not found"},
		{"no type", `{"n":0,"section":true,"type":false,"priced":true,"one_map":true,"free":true,"movable":false,"roomy":false}`, 404, "not found"},
	}
	for _, c := range cases {
		h := newAdminHost(t)
		h.bound = c.answer
		code, body := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Stalls","price_cents":0,"section_id":12}`)
		if code != c.code || body["error"] != c.text {
			t.Errorf("%s: %d %v", c.name, code, body)
		}
		if h.sawSQL("INSERT INTO events") != nil {
			t.Errorf("%s: a refused save snapshots nothing", c.name)
		}
	}
	h := newAdminHost(t)
	h.bound = `{"n":0,"section":true,"priced":true,"one_map":true,"free":true,"room":false}`
	if code, body := post(t, "/admin/events/{id}/types", "e1", editor, `{"name":"Stalls","price_cents":5,"section_id":12}`); code != 409 || body["error"] != "too many ticket types" {
		t.Errorf("add over the cap: %d %v", code, body)
	}
	if code, _ := post(t, "/admin/events/{id}/types", "e1", editor, `{"name":"Stalls","section_id":-1}`); code != 400 {
		t.Errorf("negative section: %d", code)
	}
}

func TestSaveTypeKeepsOrDropsBinding(t *testing.T) {
	h := newAdminHost(t)
	h.types = `[{"id":7,"event_id":"e1","name":"Stalls","capacity":100,"taken":0,"waitlist":false,"on_sale":true,"position":0,"price_cents":2500,"section_id":12,"seated":true}]`
	h.bound = boundOK
	// no section_id in the body: the type stays bound
	if code, _ := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Stalls","price_cents":2500}`); code != 200 {
		t.Fatal(code)
	}
	if c := h.sawSQL("WITH sec AS"); c == nil || c.args[len(c.args)-1] != int64(12) {
		t.Errorf("kept binding: %+v", c)
	}
	// section_id 0: unbound, only while nothing is sold
	h = newAdminHost(t)
	h.bound = `{"n":1,"found":true,"sold":false}`
	if code, _ := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Stalls","capacity":40,"price_cents":2500,"section_id":0}`); code != 200 {
		t.Fatal(code)
	}
	c := h.sawSQL("WITH cur AS")
	if c == nil || !strings.Contains(c.sql, "section_id = NULL") || !strings.Contains(c.sql, "cur.taken = 0") {
		t.Errorf("unbind statement: %+v", c)
	}
	h.bound = `{"n":0,"found":true,"sold":true}`
	if code, body := post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Stalls","capacity":40,"price_cents":2500,"section_id":0}`); code != 409 {
		t.Errorf("unbind with sales: %d %v", code, body)
	}
}

func TestUnboundUpdateNeverTouchesBoundTypes(t *testing.T) {
	h := newAdminHost(t)
	post(t, "/admin/events/{id}/types", "e1", editor, `{"id":7,"name":"Standard","capacity":5}`)
	if c := h.sawSQL("UPDATE ticket_types SET name"); c == nil || !strings.Contains(c.sql, "AND section_id IS NULL") {
		t.Errorf("the plain update must leave a bound type's capacity to its section: %+v", c)
	}
}

func TestEventDetailOffersMaps(t *testing.T) {
	h := newAdminHost(t)
	h.types = `[{"id":7,"event_id":"e1","name":"Stalls","capacity":100,"taken":0,"waitlist":false,"on_sale":true,"position":0,"price_cents":2500,"section_id":12,"seated":true}]`
	callHost0 := callHost
	callHost = func(fn string, args, out any) error {
		if a, _ := args.(map[string]any); strings.Contains(fmt.Sprint(a["sql"]), "FROM seat_maps m") {
			return jsonInto(`{"rows":[[[{"id":3,"name":"Hall","seats":60,"sections":[{"id":12,"map_id":3,"kind":"grid","name":"Stalls","tier":"Std","position":0,"rows":["A"],"seats_per_row":60,"capacity":0}]}]]]}`, out)
		}
		return callHost0(fn, args, out)
	}
	code, body := get(t, "/admin/events/{id}", "e1", editor, nil)
	maps, _ := body["maps"].([]any)
	if code != 200 || len(maps) != 1 {
		t.Fatalf("%d %v", code, body)
	}
	sec := maps[0].(map[string]any)["sections"].([]any)[0].(map[string]any)
	if sec["name"] != "Stalls" || sec["tier"] != "Std" || sec["kind"] != "grid" || sec["id"] != float64(12) || sec["rows"] != nil {
		t.Errorf("section choice = %v", sec)
	}
	ty := body["types"].([]any)[0].(map[string]any)
	if ty["section_id"] != float64(12) || ty["seated"] != true {
		t.Errorf("type = %v", ty)
	}
}
