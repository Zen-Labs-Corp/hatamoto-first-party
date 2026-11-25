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

// rsvpHost fakes everything the public routes touch and records the writes.
type rsvpHost struct {
	entity    string
	types     string
	fields    string
	counts    string
	soldOut   map[int64]bool // types that cannot give their seats
	dupe      bool           // the waitlist insert hits the unique index
	dbErr     error
	key       string
	clock     time.Time
	writes    []call
	mails     []map[string]any
	nextOrder int64
}

const (
	oneType  = `[{"id":5,"event_id":"e1","name":"Standard","capacity":10,"taken":4,"waitlist":true,"on_sale":true,"position":0}]`
	twoTypes = `[{"id":5,"event_id":"e1","name":"Standard","capacity":10,"taken":4,"waitlist":true,"on_sale":true,"position":0},
{"id":6,"event_id":"e1","name":"VIP","capacity":2,"taken":2,"waitlist":true,"on_sale":true,"position":1},
{"id":7,"event_id":"e1","name":"Closed","capacity":9,"taken":0,"waitlist":false,"on_sale":false,"position":2}]`
)

func newRSVPHost(t *testing.T) *rsvpHost {
	h := &rsvpHost{entity: gigEntity, types: twoTypes, fields: `[]`, counts: `{"by_email":0,"hour":0,"waiting":0}`,
		soldOut: map[int64]bool{}, clock: time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), nextOrder: 41}
	now = func() time.Time { return h.clock }
	t.Cleanup(func() { now = time.Now })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		write := func() { h.writes = append(h.writes, call{fn, sql, ar}) }
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{}`, out)
		case fn == sdk.FnEntitiesGet:
			return jsonInto(h.entity, out)
		case fn == sdk.FnMailSend:
			h.mails = append(h.mails, a)
			return nil
		case h.dbErr != nil && !strings.Contains(sql, "meta"):
			return h.dbErr
		case strings.HasPrefix(sql, "INSERT INTO meta"):
			if h.key == "" {
				h.key = ar[1].(string)
			}
			return nil
		case strings.Contains(sql, "FROM meta"):
			return cell(`"` + h.key + `"`)
		case strings.Contains(sql, "FROM ticket_types WHERE event_id"):
			return cell(h.types)
		case strings.Contains(sql, "FROM fields"):
			return cell(h.fields)
		case strings.Contains(sql, "'by_email'"):
			return cell(h.counts)
		case strings.Contains(sql, "INSERT INTO orders"):
			write()
			if h.soldOut[ar[1].(int64)] {
				return cell(`null`)
			}
			h.nextOrder++
			return cell(jsonText(h.nextOrder))
		case strings.Contains(sql, "SELECT to_jsonb(count(*)) FROM a"):
			write()
			if h.soldOut[ar[1].(int64)] {
				return cell(`0`)
			}
			return cell(jsonText(ar[2]))
		case strings.HasPrefix(sql, "INSERT INTO waitlist"), strings.Contains(sql, "INSERT INTO waitlist"):
			write()
			if h.dupe {
				return cell(`null`)
			}
			return cell(`9`)
		case strings.Contains(sql, "FROM orders o WHERE"):
			return cell(twoTickets)
		case strings.Contains(sql, "FROM waitlist w JOIN"):
			return cell(`{"event_id":"e1","name":"Bo","email":"bo@example.com","code":"0123456789abcdef0123456789abcdef","status":"waiting","type_name":"VIP","expires_at":""}`)
		case strings.HasPrefix(sql, "WITH c AS"), strings.HasPrefix(sql, "DELETE FROM orders"),
			strings.HasPrefix(sql, "UPDATE orders SET mailed_at"), strings.HasPrefix(sql, "INSERT INTO mail_queue"):
			write()
			return nil
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *rsvpHost) wrote(substr string) []call {
	var out []call
	for _, c := range h.writes {
		if strings.Contains(c.sql, substr) {
			out = append(out, c)
		}
	}
	return out
}

// issue gets a token from /token and lets enough time pass for it to count.
func (h *rsvpHost) issue(t *testing.T) string {
	t.Helper()
	r := publicGet(t, "/token", nil, map[string][]string{"event": {"e1"}})
	if r.Status != 200 {
		t.Fatalf("token: %d %s", r.Status, r.Body)
	}
	var b struct{ Token string }
	_ = json.Unmarshal([]byte(r.Body), &b)
	h.clock = h.clock.Add(10 * time.Second)
	return b.Token
}

func rsvp(t *testing.T, contentType, body string) (int, map[string]any) {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "POST", Pattern: "/rsvp", Body: body,
		Headers: map[string]string{"content-type": contentType}})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var resp sdk.HTTPResponse
	_ = json.Unmarshal(out, &resp)
	var m map[string]any
	_ = json.Unmarshal([]byte(resp.Body), &m)
	return resp.Status, m
}

func rsvpJSON(t *testing.T, h *rsvpHost, fields map[string]any) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"event": "e1", "name": "Ana", "email": "ana@example.com", "token": h.issue(t)}
	for k, v := range fields {
		body[k] = v
	}
	return rsvp(t, "application/json", jsonText(body))
}

func items(parts ...int) []map[string]int {
	var out []map[string]int
	for i := 0; i+1 < len(parts); i += 2 {
		out = append(out, map[string]int{"type": parts[i], "qty": parts[i+1]})
	}
	return out
}

func TestTokenRoute(t *testing.T) {
	h := newRSVPHost(t)
	h.fields = `[{"key":"diet","label":"Diet","kind":"select","options":["none","veg"],"required":true}]`
	r := publicGet(t, "/token", nil, map[string][]string{"event": {"e1"}})
	var b struct {
		Token  string
		Types  []tokenType
		Fields []field
	}
	if err := json.Unmarshal([]byte(r.Body), &b); err != nil || r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if r.Headers["Cache-Control"] != "no-store" || b.Token == "" {
		t.Errorf("%v %q", r.Headers, b.Token)
	}
	if len(b.Types) != 2 || b.Types[0].Left != 6 || b.Types[0].SoldOut || !b.Types[1].SoldOut || !b.Types[1].Waitlist {
		t.Errorf("types (only the ones on sale, with live counts): %+v", b.Types)
	}
	if len(b.Fields) != 1 || b.Fields[0].Key != "diet" {
		t.Errorf("fields %+v", b.Fields)
	}
	h.types = `[{"id":7,"event_id":"e1","name":"Closed","capacity":9,"taken":0,"waitlist":false,"on_sale":false,"position":0}]`
	if r := publicGet(t, "/token", nil, map[string][]string{"event": {"e1"}}); r.Status != 404 {
		t.Errorf("no type on sale: %d", r.Status)
	}
	if r := publicGet(t, "/token", nil, map[string][]string{"event": {"a b"}}); r.Status != 404 {
		t.Errorf("bad id: %d", r.Status)
	}
	h.dbErr = errors.New("pq: secret detail")
	if r := publicGet(t, "/token", nil, map[string][]string{"event": {"e1"}}); r.Status != 503 || strings.Contains(r.Body, "secret") {
		t.Errorf("db error: %d %s", r.Status, r.Body)
	}
}

func TestRSVPReservesInOneStatement(t *testing.T) {
	h := newRSVPHost(t)
	code, body := rsvpJSON(t, h, map[string]any{"items": items(5, 2)})
	if code != 200 || body["ok"] != true {
		t.Fatalf("%d %v", code, body)
	}
	tickets, _ := body["tickets"].([]any)
	if len(tickets) != 2 || !strings.HasPrefix(tickets[0].(string), "/p/event-tickets/ticket/") {
		t.Errorf("tickets %v", tickets)
	}
	w := h.wrote("INSERT INTO orders")
	if len(w) != 1 {
		t.Fatalf("%d reservations", len(w))
	}
	for _, want := range []string{"UPDATE ticket_types SET taken = taken + $3", "taken + $3 <= capacity", "on_sale", "INSERT INTO attendees"} {
		if !strings.Contains(w[0].sql, want) {
			t.Errorf("the reservation statement lacks %q", want)
		}
	}
	if len(w[0].args) < 3 || w[0].args[2] != 2 {
		t.Errorf("args %v", w[0].args)
	}
	for _, c := range h.writes {
		if strings.Contains(c.sql, "Ana") || strings.Contains(c.sql, "ana@example.com") {
			t.Error("input must travel as arguments only")
		}
	}
	if len(h.mails) != 1 {
		t.Errorf("%d mails, want the confirmation", len(h.mails))
	}
	if len(h.wrote("WITH c AS")) != 0 {
		t.Error("nothing to compensate")
	}
}

func TestRSVPSoldOutStoresNothing(t *testing.T) {
	h := newRSVPHost(t)
	h.soldOut[5] = true
	code, body := rsvpJSON(t, h, map[string]any{"items": items(5, 3)})
	if code != 409 || body["ok"] != false {
		t.Fatalf("%d %v", code, body)
	}
	if len(h.mails) != 0 || len(h.wrote("WITH c AS")) != 0 || len(h.wrote("DELETE FROM orders")) != 0 {
		t.Error("a refused order stores and sends nothing")
	}
}

func TestRSVPMultiTypeCompensates(t *testing.T) {
	h := newRSVPHost(t)
	h.soldOut[6] = true
	code, body := rsvpJSON(t, h, map[string]any{"items": items(5, 1, 6, 1)})
	if code != 409 {
		t.Fatalf("%d %v", code, body)
	}
	if ids, _ := body["sold_out"].([]any); len(ids) != 1 || ids[0] != float64(6) {
		t.Errorf("sold_out %v", body["sold_out"])
	}
	comp := h.wrote("WITH c AS")
	del := h.wrote("DELETE FROM orders")
	if len(comp) != 1 || len(del) != 1 || comp[0].args[0] != int64(42) || del[0].args[0] != int64(42) {
		t.Fatalf("compensation %+v, delete %+v", comp, del)
	}
	if !strings.Contains(comp[0].sql, "taken = taken - x.n") {
		t.Error("the seats go back")
	}
	if len(h.mails) != 0 {
		t.Error("no mail for an order that was undone")
	}
}

func TestRSVPSilentRefusals(t *testing.T) {
	h := newRSVPHost(t)
	good := h.issue(t)
	base := func(m map[string]any) string {
		b := map[string]any{"event": "e1", "name": "Ana", "email": "ana@example.com", "items": items(5, 1), "token": good}
		for k, v := range m {
			b[k] = v
		}
		return jsonText(b)
	}
	young := func() string {
		r := publicGet(t, "/token", nil, map[string][]string{"event": {"e1"}})
		var b struct{ Token string }
		_ = json.Unmarshal([]byte(r.Body), &b)
		return b.Token // not aged yet
	}()
	h.clock = h.clock.Add(2 * time.Hour)
	for name, body := range map[string]string{
		"honeypot": base(map[string]any{"website": "http://x.example"}),
		"young":    base(map[string]any{"token": young}),
		"old":      base(nil), // good is now two hours old
		"forged":   base(map[string]any{"token": good[:len(good)-1] + "0"}),
		"no token": base(map[string]any{"token": ""}),
	} {
		code, m := rsvp(t, "application/json", body)
		if code != 200 || m["ok"] != true || m["tickets"] != nil {
			t.Errorf("%s: %d %v", name, code, m)
		}
	}
	if len(h.writes) != 0 || len(h.mails) != 0 {
		t.Errorf("silent refusals write nothing: %+v", h.writes)
	}
}

func TestRSVPMalformed(t *testing.T) {
	h := newRSVPHost(t)
	tok := h.issue(t)
	ok := `{"event":"e1","name":"A","email":"a@example.com","items":[{"type":5,"qty":1}],"token":"` + tok + `"`
	for name, c := range map[string]struct {
		ctype, body string
		want        int
	}{
		"too large":   {"application/json", ok + `,"pad":"` + strings.Repeat("a", 16<<10) + `"}`, 413},
		"text/plain":  {"text/plain", ok + `}`, 415},
		"form":        {"application/x-www-form-urlencoded", "name=a", 415},
		"unknown key": {"application/json", ok + `,"admin":true}`, 400},
		"trailing":    {"application/json", ok + `} {}`, 400},
		"not json":    {"application/json", `nope`, 400},
	} {
		if code, _ := rsvp(t, c.ctype, c.body); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	if len(h.writes) != 0 {
		t.Error("malformed posts store nothing")
	}
}

func TestRSVPValidation(t *testing.T) {
	h := newRSVPHost(t)
	h.fields = `[{"key":"diet","label":"Diet","kind":"select","options":["none","veg"],"required":true},
{"key":"note","label":"Note","kind":"text","options":[],"required":false},
{"key":"ok","label":"OK","kind":"checkbox","options":[],"required":false}]`
	good := map[string]any{"diet": "veg"}
	for name, f := range map[string]map[string]any{
		"unknown answer key": {"items": items(5, 1), "answers": map[string]any{"diet": "veg", "zzz": "x"}},
		"missing required":   {"items": items(5, 1), "answers": map[string]any{}},
		"bad option":         {"items": items(5, 1), "answers": map[string]any{"diet": "meat"}},
		"bad checkbox":       {"items": items(5, 1), "answers": map[string]any{"diet": "veg", "ok": "maybe"}},
		"long answer":        {"items": items(5, 1), "answers": map[string]any{"diet": "veg", "note": strings.Repeat("é", 201)}},
		"no items":           {"answers": good},
		"both":               {"items": items(5, 1), "waitlist_type": 6, "answers": good},
		"zero qty":           {"items": items(5, 0), "answers": good},
		"eleven tickets":     {"items": items(5, 11), "answers": good},
		"repeated type":      {"items": items(5, 1, 5, 1), "answers": good},
		"foreign type":       {"items": items(99, 1), "answers": good},
		"closed type":        {"items": items(7, 1), "answers": good},
		"empty name":         {"items": items(5, 1), "answers": good, "name": "  "},
		"long name":          {"items": items(5, 1), "answers": good, "name": strings.Repeat("a", 121)},
		"bad email":          {"items": items(5, 1), "answers": good, "email": "Ana <ana@example.com>"},
		"bad event":          {"items": items(5, 1), "answers": good, "event": "a b"},
	} {
		if code, _ := rsvpJSON(t, h, f); code != 422 {
			t.Errorf("%s: %d, want 422", name, code)
		}
	}
	if len(h.wrote("INSERT INTO orders")) != 0 {
		t.Error("an invalid post reserves nothing")
	}
	if code, body := rsvpJSON(t, h, map[string]any{"items": items(5, 1), "answers": map[string]any{"diet": "veg", "note": "hi", "ok": "yes"}}); code != 200 {
		t.Errorf("valid answers: %d %v", code, body)
	}
	w := h.wrote("INSERT INTO orders")
	if len(w) != 1 || w[0].args[5] != `{"diet":"veg","note":"hi","ok":"yes"}` {
		t.Errorf("stored answers %+v", w)
	}
}

func TestRSVPCaps(t *testing.T) {
	h := newRSVPHost(t)
	h.counts = `{"by_email":3,"hour":0,"waiting":0}`
	if code, _ := rsvpJSON(t, h, map[string]any{"items": items(5, 1)}); code != 429 {
		t.Errorf("per-email cap: %d", code)
	}
	h.counts = `{"by_email":0,"hour":200,"waiting":0}`
	if code, _ := rsvpJSON(t, h, map[string]any{"items": items(5, 1)}); code != 429 {
		t.Errorf("per-hour cap: %d", code)
	}
	h.counts = `{"by_email":0,"hour":0,"waiting":500}`
	if code, _ := rsvpJSON(t, h, map[string]any{"waitlist_type": 6}); code != 429 {
		t.Errorf("waiting cap: %d", code)
	}
	if len(h.writes) != 0 {
		t.Error("a capped post stores nothing")
	}
}

func TestRSVPClosedEvent(t *testing.T) {
	h := newRSVPHost(t)
	for name, entity := range map[string]string{
		"cancelled": strings.Replace(gigEntity, `"attendance_mode"`, `"event_status":"cancelled","attendance_mode"`, 1),
		"draft":     strings.Replace(gigEntity, `"status":"published"`, `"status":"draft"`, 1),
		"over":      strings.Replace(strings.Replace(gigEntity, "2027-03-01", "2026-03-01", 2), "end_at", "end_at", 1),
	} {
		h.entity = entity
		code, body := rsvpJSON(t, h, map[string]any{"items": items(5, 1)})
		if code != 404 || body["closed"] != true {
			t.Errorf("%s: %d %v", name, code, body)
		}
	}
	if len(h.writes) != 0 {
		t.Error("a closed event stores nothing")
	}
}

func TestRSVPWaitlist(t *testing.T) {
	h := newRSVPHost(t)
	code, body := rsvpJSON(t, h, map[string]any{"waitlist_type": 6})
	if code != 200 || body["waitlisted"] != true {
		t.Fatalf("%d %v", code, body)
	}
	if len(h.wrote("INSERT INTO waitlist")) != 1 || len(h.mails) != 1 {
		t.Errorf("%d inserts, %d mails", len(h.wrote("INSERT INTO waitlist")), len(h.mails))
	}
	if !strings.Contains(h.wrote("INSERT INTO waitlist")[0].sql, "ON CONFLICT DO NOTHING") {
		t.Error("a second entry of the same person is ignored by the index")
	}
	h.dupe = true
	code, body = rsvpJSON(t, h, map[string]any{"waitlist_type": 6})
	if code != 200 || body["waitlisted"] != true || len(h.mails) != 1 {
		t.Errorf("duplicate: %d %v, %d mails", code, body, len(h.mails))
	}
	if code, _ := rsvpJSON(t, h, map[string]any{"waitlist_type": 5}); code != 409 {
		t.Errorf("a type with seats left has no waitlist: %d", code)
	}
	if code, _ := rsvpJSON(t, h, map[string]any{"waitlist_type": 7}); code != 409 {
		t.Errorf("a closed type has no waitlist: %d", code)
	}
}

func TestRSVPDBErrorIsGeneric(t *testing.T) {
	h := newRSVPHost(t)
	tok := h.issue(t)
	h.dbErr = errors.New("pq: relation orders secret detail")
	body := jsonText(map[string]any{"event": "e1", "name": "A", "email": "a@example.com", "items": items(5, 1), "token": tok})
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "POST", Pattern: "/rsvp", Body: body, Headers: map[string]string{"content-type": "application/json"}})
	out, _ := guest.Handle(guest.HTTPExport, in)
	var resp sdk.HTTPResponse
	_ = json.Unmarshal(out, &resp)
	if resp.Status != 503 || strings.Contains(resp.Body, "secret") || strings.Contains(resp.Body, "relation") {
		t.Errorf("%d %s", resp.Status, resp.Body)
	}
}
