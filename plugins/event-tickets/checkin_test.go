package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// ckRow is one ticket of the fake: the state the check-in statements read and write.
type ckRow struct {
	id         int64
	code       string
	event      string
	status     string
	name       string
	anonymised bool
	at         *time.Time
	by         *string
	nonce      any
}

// checkinHost fakes the check-in statements on top of the mail fake (the key).
type checkinHost struct {
	*mailHost
	rows     []*ckRow
	updates  int // guarded UPDATEs that reached the database
	selects  int // state reads of a scan that changed nothing
	unchecks int64
	counts   string
	matches  string
	queries  []call
	dbErr    error
}

func newCheckinHost(t *testing.T) *checkinHost {
	h := &checkinHost{mailHost: newMailHost(t), counts: `{"checked":0,"total":0}`, matches: `[]`}
	next := callHost
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		cell := func(v any) error {
			b, _ := json.Marshal(v)
			return jsonInto(`{"rows":[[`+string(b)+`]]}`, out)
		}
		find := func() *ckRow {
			for _, r := range h.rows {
				if strings.Contains(sql, "a.code = $1") && r.code == ar[0] || strings.Contains(sql, "a.id = $1") && r.id == ar[0] {
					return r
				}
			}
			return nil
		}
		view := func(r *ckRow) map[string]any {
			return map[string]any{"id": r.id, "name": r.name, "type_name": "Standard", "checked_in_at": r.at, "checked_in_by": r.by, "checked_in_nonce": r.nonce,
				"event_id": r.event, "status": r.status, "anonymised": r.anonymised}
		}
		switch {
		case strings.HasPrefix(sql, "WITH u AS (UPDATE attendees a SET checked_in_at"):
			h.updates++
			if h.dbErr != nil {
				return h.dbErr
			}
			r := find()
			if r == nil || r.event != ar[1] || r.status != "active" || r.at != nil || r.anonymised {
				return cell(nil)
			}
			now, by := time.Date(2027, 1, 1, 20, 0, h.updates, 0, time.UTC), ar[2].(string)
			r.at, r.by, r.nonce = &now, &by, ar[3]
			return cell(view(r))
		case strings.Contains(sql, "AS anonymised"):
			h.selects++
			if r := find(); r != nil {
				return cell(view(r))
			}
			return cell(nil)
		case strings.HasPrefix(sql, "UPDATE attendees SET checked_in_at = NULL"):
			for _, r := range h.rows {
				if r.id == ar[0] && r.at != nil {
					r.at, r.by, r.nonce = nil, nil, nil
					return jsonInto(`{"rows_affected":1}`, out)
				}
			}
			return jsonInto(`{"rows_affected":0}`, out)
		case strings.Contains(sql, "count(*) FILTER"):
			h.queries = append(h.queries, call{fn, sql, ar})
			return jsonInto(`{"rows":[[`+h.counts+`]]}`, out)
		case strings.Contains(sql, "strpos(lower(o.name)"):
			h.queries = append(h.queries, call{fn, sql, ar})
			return jsonInto(`{"rows":[[`+h.matches+`]]}`, out)
		}
		return next(fn, args, out)
	}
	h.rows = []*ckRow{
		{id: 1, code: "00000000000000000000000000000001", event: "e1", status: "active", name: "Ana"},
		{id: 2, code: "00000000000000000000000000000002", event: "e2", status: "active", name: "Bo"},
		{id: 3, code: "00000000000000000000000000000003", event: "e1", status: "refunded", name: "Cy"},
		{id: 4, code: "00000000000000000000000000000004", event: "e1", status: "cancelled", name: "Di"},
		{id: 5, code: "00000000000000000000000000000005", event: "e1", status: "active", name: "(removed)", anonymised: true},
	}
	if _, err := hmacSecret(); err != nil { // the fake key
		t.Fatal(err)
	}
	return h
}

func (h *checkinHost) token(t *testing.T, kind string, row int) string {
	t.Helper()
	key, err := hmacSecret()
	if err != nil {
		t.Fatal(err)
	}
	return signCode(key, kind, h.rows[row].code)
}

func scan(t *testing.T, eventID, body string) (int, map[string]any) {
	t.Helper()
	return post(t, "/admin/events/{id}/checkin", eventID, editor, body)
}

func scanBody(text string) string {
	b, _ := json.Marshal(map[string]string{"scan": text})
	return string(b)
}

func wantResult(t *testing.T, code int, body map[string]any, result, reason string) {
	t.Helper()
	if code != 200 || body["ok"] != true || body["result"] != result || (reason != "" && body["reason"] != reason) {
		t.Fatalf("want 200 %s/%s, got %d %+v", result, reason, code, body)
	}
}

func TestScanOKThenAlready(t *testing.T) {
	h := newCheckinHost(t)
	tok := h.token(t, kindTicket, 0)
	code, body := scan(t, "e1", scanBody(tok))
	wantResult(t, code, body, "ok", "")
	first := body["attendee"].(map[string]any)
	if first["name"] != "Ana" || first["checked_in_by"] != editor.ID || first["checked_in_at"] == nil || first["type_name"] != "Standard" {
		t.Errorf("attendee = %+v", first)
	}
	code, body = scan(t, "e1", scanBody(tok))
	wantResult(t, code, body, "already", "")
	again := body["attendee"].(map[string]any)
	if again["checked_in_at"] != first["checked_in_at"] || again["checked_in_by"] != first["checked_in_by"] {
		t.Errorf("already must show the original: %+v vs %+v", again, first)
	}
}

func TestScanNonce(t *testing.T) {
	h := newCheckinHost(t)
	tok := h.token(t, kindTicket, 0)
	mine := func(n string) string {
		b, _ := json.Marshal(map[string]string{"scan": tok, "nonce": n})
		return string(b)
	}
	code, body := scan(t, "e1", mine("aaaaaaaa11111111"))
	wantResult(t, code, body, "ok", "")
	if body["confirmed"] != nil {
		t.Error("a first check-in is not 'confirmed'")
	}
	first := body["attendee"].(map[string]any)
	// the retry of the same scan (reply lost): ok, confirmed, same holder data
	code, body = scan(t, "e1", mine("aaaaaaaa11111111"))
	wantResult(t, code, body, "ok", "")
	if body["confirmed"] != true {
		t.Errorf("same nonce must be confirmed: %+v", body)
	}
	if again := body["attendee"].(map[string]any); again["name"] != "Ana" || again["checked_in_at"] != first["checked_in_at"] {
		t.Errorf("confirmed shows the original check-in: %+v", again)
	}
	if _, leaked := body["attendee"].(map[string]any)["checked_in_nonce"]; leaked {
		t.Error("the stored nonce must not be returned")
	}
	// another scan of the ticket: a different nonce, none, are duplicates
	code, body = scan(t, "e1", mine("bbbbbbbb22222222"))
	wantResult(t, code, body, "already", "")
	code, body = scan(t, "e1", scanBody(tok))
	wantResult(t, code, body, "already", "")
	// a check-in without a nonce is never confirmed by an empty nonce
	h.rows[1].event = "e1"
	code, body = scan(t, "e1", `{"attendee_id":2}`)
	wantResult(t, code, body, "ok", "")
	code, body = scan(t, "e1", `{"attendee_id":2}`)
	wantResult(t, code, body, "already", "")
}

func TestScanAcceptsURLAndBareToken(t *testing.T) {
	h := newCheckinHost(t)
	tok := h.token(t, kindTicket, 0)
	code, body := scan(t, "e1", scanBody("https://x.test/p/event-tickets/ticket/"+tok+"?utm=1#top"))
	wantResult(t, code, body, "ok", "")
	code, body = scan(t, "e1", scanBody("  "+tok+" "))
	wantResult(t, code, body, "already", "")
}

func TestScanInvalid(t *testing.T) {
	h := newCheckinHost(t)
	good := h.token(t, kindTicket, 0)
	flipped := good[:len(good)-1] + map[bool]string{true: "1", false: "0"}[good[len(good)-1] == '0']
	key, err := hmacSecret()
	if err != nil {
		t.Fatal(err)
	}
	unknown := signCode(key, kindTicket, "0123456789abcdef0123456789abcdef")
	for name, text := range map[string]string{"forged": flipped, "wait kind": h.token(t, kindWait, 0), "garbage": "garbage", "unknown code": unknown} {
		code, body := scan(t, "e1", scanBody(text))
		wantResult(t, code, body, "refused", "invalid")
		if _, has := body["attendee"]; has {
			t.Errorf("%s: invalid shows no holder: %+v", name, body)
		}
	}
	if h.updates != 1 { // only the unknown code has a good MAC
		t.Errorf("UPDATEs = %d, want 1 (forged tokens never reach the database)", h.updates)
	}
}

func TestScanAnonymisedIsInvalid(t *testing.T) {
	h := newCheckinHost(t)
	code, body := scan(t, "e1", scanBody(h.token(t, kindTicket, 4)))
	wantResult(t, code, body, "refused", "invalid")
	if _, has := body["attendee"]; has {
		t.Errorf("%+v", body)
	}
}

func TestScanOtherEventRevealsNothing(t *testing.T) {
	h := newCheckinHost(t)
	code, body := scan(t, "e1", scanBody(h.token(t, kindTicket, 1)))
	wantResult(t, code, body, "refused", "other_event")
	if _, has := body["attendee"]; has || strings.Contains(fmt.Sprint(body), "Bo") || strings.Contains(fmt.Sprint(body), "e2") {
		t.Errorf("other_event leaks: %+v", body)
	}
	code, body = scan(t, "e1", `{"attendee_id":2}`)
	wantResult(t, code, body, "refused", "other_event")
	if h.rows[1].at != nil {
		t.Error("another event's ticket must stay unchecked")
	}
}

func TestScanNonActiveIsRefusedWithStatus(t *testing.T) {
	h := newCheckinHost(t)
	code, body := scan(t, "e1", scanBody(h.token(t, kindTicket, 2)))
	wantResult(t, code, body, "refused", "refunded")
	code, body = scan(t, "e1", `{"attendee_id":4}`)
	wantResult(t, code, body, "refused", "cancelled")
	if body["attendee"].(map[string]any)["name"] != "Di" {
		t.Errorf("the name of this event's ticket is shown: %+v", body)
	}
}

func TestScanByAttendeeID(t *testing.T) {
	newCheckinHost(t)
	code, body := scan(t, "e1", `{"attendee_id":1}`)
	wantResult(t, code, body, "ok", "")
	code, body = scan(t, "e1", `{"attendee_id":1}`)
	wantResult(t, code, body, "already", "")
	code, body = scan(t, "e1", `{"attendee_id":99}`)
	wantResult(t, code, body, "refused", "invalid")
}

func TestScanBadBodiesAre400(t *testing.T) {
	h := newCheckinHost(t)
	for name, b := range map[string]string{
		"both": `{"scan":"x","attendee_id":1}`, "neither": `{}`, "empty scan": `{"scan":""}`,
		"too long": scanBody(strings.Repeat("a", scanMax+1)), "negative id": `{"attendee_id":-1}`,
		"unknown key": `{"scan":"x","extra":1}`, "short nonce": `{"attendee_id":1,"nonce":"abc"}`,
		"bad nonce chars": `{"attendee_id":1,"nonce":"abcdefgh!?"}`, "long nonce": `{"attendee_id":1,"nonce":"` + strings.Repeat("a", 65) + `"}`, "not json": `nope`,
	} {
		if code, body := scan(t, "e1", b); code != 400 || body["ok"] != false {
			t.Errorf("%s: %d %+v", name, code, body)
		}
	}
	if h.updates != 0 {
		t.Error("a bad body must not reach the database")
	}
	if code, _ := request(t, sdk.HTTPRequest{Method: "POST", Pattern: "/admin/events/{id}/checkin", PathValues: map[string]string{"id": "e1"}, Body: `{"attendee_id":1}`}); code != 403 {
		t.Errorf("no user: %d", code)
	}
}

func TestScanDBErrorIs503(t *testing.T) {
	h := newCheckinHost(t)
	h.dbErr = errors.New("boom: secret detail")
	tok := h.token(t, kindTicket, 0)
	code, body := scan(t, "e1", scanBody(tok))
	if code != 503 || body["ok"] != false || strings.Contains(fmt.Sprint(body), "boom") {
		t.Errorf("%d %+v", code, body)
	}
}

func TestUncheck(t *testing.T) {
	h := newCheckinHost(t)
	if code, body := scan(t, "e1", `{"attendee_id":1}`); code != 200 || body["result"] != "ok" {
		t.Fatalf("%d %+v", code, body)
	}
	if code, body := post(t, "/admin/attendees/{id}/uncheck", "1", editor, ""); code != 200 || body["ok"] != true {
		t.Fatalf("uncheck: %d %+v", code, body)
	}
	if h.rows[0].at != nil || h.rows[0].by != nil || h.rows[0].nonce != nil {
		t.Error("uncheck clears all check-in columns")
	}
	if code, body := post(t, "/admin/attendees/{id}/uncheck", "1", editor, ""); code != 409 || body["ok"] != false {
		t.Errorf("not checked in: %d %+v", code, body)
	}
	if code, _ := post(t, "/admin/attendees/{id}/uncheck", "x", editor, ""); code != 400 {
		t.Errorf("bad id: %d", code)
	}
	// and the ticket can be scanned in again
	if code, body := scan(t, "e1", `{"attendee_id":1}`); code != 200 || body["result"] != "ok" {
		t.Errorf("%d %+v", code, body)
	}
}

func TestCheckinState(t *testing.T) {
	h := newCheckinHost(t)
	h.counts = `{"checked":3,"total":10}`
	h.matches = `[{"id":1,"name":"Ana","type_name":"Standard","status":"active","checked_in_at":null,"checked_in_by":null}]`

	code, body := get(t, "/admin/events/{id}/checkin", "e1", editor, map[string][]string{"q": {"a"}})
	if code != 200 || body["checked"] != float64(3) || body["total"] != float64(10) || len(body["matches"].([]any)) != 0 {
		t.Fatalf("short q: %d %+v", code, body)
	}
	if len(h.queries) != 1 {
		t.Errorf("a query under 2 characters runs no search: %d statements", len(h.queries))
	}

	h.queries = nil
	code, body = get(t, "/admin/events/{id}/checkin", "e1", editor, map[string][]string{"q": {"an"}})
	if code != 200 || len(body["matches"].([]any)) != 1 || len(h.queries) != 2 {
		t.Fatalf("name q: %d %+v", code, body)
	}
	if strings.Contains(h.queries[1].sql, "a.code LIKE") {
		t.Error("a non-hex query does not search codes")
	}

	h.queries = nil
	if code, _ = get(t, "/admin/events/{id}/checkin", "e1", editor, map[string][]string{"q": {"ab12"}}); code != 200 {
		t.Fatal(code)
	}
	if !strings.Contains(h.queries[1].sql, "a.code LIKE $4") || h.queries[1].args[3] != "ab12" {
		t.Errorf("a hex query also searches codes: %+v", h.queries[1])
	}

	h.queries = nil
	get(t, "/admin/events/{id}/checkin", "e1", editor, map[string][]string{"q": {strings.Repeat("a", 65)}})
	if len(h.queries) != 1 {
		t.Error("a query over 64 characters runs no search")
	}
}
