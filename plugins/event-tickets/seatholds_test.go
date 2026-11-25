package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestHoldSeatStatement(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{`{"status":"held","seat_id":42,"label":"Stalls A-12","expires_in":599}`}
	res, err := holdSeat("e1", "c0de", 9, 0, 12, maxOrdersPerHour)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "held" || res.SeatID != 42 || res.Label != "Stalls A-12" || res.ExpiresIn != 599 {
		t.Errorf("result = %+v", res)
	}
	if len(h.calls) != 1 {
		t.Fatalf("%d host calls, want one statement", len(h.calls))
	}
	if want := []any{"e1", "c0de", int64(9), 0, 12, maxTicketsPerOrder, maxOrdersPerHour}; !reflect.DeepEqual(h.calls[0].args, want) {
		t.Errorf("args = %#v, want %#v", h.calls[0].args, want)
	}
	sql := h.calls[0].sql
	mustContain(t, sql,
		"interval '10 minutes'",
		"ON CONFLICT (event_id, seat_id) DO UPDATE",
		"WHERE seat_holds.expires_at <= now()", // only an expired row is taken over
		"t.on_sale", "NOT st.blocked", "ms.kind = 'grid'", "t.event_id = $1",
		"a.status IN ('pending', 'active')",              // a sold seat is never held
		"h.code = $2 AND h.expires_at > now()",           // the code's live seats
		"< $6",                                           // seats per code
		"count(DISTINCT h.code)",                         // codes with a live hold
		"h.event_id = $1 AND h.expires_at > now()) < $7", // only live codes count
		"$7 = 0 OR NOT EXISTS (SELECT 1 FROM seat_holds h WHERE h.event_id = $1 AND h.code = $2 AND h.expires_at > now())", // 0 = no ceiling; only a live code is exempt
		"min(h.expires_at)",               // a later seat inherits the first seat's window
		"NOT EXISTS (SELECT 1 FROM mine)", // holding it again changes nothing
	)
	if strings.Contains(sql, "created_at > now()") {
		t.Error("expired holds must not count towards the ceiling")
	}
	if strings.Contains(sql, "greatest(") || strings.Contains(sql, "hs.section_id") {
		t.Error("a section-wide cap turns partial hoarding into a lockout")
	}
	if strings.Contains(sql, "OR seat_holds.code") || strings.Contains(sql, "OR code =") {
		t.Error("the takeover must not touch a live row of the same code")
	}
	if strings.Contains(sql, "15 minutes") {
		t.Error("the seat hold is not the payment window")
	}
}

func TestSeatHoldWindowIsTenMinutes(t *testing.T) {
	if seatHoldWindow != "10 minutes" || holdWindow != "15 minutes" {
		t.Errorf("seat hold %q, payment window %q", seatHoldWindow, holdWindow)
	}
}

func TestHoldSeatStatuses(t *testing.T) {
	for _, status := range []string{"taken", "full", "gone"} {
		h := newSeatHost(t)
		h.cells = []string{`{"status":"` + status + `","seat_id":0,"label":"","expires_in":0}`}
		res, err := holdSeat("e1", "c0de", 9, 0, 1, maxOrdersPerHour)
		if err != nil || res.Status != status {
			t.Errorf("%s: %+v, %v", status, res, err)
		}
	}
}

func TestReleaseSeatHold(t *testing.T) {
	h := newSeatHost(t)
	h.affected = []int64{1, 3}
	n, err := releaseSeatHold("e1", "c0de", 42)
	if err != nil || n != 1 {
		t.Fatalf("one seat: %d, %v", n, err)
	}
	if want := []any{"e1", "c0de", int64(42)}; !reflect.DeepEqual(h.calls[0].args, want) {
		t.Errorf("args = %#v", h.calls[0].args)
	}
	mustContain(t, h.calls[0].sql, "DELETE FROM seat_holds", "event_id = $1 AND code = $2", "$3::bigint = 0 OR seat_id = $3")
	if n, _ = releaseSeatHold("e1", "c0de", 0); n != 3 || h.calls[1].args[2] != int64(0) {
		t.Errorf("all seats: %d %v", n, h.calls[1].args)
	}
}

func TestHeldSeats(t *testing.T) {
	h := newSeatHost(t)
	h.cells = []string{`[{"seat_id":42,"type_id":4,"label":"Stalls A-12"},{"seat_id":43,"type_id":4,"label":"Stalls A-13"}]`, `[]`}
	got, err := heldSeats("e1", "c0de")
	if err != nil || len(got) != 2 || got[0] != (heldSeat{42, 4, "Stalls A-12"}) {
		t.Fatalf("heldSeats = %+v, %v", got, err)
	}
	mustContain(t, h.calls[0].sql, "h.event_id = $1 AND h.code = $2 AND h.expires_at > now()", "t.event_id = h.event_id")
	if got, _ := heldSeats("e1", "other"); got == nil || len(got) != 0 {
		t.Errorf("no seats = %#v", got)
	}
}

func TestHoldTokenKindIsOwn(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	code, _ := newCode()
	tok := signCode(key, kindHold, code)
	if got, ok := verifyToken(key, kindHold, tok); !ok || got != code {
		t.Error("a hold token verifies as a hold")
	}
	if _, ok := verifyToken(key, kindTicket, tok); ok {
		t.Error("a hold token must not pass as a ticket token")
	}
}
