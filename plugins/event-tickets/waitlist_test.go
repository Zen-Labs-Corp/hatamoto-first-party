package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// waitHost fakes the statements of the waitlist code in order and records
// every write. promote answers the ids of the successive promotions; once
// they are used up nobody waits.
type waitHost struct {
	key      string
	lookup   string // jsonb cell of the entry lookup
	attendee string // answer of the cancel statement: a type id or null
	leave    string // answer of the leave statement
	claim    string // answer of the claim statement
	promote  []string
	calls    []call
	mails    int
}

const offeredEntry = `{"status":"offered","event_id":"e1","event_name":"","type_name":"VIP","expired":false,"ticket_code":""}`

func newWaitHost(t *testing.T) *waitHost {
	h := &waitHost{lookup: offeredEntry, attendee: `null`, leave: `null`, claim: `null`}
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		if fn == sdk.FnDBExec || fn == sdk.FnDBQuery {
			h.calls = append(h.calls, call{fn, sql, ar})
		}
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{}`, out)
		case fn == sdk.FnEntitiesGet:
			return jsonInto(gigEntity, out)
		case fn == sdk.FnMailSend:
			h.mails++
			return nil
		case strings.HasPrefix(sql, "INSERT INTO meta"):
			if h.key == "" {
				h.key = ar[1].(string)
			}
			return nil
		case strings.Contains(sql, "FROM meta"):
			return cell(`"` + h.key + `"`)
		case strings.Contains(sql, "FROM waitlist w JOIN ticket_types"):
			return cell(h.lookup)
		case strings.Contains(sql, "UPDATE attendees SET status = 'cancelled'"):
			return cell(h.attendee)
		case strings.Contains(sql, "SET status = 'left'"):
			return cell(h.leave)
		case strings.Contains(sql, "SET status = 'claimed'"):
			return cell(h.claim)
		case strings.Contains(sql, "SET status = 'offered'"):
			if len(h.promote) == 0 {
				return cell(`null`)
			}
			v := h.promote[0]
			h.promote = h.promote[1:]
			return cell(v)
		case strings.Contains(sql, "FROM mail_queue WHERE next_at"):
			return cell(`[]`)
		case strings.Contains(sql, "FROM orders o WHERE"):
			return cell(twoTickets)
		case strings.HasPrefix(sql, "INSERT INTO mail_queue"), strings.HasPrefix(sql, "UPDATE orders SET mailed_at"):
			return nil
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *waitHost) count(substr string) int {
	n := 0
	for _, c := range h.calls {
		if strings.Contains(c.sql, substr) {
			n++
		}
	}
	return n
}

// order is the position of the first statement containing substr, -1 if none.
func (h *waitHost) order(substr string) int {
	for i, c := range h.calls {
		if strings.Contains(c.sql, substr) {
			return i
		}
	}
	return -1
}

func (h *waitHost) writes() int {
	return h.count("SET status = 'claimed'") + h.count("SET status = 'left'") + h.count("SET status = 'offered'") +
		h.count("INSERT INTO orders") + h.count("INSERT INTO mail_queue") + h.count("UPDATE attendees")
}

func (h *waitHost) token(t *testing.T, kind string) string {
	t.Helper()
	key, err := hmacSecret()
	if err != nil {
		t.Fatal(err)
	}
	return signCode(key, kind, "0123456789abcdef0123456789abcdef")
}

func waitGet(t *testing.T, action, tok string, yes bool) sdk.HTTPResponse {
	q := map[string][]string{}
	if yes {
		q["yes"] = []string{"1"}
	}
	return publicGet(t, "/waitlist/{token}/"+action, map[string]string{"token": tok}, q)
}

func TestCancelAttendeePromotes(t *testing.T) {
	h := newWaitHost(t)
	h.attendee = `6`
	h.promote = []string{`3`}
	typeID, promoted, ok := cancelAttendee(12)
	if !ok || typeID != 6 || promoted != 1 {
		t.Fatalf("%d %d %v", typeID, promoted, ok)
	}
	cancel, promote := h.order("UPDATE attendees SET status = 'cancelled'"), h.order("SET status = 'offered'")
	if cancel < 0 || promote < cancel {
		t.Errorf("the seat is freed, then offered: %d %d", cancel, promote)
	}
	if !strings.Contains(h.calls[cancel].sql, "taken = taken - 1") || !strings.Contains(h.calls[cancel].sql, "status = 'active'") {
		t.Error("cancelling frees the seat in the same statement")
	}
	p := h.calls[promote].sql
	for _, want := range []string{"ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED", "taken + 1 <= capacity", "interval '48 hours'"} {
		if !strings.Contains(p, want) {
			t.Errorf("promotion statement lacks %q", want)
		}
	}
	var queued *call
	for i, c := range h.calls {
		if strings.HasPrefix(c.sql, "INSERT INTO mail_queue") {
			queued = &h.calls[i]
		}
	}
	if queued == nil || queued.args[0] != kindWaitlistOffer || queued.args[1] != int64(3) {
		t.Errorf("one offer mail is queued: %+v", queued)
	}
	if h.count("INSERT INTO mail_queue") != 1 {
		t.Error("exactly one offer mail")
	}
}

func TestCancelAttendeeIsIdempotent(t *testing.T) {
	h := newWaitHost(t) // the statement finds no active ticket
	if _, _, ok := cancelAttendee(12); ok {
		t.Error("already cancelled")
	}
	if h.count("SET status = 'offered'") != 0 || h.count("INSERT INTO mail_queue") != 0 {
		t.Error("nothing is promoted or mailed")
	}
}

func TestPromoteWithNobodyWaiting(t *testing.T) {
	h := newWaitHost(t)
	if _, ok := promoteNext(6); ok {
		t.Error("nobody waits")
	}
	promoteAll(6)
	if h.count("INSERT INTO mail_queue") != 0 {
		t.Error("nothing to mail")
	}
}

func TestPromoteAllOffersEverySeat(t *testing.T) {
	h := newWaitHost(t)
	h.promote = []string{`1`, `2`, `3`}
	promoteAll(6)
	if h.count("INSERT INTO mail_queue") != 3 {
		t.Errorf("%d offers", h.count("INSERT INTO mail_queue"))
	}
}

func TestClaimWithoutConfirmationChangesNothing(t *testing.T) {
	h := newWaitHost(t)
	r := waitGet(t, "claim", h.token(t, kindWait), false)
	if r.Status != 200 || !strings.Contains(r.Body, "?yes=1") || !strings.Contains(r.Body, "VIP") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if strings.Contains(r.Body, "<form") || strings.Contains(r.Body, "<script") {
		t.Error("the page has no form and no script")
	}
	if h.writes() != 0 {
		t.Errorf("a plain GET writes nothing: %d", h.writes())
	}
	if w := waitGet(t, "leave", h.token(t, kindWait), false); h.writes() != 0 || !strings.Contains(w.Body, "?yes=1") {
		t.Error("leave without yes only asks")
	}
}

func TestClaimConvertsTheOffer(t *testing.T) {
	h := newWaitHost(t)
	h.claim = `{"order_id":77,"code":"x"}`
	r := waitGet(t, "claim", h.token(t, kindWait), true)
	if r.Status != 200 || !strings.Contains(r.Body, "Your ticket is ready") || !strings.Contains(r.Body, "/p/event-tickets/ticket/") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if h.count("SET status = 'claimed'") != 1 || h.count("INSERT INTO orders") != 1 || h.count("INSERT INTO attendees") != 1 {
		t.Error("the claim is one statement writing the entry, the order and the ticket")
	}
	if i := h.order("SET status = 'claimed'"); strings.Contains(h.calls[i].sql, "UPDATE ticket_types") {
		t.Error("claiming must not touch the counter: the offer already holds the seat")
	}
	if h.mails != 1 {
		t.Errorf("%d confirmation mails", h.mails)
	}
}

func TestClaimReplayShowsTheSameTicket(t *testing.T) {
	h := newWaitHost(t)
	h.lookup = `{"status":"claimed","event_id":"e1","event_name":"","type_name":"VIP","expired":true,"ticket_code":"0123456789abcdef0123456789abcdef"}`
	r := waitGet(t, "claim", h.token(t, kindWait), true)
	if !strings.Contains(r.Body, "Your ticket is ready") || !strings.Contains(r.Body, "/p/event-tickets/ticket/") {
		t.Errorf("%s", r.Body)
	}
	if h.writes() != 0 || h.mails != 0 {
		t.Error("a replay writes and mails nothing")
	}
}

func TestClaimLostRaceShowsWhatIsTrue(t *testing.T) {
	h := newWaitHost(t) // claim answers null: the offer ended between the read and the claim
	r := waitGet(t, "claim", h.token(t, kindWait), true)
	if h.mails != 0 || r.Status != 200 {
		t.Errorf("%d %d", r.Status, h.mails)
	}
}

func TestClaimExpiredOffer(t *testing.T) {
	h := newWaitHost(t)
	h.lookup = strings.Replace(offeredEntry, `"expired":false`, `"expired":true`, 1)
	r := waitGet(t, "claim", h.token(t, kindWait), true)
	if !strings.Contains(r.Body, "This offer has expired") || h.count("SET status = 'claimed'") != 0 {
		t.Errorf("an expired offer cannot be claimed: %s", r.Body)
	}
	h.lookup = strings.Replace(offeredEntry, `"offered"`, `"expired"`, 1)
	if r := waitGet(t, "claim", h.token(t, kindWait), false); !strings.Contains(r.Body, "This offer has expired") {
		t.Error("an entry that expired")
	}
	h.lookup = strings.Replace(offeredEntry, `"offered"`, `"waiting"`, 1)
	if r := waitGet(t, "claim", h.token(t, kindWait), true); !strings.Contains(r.Body, "still on the waitlist") || h.count("SET status = 'claimed'") != 0 {
		t.Error("a waiting entry has nothing to claim yet")
	}
}

func TestLeaveOfferFreesTheSeatAndPromotes(t *testing.T) {
	h := newWaitHost(t)
	h.leave = `6`
	h.promote = []string{`4`}
	r := waitGet(t, "leave", h.token(t, kindWait), true)
	if !strings.Contains(r.Body, "You left the waitlist") {
		t.Fatalf("%s", r.Body)
	}
	left, promote := h.order("SET status = 'left'"), h.order("SET status = 'offered'")
	if left < 0 || promote < left || !strings.Contains(h.calls[left].sql, "taken = taken - 1") {
		t.Errorf("the seat goes back in the leave statement, then is offered: %d %d", left, promote)
	}
	if h.count("INSERT INTO mail_queue") != 1 {
		t.Error("the next person gets the offer mail")
	}
}

func TestLeaveWaitingEntryFreesNothing(t *testing.T) {
	h := newWaitHost(t)
	h.lookup = strings.Replace(offeredEntry, `"offered"`, `"waiting"`, 1)
	waitGet(t, "leave", h.token(t, kindWait), true)
	if h.count("SET status = 'left'") != 1 || h.count("SET status = 'offered'") != 0 {
		t.Error("leaving while waiting frees no seat")
	}
	h.lookup = strings.Replace(offeredEntry, `"offered"`, `"left"`, 1)
	n := h.count("SET status = 'left'")
	if r := waitGet(t, "leave", h.token(t, kindWait), true); !strings.Contains(r.Body, "You left the waitlist") || h.count("SET status = 'left'") != n {
		t.Error("leaving twice is the same page and writes nothing")
	}
}

func TestWaitlistForgedTokens(t *testing.T) {
	h := newWaitHost(t)
	good := h.token(t, kindWait)
	for name, tok := range map[string]string{
		"flipped": flipTok(good),
		"kind":    h.token(t, kindTicket),
		"garbage": "x",
	} {
		for _, action := range []string{"claim", "leave"} {
			if r := waitGet(t, action, tok, true); r.Status != 404 {
				t.Errorf("%s %s: %d", name, action, r.Status)
			}
		}
	}
	if h.count("FROM waitlist w") != 0 || h.writes() != 0 {
		t.Error("a forged token must not reach the database")
	}
	h.lookup = `null`
	if r := waitGet(t, "claim", good, true); r.Status != 404 {
		t.Errorf("unknown entry: %d", r.Status)
	}
}

// RETURNING sees the row after the update, so the held flag must not read the
// status the same statement just set to 'left'.
func TestLeaveStatementDoesNotReadNewStatusForHeld(t *testing.T) {
	h := newWaitHost(t)
	h.leave = `7`
	if _, err := leaveWaitlist("c"); err != nil {
		t.Fatal(err)
	}
	var sql string
	for _, c := range h.calls {
		if strings.Contains(c.sql, "SET status = 'left'") {
			sql = c.sql
		}
	}
	if sql == "" {
		t.Fatal("no leave statement")
	}
	if strings.Contains(sql, "status = 'offered' AS held") || !strings.Contains(sql, "offered_at IS NOT NULL AS held") {
		t.Errorf("held must be derived from offered_at, got %s", sql)
	}
}
