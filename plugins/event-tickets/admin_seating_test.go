package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestSeatingRoutesRefuseNoUser(t *testing.T) {
	h := newSeatHost(t)
	for _, c := range []struct{ method, pattern string }{
		{"GET", "/admin/maps"}, {"GET", "/admin/maps/{id}"}, {"POST", "/admin/maps"}, {"POST", "/admin/maps/{id}/delete"},
		{"POST", "/admin/maps/{id}/sections"}, {"POST", "/admin/sections/{id}/delete"}, {"POST", "/admin/sections/{id}/seats"},
	} {
		code, _ := request(t, sdkRequest(c.method, c.pattern, "1", nil, `{}`))
		if code != 403 {
			t.Errorf("%s %s without a user: %d", c.method, c.pattern, code)
		}
	}
	if len(h.calls) != 0 {
		t.Errorf("a request without a user reached the host: %v", h.calls)
	}
}

func TestSeatingBadRequests(t *testing.T) {
	h := newSeatHost(t)
	for _, id := range []string{"abc", "0", "-1", "1 2", strings.Repeat("1", 19)} {
		for _, p := range []string{"/admin/maps/{id}/delete", "/admin/sections/{id}/delete", "/admin/maps/{id}/sections", "/admin/sections/{id}/seats"} {
			if code, _ := post(t, p, id, editor, `{}`); code != 400 {
				t.Errorf("%s %q: %d", p, id, code)
			}
		}
		if code, _ := get(t, "/admin/maps/{id}", id, editor, nil); code != 400 {
			t.Errorf("map %q: %d", id, code)
		}
	}
	bodies := map[string][]string{
		"/admin/maps": {`{"name":""}`, `{"name":"x","extra":1}`, `{"name":"x"} {}`, `{"name":"x","id":-1}`, `[]`},
		"/admin/maps/{id}/sections": {
			`{"kind":"grid","name":"S","rows":"A-J","seats_per_row":20,"extra":1}`,
			`{"kind":"grid","name":"S","rows":"J-A","seats_per_row":20}`,
			`{"kind":"grid","name":"S","rows":"A-J","seats_per_row":0}`,
			`{"kind":"grid","name":"S","rows":"A-J","seats_per_row":101}`,
			`{"kind":"grid","name":"  ","rows":"A","seats_per_row":2}`,
			`{"kind":"zone","name":"Z","capacity":0}`,
			`{"kind":"zone","name":"Z","capacity":5,"rows":"A"}`,
			`{"kind":"box","name":"Z","capacity":5}`,
			`{"kind":"grid","name":"S","rows":"A","seats_per_row":2,"id":-3}`,
		},
		"/admin/sections/{id}/seats": {`{"seats":[],"blocked":true}`, `{"seats":[0],"blocked":true}`, `{"seats":[1],"blocked":true,"x":1}`,
			`{"seats":[` + strings.Repeat("1,", 500) + `1],"blocked":true}`},
	}
	for p, list := range bodies {
		for _, b := range list {
			if code, _ := post(t, p, "5", editor, b); code != 400 {
				t.Errorf("%s %.60s: %d", p, b, code)
			}
		}
	}
	if len(h.calls) != 0 {
		t.Errorf("a bad request reached the host: %d calls, first %q", len(h.calls), h.calls[0].sql)
	}
}

func TestSeatingHappyPaths(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{`3`}
	code, body := post(t, "/admin/maps", "", editor, `{"name":" Main Hall "}`)
	if code != 200 || body["id"] != float64(3) || h.calls[0].args[0] != "Main Hall" {
		t.Errorf("save map: %d %v %v", code, body, h.calls[0].args)
	}

	h.calls, h.cells = nil, []string{`12`}
	code, body = post(t, "/admin/maps/{id}/sections", "3", editor, `{"kind":"grid","name":"Stalls","tier":"Std","rows":"A-C","seats_per_row":20,"position":1}`)
	if code != 200 || body["id"] != float64(12) {
		t.Fatalf("save section: %d %v", code, body)
	}
	if a := h.calls[0].args; a[0] != int64(3) || a[1] != "grid" || a[2] != "Stalls" || a[3] != "Std" || a[6] != `["A","B","C"]` || a[9] != 3 {
		t.Errorf("section args = %#v", a)
	}

	h.calls, h.cells = nil, []string{`9`}
	code, _ = post(t, "/admin/maps/{id}/sections", "3", editor, `{"kind":"zone","name":"Standing","capacity":80}`)
	if code != 200 || h.calls[0].args[6] != `[]` || h.calls[0].args[7] != 80 {
		t.Errorf("zone: %d %#v", code, h.calls[0].args)
	}

	h.calls, h.cells = nil, []string{`{"ok":true,"n":2}`}
	code, body = post(t, "/admin/sections/{id}/seats", "12", editor, `{"seats":[4,5],"blocked":true}`)
	if code != 200 || body["changed"] != float64(2) || h.calls[0].args[1] != `[4,5]` {
		t.Errorf("block: %d %v %#v", code, body, h.calls[0].args)
	}

	h.calls, h.affected = nil, []int64{1}
	if code, _ = post(t, "/admin/maps/{id}/delete", "3", editor, ``); code != 200 {
		t.Errorf("delete map: %d", code)
	}
	h.affected = []int64{1}
	if code, _ = post(t, "/admin/sections/{id}/delete", "12", editor, ``); code != 200 {
		t.Errorf("delete section: %d", code)
	}

	h.calls, h.cells = nil, []string{`[{"id":3,"name":"Main Hall","seats":60,"sections":[]}]`}
	if code, body = get(t, "/admin/maps", "", editor, nil); code != 200 || len(body["maps"].([]any)) != 1 {
		t.Errorf("list: %d %v", code, body)
	}
	h.cells = []string{`{"id":3,"name":"Main Hall","seats":2,"sections":[{"id":12,"map_id":3,"kind":"grid","name":"S","tier":"","position":0,"rows":["A"],"seats_per_row":2,"capacity":0}]}`,
		`[{"s":12,"c":[[1,0,"A",1,false],[2,0,"A",2,true]]}]`}
	code, body = get(t, "/admin/maps/{id}", "3", editor, nil)
	if seats, _ := body["seats"].([]any); code != 200 || len(seats) != 1 || len(seats[0].([]any)) != 2 {
		t.Errorf("read: %d %v", code, body)
	}
}

func TestSeatingRefusals(t *testing.T) {
	h := newSeatHost(t)
	// Section save refused by a guard on an existing section.
	h.cells = []string{`0`, `true`}
	code, body := post(t, "/admin/maps/{id}/sections", "3", editor, `{"id":12,"kind":"grid","name":"S","rows":"A-B","seats_per_row":5}`)
	if code != 409 || body["error"] != "This section has sold seats; add a new section instead" {
		t.Errorf("sold seats: %d %v", code, body)
	}
	// Delete of a bound section.
	h.affected, h.cells = []int64{0}, []string{`true`}
	if code, body = post(t, "/admin/sections/{id}/delete", "12", editor, ``); code != 409 || body["error"] != "In use by ticket types" {
		t.Errorf("in use: %d %v", code, body)
	}
	// Missing map.
	h.cells = []string{`{}`}
	if code, _ = get(t, "/admin/maps/{id}", "99", editor, nil); code != 404 {
		t.Errorf("missing map: %d", code)
	}
	// Host failure: generic text, no host error text.
	callHost = func(string, any, any) error { return errTest("pq: secret relation detail") }
	code, body = post(t, "/admin/maps", "", editor, `{"name":"x"}`)
	if code != 503 || strings.Contains(body["error"].(string), "secret") {
		t.Errorf("db failure: %d %v", code, body)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func sdkRequest(method, pattern, id string, user *sdk.HTTPUser, body string) sdk.HTTPRequest {
	return sdk.HTTPRequest{Method: method, Pattern: pattern, PathValues: map[string]string{"id": id}, User: user, Body: body}
}
