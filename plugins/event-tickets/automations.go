package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"
)

// The mails the plugin sends on its own: an event changed, a reminder before
// it, a thank-you after it. Each goes to a person (one address, however many
// tickets) once per event. Fan-out writes one email_sends row per person; the
// emails job sends them within the mail quota.

// recipientSQL selects the people who hold a valid ticket, on orders o: paid or
// confirmed, not anonymised, with an address, and not unsubscribed.
const recipientSQL = `o.status IN ('confirmed', 'paid') AND o.anonymised_at IS NULL AND o.email <> ''
  AND EXISTS (SELECT 1 FROM attendees a WHERE a.order_id = o.id AND a.status = 'active')
  AND NOT EXISTS (SELECT 1 FROM unsubscribes u WHERE u.email_hash = ` + hashSQL + `)`

// hashSQL is the pseudonymous key of an address, computed in the database so
// the unsubscribe list works after the order is anonymised.
const hashSQL = `encode(sha256(convert_to(lower(o.email), 'UTF8')), 'hex')`

// codeSQL is a fresh random send code: 32 hex characters.
const codeSQL = `replace(gen_random_uuid()::text, '-', '')`

func init() {
	fanouts = append(fanouts, fanoutChanges, fanoutReminders, fanoutThanks)
	composers["changed"] = composeAuto
	composers["reminder"] = composeAuto
	composers["thanks"] = composeAuto
}

const changeMarkersPerRun = 20

// fanoutChanges turns each unfanned change marker into sends, once: the same
// statement marks it fanned and inserts, so a second run or a second instance
// finds nothing. With the switch off the marker is still consumed, so turning
// the mail on later does not mail old changes.
func fanoutChanges(a automations, over func() bool) {
	var ids []int64
	if err := dbJSON(`SELECT coalesce(jsonb_agg(x.id ORDER BY x.id), '[]'::jsonb)
FROM (SELECT id FROM event_changes WHERE fanned_at IS NULL ORDER BY id LIMIT $1) x`, &ids, changeMarkersPerRun); err != nil {
		return
	}
	for _, id := range ids {
		if over() {
			return
		}
		_ = dbExec(`WITH c AS (UPDATE event_changes SET fanned_at = now() WHERE id = $1 AND fanned_at IS NULL RETURNING id, event_id)
INSERT INTO email_sends (kind, ref_id, event_id, order_id, email_hash, code)
SELECT DISTINCT ON (lower(o.email)) 'changed', c.id, c.event_id, o.id, `+hashSQL+`, `+codeSQL+`
FROM c JOIN orders o ON o.event_id = c.event_id
WHERE $2::bool AND `+recipientSQL+`
ORDER BY lower(o.email), o.id
ON CONFLICT DO NOTHING`, id, a.Changed)
	}
}

// fanoutWindow inserts the sends of one timed kind. The NOT EXISTS keeps a
// person who already has a send of that kind for the event from getting a
// second one, and the limit lets a large site continue on the next run.
func fanoutWindow(kind, eventWhere, limitArg string, args ...any) {
	_ = dbExec(`INSERT INTO email_sends (kind, ref_id, event_id, order_id, email_hash, code)
SELECT DISTINCT ON (e.entity_id, lower(o.email)) '`+kind+`', 0, e.entity_id, o.id, `+hashSQL+`, `+codeSQL+`
FROM events e JOIN orders o ON o.event_id = e.entity_id
WHERE e.status = 'published' AND `+eventWhere+` AND `+recipientSQL+`
  AND NOT EXISTS (SELECT 1 FROM email_sends s WHERE s.kind = '`+kind+`' AND s.event_id = e.entity_id AND s.email_hash = `+hashSQL+`)
ORDER BY e.entity_id, lower(o.email), o.id
LIMIT `+limitArg+`
ON CONFLICT DO NOTHING`, args...)
}

// fanoutReminders mails the people of events that start within the reminder
// window. Orders placed inside the window are left out: their confirmation
// just told them.
func fanoutReminders(a automations, over func() bool) {
	if !a.Reminder || over() {
		return
	}
	fanoutWindow("reminder", `e.starts_at > now() AND e.starts_at <= now() + make_interval(hours => $1)
  AND o.created_at < e.starts_at - make_interval(hours => $1)`, "$2", a.ReminderHours, fanoutLimit)
}

// fanoutThanks mails the people of events that ended in the last 48 hours.
func fanoutThanks(a automations, over func() bool) {
	if !a.Thanks || over() {
		return
	}
	fanoutWindow("thanks", `e.ends_at <= now() AND e.ends_at > now() - interval '48 hours'`, "$1", fanoutLimit)
}

const unsubscribeNote = "You get this email because you have tickets for events on this site."

// unsubscribeURL is the link every bulk mail carries.
func unsubscribeURL(origin string, key []byte, code string) string {
	return origin + "/p/event-tickets/u/" + signCode(key, kindUnsub, code)
}

var autoTmpl = template.Must(template.New("auto").Parse(`<p>{{.Greeting}}</p>
{{range .Lines}}<p>{{.}}</p>
{{end}}{{range .Links}}<p>{{.Text}}: <a href="{{.Href}}">{{.Href}}</a></p>
{{end}}{{if .Signature}}<p>{{.Signature}}</p>{{end}}<hr>
<p>{{.Note}} Unsubscribe: <a href="{{.Unsub}}">{{.Unsub}}</a></p>
`))

// autoData is the content of an automatic mail.
type autoData struct {
	Greeting  string
	Lines     []string
	Links     []pageLink
	Signature string
	Note      string
	Unsub     string
}

func (d autoData) text() string {
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
	b.WriteString("\n--\n" + d.Note + " Unsubscribe: " + d.Unsub + "\n")
	return b.String()
}

// whereText says where an event takes place, as far as the plugin knows.
func whereText(e event) string {
	if e.VenueText != "" {
		return e.VenueText
	}
	return "See the event page for the place"
}

func httpURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// composeAuto builds a changed, reminder or thank-you mail. ok is false when
// there is nobody to write to or no absolute link to put in the mail.
func composeAuto(s sendRow) (m outMail, ok bool, err error) {
	o, err := readOrderMail(s.OrderID)
	if err != nil || o == nil || o.Anonymised || o.Email == "" || len(o.Tickets) == 0 {
		return outMail{}, false, err
	}
	key, err := hmacSecret()
	if err != nil {
		return outMail{}, false, err
	}
	set := loadSettings()
	e, err := eventFacts(s.EventID, set)
	if err != nil {
		return outMail{}, false, err
	}
	origin := originOf(e.URL, set.SiteURL)
	if e.Name == "" || origin == "" {
		return outMail{}, false, nil
	}
	title := eventTitle(e)
	d := autoData{Greeting: greeting(o.Name), Signature: set.SenderName, Note: unsubscribeNote, Unsub: unsubscribeURL(origin, key, s.Code)}
	var when string
	if !e.Start.IsZero() {
		when = whenText(e)
	}
	var subject string
	switch s.Kind {
	case "changed":
		var what string
		if err := dbJSON(`SELECT coalesce((SELECT to_jsonb(what) FROM event_changes WHERE id = $1), 'null'::jsonb)`, &what, s.RefID); err != nil {
			return outMail{}, false, err
		}
		if what == "" {
			return outMail{}, false, nil
		}
		subject = "Change to " + title
		switch {
		case strings.Contains(what, "status") && e.EventStatus == "cancelled":
			subject = title + " is cancelled"
			d.Lines = append(d.Lines, title+" has been cancelled.")
		case strings.Contains(what, "status") && e.EventStatus == "postponed":
			subject = title + " is postponed"
			d.Lines = append(d.Lines, title+" has been postponed. We will share the new date when it is set.")
		case strings.Contains(what, "status"):
			d.Lines = append(d.Lines, title+" is going ahead.")
		default:
			d.Lines = append(d.Lines, "There is a change to "+title+".")
		}
		if when != "" {
			d.Lines = append(d.Lines, "When: "+when)
		}
		d.Lines = append(d.Lines, "Where: "+whereText(e))
		if httpURL(e.URL) {
			d.Links = append(d.Links, pageLink{e.URL, "Event page"})
		}
	case "reminder":
		subject = "Reminder: " + title
		if when != "" {
			subject += ", " + when
		}
		d.Lines = append(d.Lines, "A reminder that "+title+" is coming up.")
		if when != "" {
			d.Lines = append(d.Lines, "When: "+when)
		}
		d.Lines = append(d.Lines, "Where: "+whereText(e))
		for _, t := range o.Tickets {
			d.Links = append(d.Links, pageLink{ticketURL(origin, signCode(key, kindTicket, t.Code)), "Your ticket (" + plainText(t.TypeName, false) + ")"})
		}
	case "thanks":
		subject = "Thank you for coming to " + title
		d.Lines = append(d.Lines, "Thank you for coming to "+title+". We hope to see you again.")
	default:
		return outMail{}, false, nil
	}
	var buf bytes.Buffer
	if err := autoTmpl.Execute(&buf, d); err != nil {
		return outMail{}, false, err
	}
	return outMail{To: o.Email, Subject: subject, Text: d.text(), HTML: buf.String()}, true, nil
}

// serveEmails answers GET /admin/emails: the switches and the pace the quota allows.
func serveEmails(u *sdk.HTTPUser) sdk.HTTPResponse {
	if u == nil || u.Role != "owner" {
		return forbidden()
	}
	a, err := loadAutomations()
	if err != nil {
		return unavailable()
	}
	q := mailQuota()
	campaigns, err := readCampaigns()
	if err != nil {
		campaigns = json.RawMessage(`[]`)
	}
	return jsonReply(http.StatusOK, map[string]any{
		"ok": true, "campaigns": campaigns,
		"automations": map[string]any{
			"changed": a.Changed, "reminder": a.Reminder, "reminder_hours": a.ReminderHours, "thanks": a.Thanks,
		},
		"quota": q, "bulk_per_run": bulkBudget(q),
		"site_origin_ok": originOf("", loadSettings().SiteURL) != "",
	})
}

type automationsBody struct {
	Changed       bool `json:"changed"`
	Reminder      bool `json:"reminder"`
	ReminderHours int  `json:"reminder_hours"`
	Thanks        bool `json:"thanks"`
}

// serveSaveAutomations answers POST /admin/emails/automations.
func serveSaveAutomations(r sdk.HTTPRequest) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	var b automationsBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	if b.ReminderHours < 1 || b.ReminderHours > 168 {
		return failReply(http.StatusBadRequest, "bad request")
	}
	err := dbExec(`INSERT INTO meta (key, value) VALUES ('`+metaChanged+`', $1), ('`+metaReminder+`', $2), ('`+metaThanks+`', $3), ('`+metaReminderHours+`', $4)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		strconv.FormatBool(b.Changed), strconv.FormatBool(b.Reminder), strconv.FormatBool(b.Thanks), strconv.Itoa(b.ReminderHours))
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true})
}
