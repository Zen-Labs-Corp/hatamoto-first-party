package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// mailHost fakes settings, the signing key, the order, waitlist and queue
// reads, the event and mail.send; it records the writes.
type mailHost struct {
	settings string
	entity   string
	order    string
	waiting  string
	queue    []queuedMail
	mailErrs []error // answers of mail.send in turn; nil = sent
	key      string
	mails    []map[string]any
	writes   []call
}

const twoTickets = `{"event_id":"e1","name":"Ana","email":"ana@example.com","anonymised":false,"tickets":[
{"code":"0123456789abcdef0123456789abcdef","type_name":"Standard"},{"code":"fedcba9876543210fedcba9876543210","type_name":"Standard"}]}`

func newMailHost(t *testing.T) *mailHost {
	h := &mailHost{settings: `{}`, entity: gigEntity, order: twoTickets}
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(h.settings, out)
		case fn == sdk.FnEntitiesGet:
			return jsonInto(h.entity, out)
		case fn == sdk.FnMailSend:
			h.mails = append(h.mails, a)
			if len(h.mailErrs) == 0 {
				return nil
			}
			err := h.mailErrs[0]
			h.mailErrs = h.mailErrs[1:]
			return err
		case strings.HasPrefix(sql, "INSERT INTO meta"):
			if h.key == "" {
				h.key = ar[1].(string)
			}
			return nil
		case strings.Contains(sql, "FROM meta"):
			return jsonInto(`{"rows":[["`+h.key+`"]]}`, out)
		case strings.Contains(sql, "FROM orders o WHERE"):
			return jsonInto(`{"rows":[[`+h.order+`]]}`, out)
		case strings.Contains(sql, "FROM waitlist w JOIN"):
			return jsonInto(`{"rows":[[`+h.waiting+`]]}`, out)
		case strings.Contains(sql, "FROM mail_queue WHERE next_at"):
			rows := h.queue
			if len(rows) > 20 {
				rows = rows[:20]
			}
			return jsonInto(`{"rows":[[`+jsonText(rows)+`]]}`, out)
		case strings.HasPrefix(sql, "DELETE FROM mail_queue"), strings.HasPrefix(sql, "UPDATE mail_queue"):
			h.writes = append(h.writes, call{fn, sql, ar})
			id := int64(ar[0].(int64))
			for i, q := range h.queue {
				if q.ID == id {
					h.queue = append(h.queue[:i], h.queue[i+1:]...)
					break
				}
			}
			return nil
		case strings.HasPrefix(sql, "UPDATE orders SET mailed_at"), strings.HasPrefix(sql, "INSERT INTO mail_queue"):
			h.writes = append(h.writes, call{fn, sql, ar})
			return nil
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *mailHost) wrote(prefix string) int {
	n := 0
	for _, c := range h.writes {
		if strings.HasPrefix(c.sql, prefix) {
			n++
		}
	}
	return n
}

func TestConfirmMailAbsoluteQR(t *testing.T) {
	h := newMailHost(t)
	h.settings = `{"confirmation_text":"Door opens at 19:00.","sender_name":"The Crew"}`
	m, ok, err := composeConfirm(7)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	img := regexp.MustCompile(`<img src="(https://example\.org/p/event-tickets/ticket/[0-9a-f]{32}\.[0-9a-f]{32}/qr\.png)" width="240" height="240" alt="QR code for ticket`)
	if got := img.FindAllStringSubmatch(m.HTML, -1); len(got) != 2 {
		t.Fatalf("%d QR images, want 2: %s", len(got), m.HTML)
	}
	for _, body := range []string{m.HTML, m.Text} {
		for _, want := range []string{"01234567 89abcdef 01234567 89abcdef", "fedcba98 76543210 fedcba98 76543210", "Door opens at 19:00.", "The Crew", "Ana"} {
			if !strings.Contains(body, want) {
				t.Errorf("lacks %q: %s", want, body)
			}
		}
		if n := strings.Count(body, "https://example.org/p/event-tickets/ticket/"); n < 2 {
			t.Errorf("%d ticket links", n)
		}
	}
	if m.To != "ana@example.com" || m.Subject != "Your tickets for Gig" {
		t.Errorf("%q %q", m.To, m.Subject)
	}
	if len(m.HTML) > 256<<10 {
		t.Error("html too large")
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		t.Error("subject has a line break")
	}
}

func TestConfirmMailOrigins(t *testing.T) {
	h := newMailHost(t)
	h.entity = strings.Replace(gigEntity, "https://example.org/events/gig", "/events/gig", 1)
	h.settings = `{"site_url":"https://example.org"}`
	m, _, _ := composeConfirm(7)
	if strings.Count(m.HTML, `<img src="https://example.org/p/event-tickets/ticket/`) != 2 {
		t.Errorf("site_url is the origin when the event address is a path: %s", m.HTML)
	}
	h.settings = `{}`
	m, ok, _ := composeConfirm(7)
	if !ok || strings.Contains(m.HTML, "<img") {
		t.Errorf("no origin, no image: %s", m.HTML)
	}
	for _, want := range []string{"/p/event-tickets/ticket/", "01234567 89abcdef 01234567 89abcdef"} {
		if !strings.Contains(m.HTML, want) || !strings.Contains(m.Text, want) {
			t.Errorf("path link and code remain: lacks %q", want)
		}
	}
}

func TestConfirmMailJoinLinkForHolders(t *testing.T) {
	newMailHost(t) // join_link_public "false", hybrid
	m, _, _ := composeConfirm(7)
	if !strings.Contains(m.HTML, "https://meet.example.org/x") || !strings.Contains(m.Text, "Join online: https://meet.example.org/x") {
		t.Error("the confirmation carries the join link even when it is not public")
	}
}

func TestConfirmMailEscapesAndSkips(t *testing.T) {
	h := newMailHost(t)
	h.order = strings.Replace(twoTickets, `"Ana"`, `"<script>x</script>"`, 1)
	m, _, _ := composeConfirm(7)
	if strings.Contains(m.HTML, "<script>") {
		t.Error("holder name must be escaped")
	}
	for name, order := range map[string]string{
		"gone":       `null`,
		"anonymised": strings.Replace(twoTickets, `"anonymised":false`, `"anonymised":true`, 1),
		"no tickets": `{"event_id":"e1","name":"A","email":"a@example.com","anonymised":false,"tickets":[]}`,
	} {
		h.order = order
		if _, ok, err := composeConfirm(7); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
}

func TestWaitlistMails(t *testing.T) {
	h := newMailHost(t)
	h.waiting = `{"event_id":"e1","name":"Bo","email":"bo@example.com","code":"0123456789abcdef0123456789abcdef","status":"waiting","type_name":"Standard","expires_at":""}`
	m, ok, err := composeWaitlist(kindWaitlistJoined, 3)
	if err != nil || !ok || !strings.Contains(m.Text, "on the waitlist") || !strings.Contains(m.Text, "/leave") || strings.Contains(m.Text, "/claim") {
		t.Fatalf("%v %v %+v", ok, err, m)
	}
	if _, ok, _ := composeWaitlist(kindWaitlistOffer, 3); ok {
		t.Error("an entry that waits has no offer to mail")
	}
	h.waiting = strings.Replace(strings.Replace(h.waiting, `"waiting"`, `"offered"`, 1), `"expires_at":""`, `"expires_at":"2027-02-01 10:00"`, 1)
	m, ok, _ = composeWaitlist(kindWaitlistOffer, 3)
	if !ok || !strings.Contains(m.Text, "/claim") || !strings.Contains(m.Text, "/leave") || !strings.Contains(m.Text, "2027-02-01 10:00 UTC") {
		t.Errorf("offer mail: %+v", m)
	}
	tok := regexp.MustCompile(`/waitlist/([0-9a-f]{32}\.[0-9a-f]{32})/claim`).FindStringSubmatch(m.Text)
	if tok == nil {
		t.Fatal("no signed claim link")
	}
	key, _ := hmacSecret()
	if _, ok := verifyToken(key, kindWait, tok[1]); !ok {
		t.Error("the claim link carries a wait token")
	}
}

func TestSendNowQueuesOnError(t *testing.T) {
	h := newMailHost(t)
	sendNow(kindConfirm, 7)
	if len(h.mails) != 1 || h.wrote("UPDATE orders SET mailed_at") != 1 || h.wrote("INSERT INTO mail_queue") != 0 {
		t.Errorf("sent: %d mails, %d writes", len(h.mails), len(h.writes))
	}
	h = newMailHost(t)
	h.mailErrs = []error{errors.New("smtp down")}
	sendNow(kindConfirm, 7)
	if h.wrote("INSERT INTO mail_queue") != 1 || h.wrote("UPDATE orders SET mailed_at") != 0 {
		t.Errorf("a failed mail is queued: %+v", h.writes)
	}
	for _, c := range h.writes {
		if strings.Contains(jsonText(c.args), "ana@example.com") {
			t.Error("the queue holds ids only")
		}
	}
}

func TestDrainStopsAtRateLimit(t *testing.T) {
	h := newMailHost(t)
	h.queue = []queuedMail{{ID: 1, Kind: kindConfirm, RefID: 7}, {ID: 2, Kind: kindConfirm, RefID: 8}}
	h.mailErrs = []error{&sdk.HostError{Code: sdk.ErrRateLimited}}
	drainMail(1e9, 100)
	if len(h.mails) != 1 || len(h.queue) != 2 || len(h.writes) != 0 {
		t.Errorf("%d mails, %d queued, %d writes", len(h.mails), len(h.queue), len(h.writes))
	}
	h.mailErrs = []error{&sdk.HostError{Code: sdk.ErrUnavailable}}
	h.mails = nil
	drainMail(1e9, 100)
	if len(h.mails) != 1 || len(h.queue) != 2 {
		t.Error("unavailable also stops the run")
	}
}

func TestDrainSendsInOrderAndDeletes(t *testing.T) {
	h := newMailHost(t)
	h.queue = []queuedMail{{ID: 1, Kind: kindConfirm, RefID: 7}, {ID: 2, Kind: kindConfirm, RefID: 8}}
	drainMail(1e9, 100)
	if len(h.mails) != 2 || len(h.queue) != 0 || h.wrote("DELETE FROM mail_queue") != 2 {
		t.Errorf("%d mails, %d queued", len(h.mails), len(h.queue))
	}
}

func TestDrainBacksOffAndGivesUp(t *testing.T) {
	h := newMailHost(t)
	h.queue = []queuedMail{{ID: 1, Kind: kindConfirm, RefID: 7, Attempts: 2}}
	h.mailErrs = []error{errors.New("boom")}
	drainMail(1e9, 100)
	if h.wrote("UPDATE mail_queue SET attempts = attempts + 1") != 1 {
		t.Fatalf("writes %+v", h.writes)
	}
	if got := h.writes[0].args[1]; got != int64(1200) { // 5 min * 2^2
		t.Errorf("backoff seconds %v, want 1200", got)
	}
	h = newMailHost(t)
	h.queue = []queuedMail{{ID: 1, Kind: kindConfirm, RefID: 7, Attempts: 9}}
	h.mailErrs = []error{errors.New("boom")}
	drainMail(1e9, 100)
	if h.wrote("DELETE FROM mail_queue") != 1 {
		t.Error("an entry that failed 10 times is dropped")
	}
}

func TestDrainDropsVanishedRows(t *testing.T) {
	h := newMailHost(t)
	h.order = `null`
	h.queue = []queuedMail{{ID: 1, Kind: kindConfirm, RefID: 7}}
	drainMail(1e9, 100)
	if len(h.mails) != 0 || h.wrote("DELETE FROM mail_queue") != 1 {
		t.Errorf("%d mails, writes %+v", len(h.mails), h.writes)
	}
}
