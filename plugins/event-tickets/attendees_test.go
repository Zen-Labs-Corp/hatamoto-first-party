package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// attendeesHost fakes the attendee reads and the guarded statements of the
// admin routes; anything else (the mail reads, settings, the key) goes to
// the mail fake it wraps.
type attendeesHost struct {
	*mailHost
	list      string // attendee page, as the jsonb cell
	waiting   string
	rows      int    // attendees the export finds
	fields    string // custom fields of the event
	resendID  string // answer of the resend guard: an order id or null
	cancelTyp string // answer of the cancel statement: a type id or null
	promote   []string
	sql       []call
}

func newAttendeesHost(t *testing.T) *attendeesHost {
	h := &attendeesHost{mailHost: newMailHost(t), list: `[]`, waiting: `[]`, fields: `[]`, resendID: `null`, cancelTyp: `null`}
	next := callHost
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		switch {
		case strings.Contains(sql, "FROM attendees a JOIN orders o") && strings.Contains(sql, "a.id > $2"):
			h.sql = append(h.sql, call{fn, sql, ar})
			after := int(ar[1].(int64))
			var rows []string
			for id := after + 1; id <= h.rows && len(rows) < csvBatch; id++ {
				rows = append(rows, h.csvRow(id))
			}
			return cell("[" + strings.Join(rows, ",") + "]")
		case strings.Contains(sql, "FROM attendees a JOIN orders o"):
			h.sql = append(h.sql, call{fn, sql, ar})
			return cell(h.list)
		case strings.Contains(sql, "FROM waitlist w JOIN ticket_types"):
			return cell(h.waiting)
		case strings.Contains(sql, "FROM fields"):
			return cell(h.fields)
		case strings.HasPrefix(sql, "WITH u AS (UPDATE orders SET resent_at"):
			h.sql = append(h.sql, call{fn, sql, ar})
			return cell(h.resendID)
		case strings.HasPrefix(sql, "WITH a AS (UPDATE attendees SET status = 'cancelled'"):
			h.sql = append(h.sql, call{fn, sql, ar})
			typ := h.cancelTyp
			h.cancelTyp = `null` // a second cancel finds nothing active
			return cell(typ)
		case strings.HasPrefix(sql, "WITH w AS (SELECT id FROM waitlist"):
			h.sql = append(h.sql, call{fn, sql, ar})
			if len(h.promote) == 0 {
				return cell(`null`)
			}
			id := h.promote[0]
			h.promote = h.promote[1:]
			return cell(id)
		}
		return next(fn, args, out)
	}
	return h
}

func (h *attendeesHost) csvRow(id int) string {
	return fmt.Sprintf(`{"attendee_id":%d,"order_id":%d,"ticket_type":"Standard","name":"Ana %d","email":"ana@example.com","status":"active","booked_at":"2027-01-01T12:00:00+00:00","code":"c%d","seat":"Stalls A-%d","answers":{}}`, id, id, id, id, id)
}

func (h *attendeesHost) saw(prefix string) int {
	n := 0
	for _, c := range h.sql {
		if strings.HasPrefix(c.sql, prefix) {
			n++
		}
	}
	return n
}

func csvRequest(t *testing.T, user *sdk.HTTPUser, id string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "GET", Pattern: "/admin/events/{id}/attendees.csv", PathValues: map[string]string{"id": id}, User: user})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatal(err)
	}
	var resp sdk.HTTPResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAttendeeRoutesRefuseNoUser(t *testing.T) {
	h := newAttendeesHost(t)
	for _, c := range []struct{ method, pattern string }{
		{"GET", "/admin/events/{id}/attendees"}, {"GET", "/admin/events/{id}/attendees.csv"},
		{"POST", "/admin/attendees/{id}/resend"}, {"POST", "/admin/attendees/{id}/cancel"},
	} {
		code, _ := request(t, sdk.HTTPRequest{Method: c.method, Pattern: c.pattern, PathValues: map[string]string{"id": "5"}})
		if code != 403 {
			t.Errorf("%s %s without a user = %d, want 403", c.method, c.pattern, code)
		}
	}
	if len(h.sql) != 0 || len(h.mails) != 0 {
		t.Errorf("nothing may run without a user: %+v %d", h.sql, len(h.mails))
	}
}

func TestAttendeeBadIDs(t *testing.T) {
	h := newAttendeesHost(t)
	for _, pattern := range []string{"/admin/attendees/{id}/resend", "/admin/attendees/{id}/cancel"} {
		for _, id := range []string{"0", "-3", "+4", "abc", "1.5", "", "9999999999999999999"} {
			if code, _ := post(t, pattern, id, editor, ""); code != 400 {
				t.Errorf("%s id %q = %d, want 400", pattern, id, code)
			}
		}
	}
	for _, id := range []string{"", "a b", "x;y"} {
		if code, _ := get(t, "/admin/events/{id}/attendees", id, editor, nil); code != 400 {
			t.Errorf("list id %q = %d, want 400", id, code)
		}
	}
	if len(h.sql) != 0 {
		t.Errorf("bad ids reach no statement: %+v", h.sql)
	}
}

func TestAttendeeList(t *testing.T) {
	h := newAttendeesHost(t)
	h.list = `[{"id":1,"order_id":3,"name":"(removed)","email":"","type_name":"Standard","status":"active","created_at":"2027-01-01T12:00:00+00:00","mailed_at":null,"answers":{}}]`
	h.waiting = `[{"id":9,"name":"Bo","email":"bo@example.com","type_name":"VIP","status":"waiting","created_at":"2027-01-02T12:00:00+00:00"}]`
	code, body := get(t, "/admin/events/{id}/attendees", "e1", editor, map[string][]string{"page": {"3"}})
	if code != 200 || body["has_more"] != false || body["page"] != float64(3) {
		t.Fatalf("%d %+v", code, body)
	}
	if a := body["attendees"].([]any); len(a) != 1 || a[0].(map[string]any)["name"] != "(removed)" {
		t.Errorf("attendees = %+v", body["attendees"])
	}
	if w := body["waitlist"].([]any); len(w) != 1 || w[0].(map[string]any)["email"] != "bo@example.com" {
		t.Errorf("waitlist = %+v", body["waitlist"])
	}
	c := h.sql[0]
	if !strings.Contains(c.sql, "t.name || coalesce(', ' || a.seat_label, '') AS type_name") {
		t.Errorf("the list prints the type without its seat: %s", c.sql)
	}
	if c.args[0] != "e1" || fmt.Sprint(c.args[1], c.args[2]) != "101 200" {
		t.Errorf("page 3 reads 100 rows after 200: %v", c.args)
	}
	if !strings.Contains(c.sql, "anonymised_at IS NULL THEN o.name ELSE '(removed)'") {
		t.Error("anonymised buyers show as (removed)")
	}
}

func TestAttendeeListHasMore(t *testing.T) {
	h := newAttendeesHost(t)
	var rows []string
	for i := 1; i <= 101; i++ {
		rows = append(rows, fmt.Sprintf(`{"id":%d}`, i))
	}
	h.list = "[" + strings.Join(rows, ",") + "]"
	_, body := get(t, "/admin/events/{id}/attendees", "e1", editor, nil)
	if body["has_more"] != true || len(body["attendees"].([]any)) != 100 {
		t.Errorf("has_more %v, %d rows", body["has_more"], len(body["attendees"].([]any)))
	}
}

func TestAttendeeListDBError(t *testing.T) {
	newAttendeesHost(t)
	prev := callHost
	callHost = func(fn string, args, out any) error {
		if fn == sdk.FnDBQuery {
			return errors.New("pq: secret detail")
		}
		return prev(fn, args, out)
	}
	code, body := get(t, "/admin/events/{id}/attendees", "e1", editor, nil)
	if code != 503 || strings.Contains(fmt.Sprint(body), "secret") {
		t.Errorf("%d %+v", code, body)
	}
}

func TestCSVColumnsAndFormulas(t *testing.T) {
	h := newAttendeesHost(t)
	h.fields = `[{"key":"diet","label":"Diet","kind":"select","options":["none"],"required":false},
{"key":"terms","label":"=SUM(A1)","kind":"checkbox","required":true}]`
	rows, n, err := attendeesCSV([]field{{Key: "diet", Label: "Diet"}, {Key: "terms", Label: "=SUM(A1)"}, {Key: "z", Label: "@x"}},
		[]csvRow{{AttendeeID: 1, OrderID: 2, TicketType: "-VIP", Name: "=SUM(A1)", Email: "a@example.com", Status: "active",
			Code: "abc", Seat: "=Stalls A-12", Answers: map[string]string{"diet": "+cmd", "terms": "yes"}}}, maxCSVBytes)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	lines := strings.Split(strings.TrimSpace(rows), "\n")
	if lines[0] != "attendee_id,order_id,ticket_type,name,email,status,booked_at,ticket_code,seat,Diet,'=SUM(A1),'@x" {
		t.Errorf("header = %s", lines[0])
	}
	if lines[1] != "1,2,'-VIP,'=SUM(A1),a@example.com,active,0001-01-01T00:00:00Z,abc,'=Stalls A-12,'+cmd,yes," {
		t.Errorf("row = %s", lines[1])
	}

	// through the route: columns in position order, a formula in a name
	h.rows = 1
	resp := csvRequest(t, editor, "e1")
	if resp.Status != 200 || resp.Headers["Content-Type"] != "text/csv; charset=utf-8" || resp.Headers["Content-Disposition"] != `attachment; filename="attendees.csv"` {
		t.Fatalf("%d %+v", resp.Status, resp.Headers)
	}
	if got := strings.Split(resp.Body, "\n")[0]; got != "attendee_id,order_id,ticket_type,name,email,status,booked_at,ticket_code,seat,Diet,'=SUM(A1)" {
		t.Errorf("header = %s", got)
	}
}

func TestCSVNeutralisesNameAndAnswerFromTheDatabase(t *testing.T) {
	h := newAttendeesHost(t)
	h.fields = `[{"key":"q","label":"Q","kind":"text","options":[],"required":false}]`
	prev := callHost
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		if sql, _ := a["sql"].(string); strings.Contains(sql, "a.id > $2") {
			return jsonInto(`{"rows":[[[{"attendee_id":1,"order_id":1,"ticket_type":"T","name":"=SUM(A1)","email":"x@example.com","status":"active","booked_at":"2027-01-01T00:00:00Z","code":"c","answers":{"q":"=HYPERLINK(1)"}}]]]}`, out)
		}
		return prev(fn, args, out)
	}
	resp := csvRequest(t, editor, "e1")
	if !strings.Contains(resp.Body, "'=SUM(A1)") || !strings.Contains(resp.Body, "'=HYPERLINK(1)") {
		t.Errorf("body = %s", resp.Body)
	}
}

func TestCSVHeaderOnlyWithoutAttendees(t *testing.T) {
	h := newAttendeesHost(t)
	h.fields = `[{"key":"diet","label":"Diet","kind":"text","options":[],"required":false}]`
	resp := csvRequest(t, editor, "e1")
	if resp.Status != 200 || strings.TrimSpace(resp.Body) != "attendee_id,order_id,ticket_type,name,email,status,booked_at,ticket_code,seat,Diet" {
		t.Errorf("%d %q", resp.Status, resp.Body)
	}
}

func TestCSVTooManyRows(t *testing.T) {
	h := newAttendeesHost(t)
	h.rows = maxCSVRows + 1
	resp := csvRequest(t, editor, "e1")
	if resp.Status != 413 || strings.Contains(resp.Body, "attendee_id") {
		t.Errorf("%d %s", resp.Status, resp.Body)
	}
}

func TestCSVByteCap(t *testing.T) {
	_, n, _ := attendeesCSV(nil, []csvRow{{Name: "a"}, {Name: "b"}, {Name: "c"}}, 140)
	if n >= 3 {
		t.Errorf("the writer stops at the byte cap: wrote %d", n)
	}
}

func TestResendGuard(t *testing.T) {
	h := newAttendeesHost(t) // the guard finds no order to resend
	code, body := post(t, "/admin/attendees/{id}/resend", "12", editor, "")
	if code != 429 || body["error"] != "resent recently; try again in a few minutes" {
		t.Errorf("%d %+v", code, body)
	}
	if len(h.mails) != 0 || h.wrote("INSERT INTO mail_queue") != 0 {
		t.Error("a refused resend sends nothing")
	}
	sql := h.sql[0].sql
	for _, want := range []string{"resend_count < $2", "interval '10 minutes'", "anonymised_at IS NULL"} {
		if !strings.Contains(sql, want) {
			t.Errorf("guard lacks %q", want)
		}
	}
	if h.sql[0].args[0] != int64(12) || h.sql[0].args[1] != 5 {
		t.Errorf("args = %v", h.sql[0].args)
	}
}

func TestResendSendsOneMail(t *testing.T) {
	h := newAttendeesHost(t)
	h.resendID = `41`
	code, body := post(t, "/admin/attendees/{id}/resend", "12", editor, "")
	if code != 200 || body["ok"] != true || body["queued"] != false {
		t.Fatalf("%d %+v", code, body)
	}
	if len(h.mails) != 1 || h.wrote("UPDATE orders SET mailed_at") != 1 {
		t.Errorf("mails %d, mailed_at writes %d", len(h.mails), h.wrote("UPDATE orders SET mailed_at"))
	}
}

func TestResendQueuesWhenMailFails(t *testing.T) {
	h := newAttendeesHost(t)
	h.resendID = `41`
	h.mailErrs = []error{&sdk.HostError{Code: "rate_limited"}}
	code, body := post(t, "/admin/attendees/{id}/resend", "12", editor, "")
	if code != 200 || body["queued"] != true || h.wrote("INSERT INTO mail_queue") != 1 {
		t.Errorf("%d %+v, queue writes %d", code, body, h.wrote("INSERT INTO mail_queue"))
	}
}

func TestCancelPromotes(t *testing.T) {
	h := newAttendeesHost(t)
	h.cancelTyp = `6`
	h.promote = []string{`3`}
	code, body := post(t, "/admin/attendees/{id}/cancel", "12", editor, "")
	if code != 200 || body["ok"] != true || body["promoted"] != float64(1) {
		t.Fatalf("%d %+v", code, body)
	}
	if h.saw("WITH w AS (SELECT id FROM waitlist") < 1 {
		t.Error("the freed seat is offered to the waitlist")
	}
	for _, m := range h.mails {
		if m["to"] != "bo@example.com" && strings.Contains(fmt.Sprint(m["subject"]), "cancel") {
			t.Errorf("the attendee gets no cancel mail: %v", m)
		}
	}
}

func TestCancelTwiceIs409(t *testing.T) {
	h := newAttendeesHost(t)
	h.cancelTyp = `6`
	if code, _ := post(t, "/admin/attendees/{id}/cancel", "12", editor, ""); code != 200 {
		t.Fatalf("first cancel = %d", code)
	}
	code, body := post(t, "/admin/attendees/{id}/cancel", "12", editor, "")
	if code != 409 || body["ok"] != false {
		t.Errorf("%d %+v", code, body)
	}
}

// The export keeps ticket_type the bare type and puts the seat in its own column.
func TestCSVSeatColumn(t *testing.T) {
	h := newAttendeesHost(t)
	h.rows = 2
	resp := csvRequest(t, editor, "e1")
	lines := strings.Split(strings.TrimSpace(resp.Body), "\n")
	if resp.Status != 200 || len(lines) != 3 || !strings.HasSuffix(lines[0], ",ticket_code,seat") || !strings.HasSuffix(lines[2], ",c2,Stalls A-2") {
		t.Fatalf("%d %q", resp.Status, resp.Body)
	}
	var sql string
	for _, c := range h.sql {
		if strings.Contains(c.sql, "a.id > $2") {
			sql = c.sql
		}
	}
	if !strings.Contains(sql, "coalesce(a.seat_label, '') AS seat") || !strings.Contains(sql, "t.name AS ticket_type") {
		t.Errorf("export SQL: %s", sql)
	}
}
