package main

import (
	"bytes"
	"errors"
	"html/template"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	kindConfirm        = "confirm"
	kindWaitlistJoined = "waitlist_joined"
	kindWaitlistOffer  = "waitlist_offer"

	maxMailAttempts = 10
	mailBackoff     = 5 * time.Minute
	maxMailBackoff  = 24 * time.Hour
	maxSubject      = 200
)

func init() {
	guest.OnCron("mail", func(sdk.CronEvent) error {
		drainMail(8*time.Second, 100)
		return nil
	})
}

// outMail is one message, ready for mail.send.
type outMail struct{ To, Subject, Text, HTML string }

// plainText trims s and turns control characters into spaces; keepLines lets
// line breaks and tabs stay.
func plainText(s string, keepLines bool) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case keepLines && (r == '\n' || r == '\t'):
			return r
		case r == '\r' && keepLines:
			return -1
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// capRunes shortens s to at most n runes.
func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// sendMail sends one message to one recipient. The subject is one line.
func sendMail(m outMail) error {
	args := map[string]any{
		"to":      []string{m.To},
		"subject": capRunes(plainText(m.Subject, false), maxSubject),
		"text":    m.Text,
		"html":    m.HTML,
	}
	return callHost(sdk.FnMailSend, args, nil)
}

// confirmData is what the confirmation mail says.
type confirmData struct {
	Greeting  string
	EventName string
	When      string
	OnlineURL string
	Tickets   []mailTicket
	Notes     []string // the confirmation_text, one entry per line
	Signature string
}

// mailTicket is one ticket of a mail. Img is empty when no origin is known.
type mailTicket struct {
	Type, Code, Link, Img string
}

var confirmTmpl = template.Must(template.New("confirm").Parse(`<p>{{.Greeting}}</p>
<p>Your booking for <strong>{{.EventName}}</strong> is confirmed.</p>
{{if .When}}<p>When: {{.When}}</p>
{{end}}{{if .OnlineURL}}<p>Join online: <a href="{{.OnlineURL}}">{{.OnlineURL}}</a></p>
{{end}}{{range .Tickets}}<hr>
<p><strong>{{.Type}}</strong><br>Ticket code: {{.Code}}<br>Ticket page: <a href="{{.Link}}">{{.Link}}</a></p>
{{if .Img}}<p><img src="{{.Img}}" width="240" height="240" alt="QR code for ticket {{.Code}}"></p>
{{end}}{{end}}{{if .Notes}}<hr>
{{range .Notes}}<p>{{.}}</p>
{{end}}{{end}}{{if .Signature}}<p>{{.Signature}}</p>{{end}}
`))

func (d confirmData) text() string {
	var b strings.Builder
	b.WriteString(d.Greeting + "\n\nYour booking for " + d.EventName + " is confirmed.\n")
	if d.When != "" {
		b.WriteString("\nWhen: " + d.When + "\n")
	}
	if d.OnlineURL != "" {
		b.WriteString("\nJoin online: " + d.OnlineURL + "\n")
	}
	for _, t := range d.Tickets {
		b.WriteString("\n" + t.Type + "\nTicket code: " + t.Code + "\nTicket page: " + t.Link + "\n")
	}
	if len(d.Notes) > 0 {
		b.WriteString("\n" + strings.Join(d.Notes, "\n") + "\n")
	}
	if d.Signature != "" {
		b.WriteString("\n" + d.Signature + "\n")
	}
	return b.String()
}

// orderMail is an order as the confirmation reads it.
type orderMail struct {
	EventID    string `json:"event_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Anonymised bool   `json:"anonymised"`
	Tickets    []struct {
		Code     string `json:"code"`
		TypeName string `json:"type_name"`
	} `json:"tickets"`
}

// readOrderMail loads an order; nil when it is gone.
func readOrderMail(orderID int64) (*orderMail, error) {
	var o *orderMail
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('event_id', o.event_id, 'name', o.name, 'email', o.email,
    'anonymised', o.anonymised_at IS NOT NULL,
    'tickets', (SELECT coalesce(jsonb_agg(jsonb_build_object('code', a.code, 'type_name', t.name) ORDER BY a.id), '[]'::jsonb)
                FROM attendees a JOIN ticket_types t ON t.id = a.type_id
                WHERE a.order_id = o.id AND a.status = 'active'))
FROM orders o WHERE o.id = $1), 'null'::jsonb)`, &o, orderID)
	return o, err
}

// eventFacts reads the event for a mail. An event that is gone or not
// published is not an error: the mail goes out without its details.
func eventFacts(eventID string, s settings) (event, error) {
	e, ok, err := readEvent(eventID, s)
	if err != nil {
		return event{}, err
	}
	if !ok {
		return event{ID: eventID}, nil
	}
	return e, nil
}

func greeting(name string) string {
	if name = plainText(name, false); name != "" {
		return "Hello " + name + ","
	}
	return "Hello,"
}

func eventTitle(e event) string {
	if e.Name != "" {
		return e.Name
	}
	return "the event"
}

// composeConfirm builds the confirmation of an order: one QR, link and code
// per active ticket. ok is false when there is nothing to send (the order is
// gone, anonymised or has no active ticket); err is a host or database error
// worth retrying.
func composeConfirm(orderID int64) (m outMail, ok bool, err error) {
	o, err := readOrderMail(orderID)
	if err != nil || o == nil || o.Anonymised || o.Email == "" || len(o.Tickets) == 0 {
		return outMail{}, false, err
	}
	key, err := hmacSecret()
	if err != nil {
		return outMail{}, false, err
	}
	s := loadSettings()
	e, err := eventFacts(o.EventID, s)
	if err != nil {
		return outMail{}, false, err
	}
	origin := originOf(e.URL, s.SiteURL)
	d := confirmData{
		Greeting:  greeting(o.Name),
		EventName: eventTitle(e),
		Notes:     paragraphs(s.ConfirmationText),
		Signature: s.SenderName,
	}
	if !e.Start.IsZero() {
		d.When = whenText(e)
	}
	if e.OnlineURL != "" && (e.Mode == "online" || e.Mode == "hybrid") {
		d.OnlineURL = e.OnlineURL
	}
	for _, t := range o.Tickets {
		tok := signCode(key, kindTicket, t.Code)
		mt := mailTicket{Type: t.TypeName, Code: groupCode(t.Code), Link: ticketURL(origin, tok)}
		if origin != "" {
			mt.Img = ticketURL(origin, tok) + "/qr.png"
		}
		d.Tickets = append(d.Tickets, mt)
	}
	var buf bytes.Buffer
	if err := confirmTmpl.Execute(&buf, d); err != nil {
		return outMail{}, false, err
	}
	return outMail{To: o.Email, Subject: "Your tickets for " + eventTitle(e), Text: d.text(), HTML: buf.String()}, true, nil
}

// waitlistMail is a waitlist entry as its mails read it.
type waitlistMail struct {
	EventID   string `json:"event_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Code      string `json:"code"`
	Status    string `json:"status"`
	TypeName  string `json:"type_name"`
	ExpiresAt string `json:"expires_at"`
}

func readWaitlistMail(id int64) (*waitlistMail, error) {
	var w *waitlistMail
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('event_id', w.event_id, 'name', w.name, 'email', w.email,
    'code', w.code, 'status', w.status, 'type_name', t.name,
    'expires_at', coalesce(to_char(w.expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI'), ''))
FROM waitlist w JOIN ticket_types t ON t.id = w.type_id WHERE w.id = $1), 'null'::jsonb)`, &w, id)
	return w, err
}

var waitlistTmpl = template.Must(template.New("waitlist").Parse(`<p>{{.Greeting}}</p>
{{range .Lines}}<p>{{.}}</p>
{{end}}{{range .Links}}<p>{{.Text}}: <a href="{{.Href}}">{{.Href}}</a></p>
{{end}}{{if .Signature}}<p>{{.Signature}}</p>{{end}}
`))

type waitlistData struct {
	Greeting  string
	Lines     []string
	Links     []pageLink
	Signature string
}

func (d waitlistData) text() string {
	var b strings.Builder
	b.WriteString(d.Greeting + "\n\n")
	for _, l := range d.Lines {
		b.WriteString(l + "\n")
	}
	for _, l := range d.Links {
		b.WriteString("\n" + l.Text + ": " + l.Href + "\n")
	}
	if d.Signature != "" {
		b.WriteString("\n" + d.Signature + "\n")
	}
	return b.String()
}

// composeWaitlist builds the mail of a waitlist entry: kindWaitlistJoined
// while it waits, kindWaitlistOffer while a seat is held for it. ok is false
// when the entry no longer is in that state.
func composeWaitlist(kind string, waitID int64) (m outMail, ok bool, err error) {
	w, err := readWaitlistMail(waitID)
	if err != nil || w == nil || w.Email == "" {
		return outMail{}, false, err
	}
	if (kind == kindWaitlistJoined && w.Status != "waiting" && w.Status != "offered") ||
		(kind == kindWaitlistOffer && w.Status != "offered") {
		return outMail{}, false, nil
	}
	key, err := hmacSecret()
	if err != nil {
		return outMail{}, false, err
	}
	s := loadSettings()
	e, err := eventFacts(w.EventID, s)
	if err != nil {
		return outMail{}, false, err
	}
	origin := originOf(e.URL, s.SiteURL)
	base := origin + "/p/event-tickets/waitlist/" + signCode(key, kindWait, w.Code)
	d := waitlistData{Greeting: greeting(w.Name), Signature: s.SenderName}
	title := eventTitle(e)
	subject := "You are on the waitlist for " + title
	leave := pageLink{base + "/leave", "Leave the waitlist"}
	if kind == kindWaitlistOffer {
		subject = "A ticket is available for " + title
		d.Lines = []string{
			"A " + w.TypeName + " ticket for " + title + " has become available.",
			"It is held for you until " + w.ExpiresAt + " UTC. After that it goes to the next person.",
		}
		d.Links = []pageLink{{base + "/claim", "Claim your ticket"}, leave}
	} else {
		d.Lines = []string{"You are on the waitlist for " + w.TypeName + " tickets for " + title + ". We will email you if a ticket becomes available."}
		d.Links = []pageLink{leave}
	}
	var buf bytes.Buffer
	if err := waitlistTmpl.Execute(&buf, d); err != nil {
		return outMail{}, false, err
	}
	return outMail{To: w.Email, Subject: subject, Text: d.text(), HTML: buf.String()}, true, nil
}

// compose builds the mail of a queue entry.
func compose(kind string, ref int64) (outMail, bool, error) {
	switch kind {
	case kindConfirm:
		return composeConfirm(ref)
	case kindWaitlistJoined, kindWaitlistOffer:
		return composeWaitlist(kind, ref)
	}
	return outMail{}, false, nil
}

// deliver composes and sends one entry. sent is false when there was nothing
// to send; err is set when it should be tried again.
func deliver(kind string, ref int64) (sent bool, err error) {
	m, ok, err := compose(kind, ref)
	if err != nil || !ok {
		return false, err
	}
	if err := sendMail(m); err != nil {
		return false, err
	}
	if kind == kindConfirm {
		_ = dbExec(`UPDATE orders SET mailed_at = now() WHERE id = $1`, ref)
	}
	return true, nil
}

// enqueue puts a mail on the queue; the cron sends it.
func enqueue(kind string, ref int64) error {
	return dbExec(`INSERT INTO mail_queue (kind, ref_id) VALUES ($1, $2)`, kind, ref)
}

// sendNow tries a mail at once and queues it when that fails, so a mail error
// never fails the request that asked for it.
func sendNow(kind string, ref int64) {
	if _, err := deliver(kind, ref); err != nil {
		_ = enqueue(kind, ref)
	}
}

type queuedMail struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	RefID    int64  `json:"ref_id"`
	Attempts int    `json:"attempts"`
}

func dueMail() ([]queuedMail, error) {
	var rows []queuedMail
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(q) ORDER BY q.id), '[]'::jsonb) FROM (
SELECT id, kind, ref_id, attempts FROM mail_queue WHERE next_at <= now() ORDER BY id LIMIT 20) q`, &rows)
	return rows, err
}

// drainMail sends queued mail in order, at most max entries within budget.
// It stops at the first rate-limit or unavailable answer, leaving the rest
// queued; any other error backs that entry off, and one that keeps failing
// is dropped.
func drainMail(budget time.Duration, max int) {
	deadline := now().Add(budget)
	for done := 0; done < max; {
		rows, err := dueMail()
		if err != nil || len(rows) == 0 {
			return
		}
		for _, q := range rows {
			if done >= max || now().After(deadline) {
				return
			}
			done++
			_, err := deliver(q.Kind, q.RefID)
			var he *sdk.HostError
			switch {
			case err == nil:
				_ = dbExec(`DELETE FROM mail_queue WHERE id = $1`, q.ID)
			case errors.As(err, &he) && (he.Code == sdk.ErrRateLimited || he.Code == sdk.ErrUnavailable):
				return
			case q.Attempts+1 >= maxMailAttempts:
				_ = dbExec(`DELETE FROM mail_queue WHERE id = $1`, q.ID)
			default:
				delay := min(mailBackoff<<q.Attempts, maxMailBackoff)
				_ = dbExec(`UPDATE mail_queue SET attempts = attempts + 1, next_at = now() + $2 * interval '1 second' WHERE id = $1`,
					q.ID, int64(delay/time.Second))
			}
		}
	}
}

// paragraphs splits text into its non-empty lines.
func paragraphs(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, plainText(l, false))
		}
	}
	return out
}
