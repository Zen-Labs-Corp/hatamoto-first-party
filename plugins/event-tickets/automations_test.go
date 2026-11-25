package main

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// autoHost fakes the statements of the automatic mails on top of mailHost:
// the change markers, the fan-out inserts and the event_changes lookup.
type autoHost struct {
	*mailHost
	markers string // ids of unfanned markers, a jsonb array
	what    string // event_changes.what of the one marker, "" = none
	inserts []call
	picks   []call
	meta    []call
}

func newAutoHost(t *testing.T) *autoHost {
	h := &autoHost{mailHost: newMailHost(t), markers: `[]`, what: "date"}
	inner := callHost
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		switch {
		case strings.Contains(sql, "FROM event_changes WHERE fanned_at IS NULL"):
			h.picks = append(h.picks, call{fn, sql, ar})
			return jsonInto(`{"rows":[[`+h.markers+`]]}`, out)
		case strings.Contains(sql, "FROM event_changes WHERE id"):
			if h.what == "" {
				return jsonInto(`{"rows":[[null]]}`, out)
			}
			return jsonInto(`{"rows":[[`+jsonText(h.what)+`]]}`, out)
		case strings.HasPrefix(sql, "WITH p AS (SELECT id, what"), strings.HasPrefix(sql, "INSERT INTO email_sends"):
			h.inserts = append(h.inserts, call{fn, sql, ar})
			return nil
		case strings.HasPrefix(sql, "INSERT INTO meta (key, value) VALUES ('email."):
			h.meta = append(h.meta, call{fn, sql, ar})
			return nil
		}
		return inner(fn, args, out)
	}
	return h
}

func never() bool { return false }

func TestFanoutChangesOncePerEventAndCoalesces(t *testing.T) {
	h := newAutoHost(t)
	h.markers = `["e1","e2"]`
	fanoutChanges(defaultAutomations(), never)
	if len(h.inserts) != 2 {
		t.Fatalf("%d statements, want one per event", len(h.inserts))
	}
	c := h.inserts[0]
	for _, want := range []string{
		"UPDATE event_changes SET fanned_at = now()", "AND fanned_at IS NULL RETURNING id",
		"r.created_at > now() - interval '10 minutes'",                                                   // only a settled event
		"WHEN id = (SELECT max(id) FROM p)",                                                              // the newest marker carries the union of what changed
		"(SELECT count(*) FROM d) = (SELECT count(*) FROM p)",                                            // all or nothing, so a second instance finds nothing
		"(SELECT old_sig FROM p ORDER BY id LIMIT 1) = (SELECT new_sig FROM p ORDER BY id DESC LIMIT 1)", // undone: no mail
		"SELECT max(id) FROM p", "ON CONFLICT DO NOTHING",
		"DISTINCT ON (lower(o.email))", "NOT EXISTS (SELECT 1 FROM unsubscribes", "o.status IN ('confirmed', 'paid')", "o.anonymised_at IS NULL", "gen_random_uuid()"} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
	if jsonText(c.args) != `["e1",true]` || jsonText(h.inserts[1].args) != `["e2",true]` {
		t.Errorf("args %s %s", jsonText(c.args), jsonText(h.inserts[1].args))
	}
	if strings.Contains(c.sql, "ana@") || strings.Contains(c.sql, "email_sends (kind, ref_id, event_id, order_id, email_hash, code, email") {
		t.Error("an address must not be stored in email_sends")
	}
	if pick := h.picks[0]; !strings.Contains(pick.sql, "HAVING max(created_at) <= now() - interval '10 minutes'") || pick.args[0] != changeEventsPerRun {
		t.Errorf("the pick must wait for a quiet event: %s %v", pick.sql, pick.args)
	}

	// the switch off still consumes the markers, with no recipients
	h = newAutoHost(t)
	h.markers = `["e9"]`
	off := defaultAutomations()
	off.Changed = false
	fanoutChanges(off, never)
	if len(h.inserts) != 1 || jsonText(h.inserts[0].args) != `["e9",false]` {
		t.Errorf("off: %+v", h.inserts)
	}

	// nothing settled: nothing written
	h = newAutoHost(t)
	fanoutChanges(defaultAutomations(), never)
	if len(h.inserts) != 0 {
		t.Errorf("%d statements for no markers", len(h.inserts))
	}
}

func TestFanoutReminders(t *testing.T) {
	h := newAutoHost(t)
	a := defaultAutomations()
	a.ReminderHours = 48
	fanoutReminders(a, never)
	if len(h.inserts) != 1 {
		t.Fatalf("%d statements", len(h.inserts))
	}
	c := h.inserts[0]
	for _, want := range []string{"'reminder'", "e.status = 'published'", "e.starts_at > now()", "e.starts_at <= now() + make_interval(hours => $1)",
		"o.created_at < e.starts_at - make_interval(hours => $1)", "NOT EXISTS (SELECT 1 FROM email_sends s WHERE s.kind = 'reminder'",
		"NOT EXISTS (SELECT 1 FROM unsubscribes", "LIMIT $2", "ON CONFLICT DO NOTHING"} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
	if jsonText(c.args) != `[48,1000]` {
		t.Errorf("args %s", jsonText(c.args))
	}
	a.Reminder = false
	h.inserts = nil
	fanoutReminders(a, never)
	if len(h.inserts) != 0 {
		t.Error("the reminder switch is off")
	}
}

func TestFanoutThanks(t *testing.T) {
	h := newAutoHost(t)
	fanoutThanks(defaultAutomations(), never) // off by default
	if len(h.inserts) != 0 {
		t.Fatal("thank-you is off by default")
	}
	a := defaultAutomations()
	a.Thanks = true
	fanoutThanks(a, never)
	if len(h.inserts) != 1 {
		t.Fatalf("%d statements", len(h.inserts))
	}
	for _, want := range []string{"'thanks'", "e.ends_at <= now()", "e.ends_at > now() - interval '48 hours'", "e.status = 'published'", "LIMIT $1"} {
		if !strings.Contains(h.inserts[0].sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
}

func TestComposeAutoCarriesTheUnsubscribeLink(t *testing.T) {
	h := newAutoHost(t)
	key, err := hmacSecret()
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Repeat("ab", 16)
	link := regexp.MustCompile(`https://example\.org/p/event-tickets/u/([0-9a-f]{32}\.[0-9a-f]{32})`)
	for _, kind := range []string{"changed", "reminder", "thanks"} {
		m, ok, err := composeAuto(sendRow{Kind: kind, RefID: 3, OrderID: 7, EventID: "e1", Code: code})
		if err != nil || !ok {
			t.Fatalf("%s: %v %v", kind, ok, err)
		}
		for name, body := range map[string]string{"html": m.HTML, "text": m.Text} {
			got := link.FindStringSubmatch(body)
			if got == nil {
				t.Fatalf("%s %s lacks the unsubscribe link: %s", kind, name, body)
			}
			if c, ok := verifyToken(key, "unsub", got[1]); !ok || c != code {
				t.Errorf("%s %s: token does not verify as unsub for the send code", kind, name)
			}
			if _, ok := verifyToken(key, kindTicket, got[1]); ok {
				t.Errorf("%s %s: the unsubscribe token must not pass as a ticket token", kind, name)
			}
		}
		if strings.Contains(m.HTML, "<img") || strings.Contains(m.HTML, "/o/") || strings.Contains(m.HTML, "/c/") {
			t.Errorf("%s: automatic mails carry no tracking: %s", kind, m.HTML)
		}
		if m.To != "ana@example.com" {
			t.Errorf("%s to %q", kind, m.To)
		}
	}

	m, _, _ := composeAuto(sendRow{Kind: "reminder", OrderID: 7, EventID: "e1", Code: code})
	if n := strings.Count(m.Text, "/p/event-tickets/ticket/"); n != 2 || !strings.Contains(m.Subject, "Reminder: Gig") {
		t.Errorf("reminder: %d ticket links, subject %q", n, m.Subject)
	}
	m, _, _ = composeAuto(sendRow{Kind: "changed", RefID: 3, OrderID: 7, EventID: "e1", Code: code})
	if m.Subject != "Change to Gig" || !strings.Contains(m.Text, "See the event page for the place") || !strings.Contains(m.Text, "When: ") {
		t.Errorf("changed: %q %s", m.Subject, m.Text)
	}
	h.entity = strings.Replace(gigEntity, `"attendance_mode"`, `"venue_name":"Hall","venue_city":"Paris","event_status":"cancelled","attendance_mode"`, 1)
	h.what = "status"
	m, _, _ = composeAuto(sendRow{Kind: "changed", RefID: 3, OrderID: 7, EventID: "e1", Code: code})
	if m.Subject != "Gig is cancelled" || !strings.Contains(m.Text, "Where: Hall, Paris") {
		t.Errorf("cancelled: %q %s", m.Subject, m.Text)
	}

	// the ticket confirmation is transactional: no unsubscribe link
	c, ok, err := composeConfirm(7)
	if err != nil || !ok || strings.Contains(c.HTML, "/p/event-tickets/u/") || strings.Contains(c.Text, "/p/event-tickets/u/") {
		t.Errorf("confirmation: ok %v err %v, has an unsubscribe link", ok, err)
	}
}

func TestComposeAutoSkips(t *testing.T) {
	h := newAutoHost(t)
	send := sendRow{Kind: "thanks", OrderID: 7, EventID: "e1", Code: strings.Repeat("ab", 16)}
	h.settings = `{}`
	h.entity = strings.Replace(gigEntity, "https://example.org/events/gig", "/events/gig", 1)
	if _, ok, err := composeAuto(send); ok || err != nil {
		t.Errorf("no mail origin: ok %v err %v, want skipped", ok, err)
	}
	h.settings = `{"site_url":"https://example.org"}`
	if _, ok, _ := composeAuto(send); !ok {
		t.Error("site_url is the origin when the event address is a path")
	}
	h.order = `{"event_id":"e1","name":"","email":"","anonymised":true,"tickets":[]}`
	if _, ok, err := composeAuto(send); ok || err != nil {
		t.Errorf("anonymised order: ok %v err %v", ok, err)
	}
	h.order = twoTickets
	h.entity = `{"id":"e1","schema_slug":"calendar-event","name":"Gig","status":"draft"}`
	if _, ok, err := composeAuto(send); ok || err != nil {
		t.Errorf("unreadable event: ok %v err %v", ok, err)
	}
	h.entity = gigEntity
	h.what = ""
	if _, ok, _ := composeAuto(sendRow{Kind: "changed", RefID: 3, OrderID: 7, EventID: "e1", Code: send.Code}); ok {
		t.Error("a change marker that is gone sends nothing")
	}
}

func TestEmailsRoutes(t *testing.T) {
	h := newAutoHost(t)
	callHost0 := callHost
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		switch {
		case fn == "permissions":
			return jsonInto(`{"mail":{"recipients_per_hour":120}}`, out)
		case strings.Contains(sql, "FROM meta WHERE key LIKE"):
			return jsonInto(`{"rows":[[{"email.thanks":"true"}]]}`, out)
		case strings.Contains(sql, "FROM (SELECT c.id, c.subject"):
			return jsonInto(`{"rows":[[[]]]}`, out)
		}
		return callHost0(fn, args, out)
	}
	post := func(u *sdk.HTTPUser, body string) sdk.HTTPResponse {
		r, _ := serve(sdk.HTTPRequest{Method: http.MethodPost, Pattern: "/admin/emails/automations", User: u, Body: body})
		return r
	}
	ok := `{"changed":true,"reminder":false,"reminder_hours":36,"thanks":true}`
	if r := post(editor, ok); r.Status != http.StatusForbidden {
		t.Errorf("editor: %d", r.Status)
	}
	if r := post(nil, ok); r.Status != http.StatusForbidden {
		t.Errorf("anonymous: %d", r.Status)
	}
	for _, body := range []string{
		`{"changed":true,"reminder":true,"reminder_hours":0,"thanks":false}`,
		`{"changed":true,"reminder":true,"reminder_hours":169,"thanks":false}`,
		`{"changed":true,"reminder":true,"thanks":false}`,
		`{"changed":true,"reminder":true,"reminder_hours":24,"thanks":false,"extra":1}`,
		`not json`,
	} {
		if r := post(owner, body); r.Status != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, r.Status)
		}
	}
	if len(h.meta) != 0 {
		t.Fatalf("a refused request wrote: %+v", h.meta)
	}
	if r := post(owner, ok); r.Status != http.StatusOK {
		t.Fatalf("owner: %d %s", r.Status, r.Body)
	}
	if len(h.meta) != 1 || jsonText(h.meta[0].args) != `["true","false","true","36"]` {
		t.Errorf("upsert: %+v", h.meta)
	}

	get := func(u *sdk.HTTPUser) sdk.HTTPResponse {
		r, _ := serve(sdk.HTTPRequest{Method: http.MethodGet, Pattern: "/admin/emails", User: u})
		return r
	}
	if r := get(editor); r.Status != http.StatusForbidden {
		t.Errorf("editor GET: %d", r.Status)
	}
	r := get(owner)
	for _, want := range []string{`"thanks":true`, `"reminder_hours":24`, `"bulk_per_run":7`, `"quota":120`, `"site_origin_ok":false`} {
		if r.Status != http.StatusOK || !strings.Contains(r.Body, want) {
			t.Errorf("GET lacks %s: %d %s", want, r.Status, r.Body)
		}
	}
}
