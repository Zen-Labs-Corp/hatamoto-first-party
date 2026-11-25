package main

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"

	"hatamoto.dev/sdk"
)

// pageData is every public HTML page of the plugin: a heading, facts, text
// lines, an optional QR code and links. The route CSP is a sandbox, so a page
// has no form and no script; actions are links.
type pageData struct {
	Title string
	Facts []pageFact
	Lines []string
	QR    template.URL
	Code  string
	Links []pageLink
}

type pageFact struct{ Label, Value string }

type pageLink struct{ Href, Text string }

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Title}}</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;margin:0;padding:24px;background:#f6f6f7;color:#1b1b1f}
main{max-width:440px;margin:0 auto;background:#fff;border-radius:12px;padding:24px;box-shadow:0 1px 4px rgba(0,0,0,.12)}
h1{font-size:1.4rem;margin:0 0 12px}
dl{margin:0 0 16px}dt{font-size:.8rem;text-transform:uppercase;color:#666;margin-top:8px}dd{margin:0}
img{display:block;margin:16px auto;width:240px;height:240px}
code{display:block;text-align:center;font-size:.95rem;letter-spacing:.04em}
a{color:#0b5fff}.btn{display:inline-block;margin:8px 8px 0 0;padding:8px 14px;border-radius:8px;background:#0b5fff;color:#fff;text-decoration:none}
</style></head><body><main>
<h1>{{.Title}}</h1>
{{range .Lines}}<p>{{.}}</p>
{{end}}{{if .Facts}}<dl>{{range .Facts}}<dt>{{.Label}}</dt><dd>{{.Value}}</dd>{{end}}</dl>
{{end}}{{if .QR}}<img src="{{.QR}}" alt="QR code of this ticket" width="240" height="240">
{{end}}{{if .Code}}<code>{{.Code}}</code>
{{end}}{{range .Links}}<a class="btn" href="{{.Href}}">{{.Text}}</a>
{{end}}</main></body></html>
`))

// htmlPage renders a page. It is never cached or shared.
func htmlPage(status int, p pageData) sdk.HTTPResponse {
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, p); err != nil {
		return plain(http.StatusInternalServerError, "unavailable")
	}
	return sdk.HTTPResponse{
		Status: status,
		Headers: map[string]string{
			"Content-Type":  "text/html; charset=utf-8",
			"Cache-Control": "no-store, private",
		},
		Body: buf.String(),
	}
}

func notFound() sdk.HTTPResponse { return plain(http.StatusNotFound, "not found") }

// ticketRow is a ticket as the page and the QR route read it.
type ticketRow struct {
	Status    string `json:"status"`
	EventID   string `json:"event_id"`
	Holder    string `json:"holder"`
	TypeName  string `json:"type_name"`
	EventName string `json:"event_name"`
}

// lookupTicket finds the ticket of a code; nil when it is unknown or the
// order was anonymised.
func lookupTicket(code string) (*ticketRow, error) {
	var row *ticketRow
	err := dbJSON(`SELECT coalesce((SELECT to_jsonb(x) FROM (
SELECT a.status, a.event_id, o.name AS holder, t.name AS type_name, coalesce(e.name, '') AS event_name
FROM attendees a
JOIN orders o ON o.id = a.order_id
JOIN ticket_types t ON t.id = a.type_id
LEFT JOIN events e ON e.entity_id = a.event_id
WHERE a.code = $1 AND o.anonymised_at IS NULL) x), 'null'::jsonb)`, &row, code)
	return row, err
}

// ticketContext finds what both ticket routes need: the verified ticket, its
// event (zero when the host cannot read it) and the origin links use.
func ticketContext(tok string) (code string, t *ticketRow, e event, origin string, ok bool) {
	key, err := hmacSecret()
	if err != nil {
		return "", nil, event{}, "", false
	}
	code, valid := verifyToken(key, kindTicket, tok)
	if !valid {
		return "", nil, event{}, "", false
	}
	t, err = lookupTicket(code)
	if err != nil || t == nil {
		return "", nil, event{}, "", false
	}
	s := loadSettings()
	// A host error leaves e empty: the ticket still shows, with the stored name.
	if ev, found, err := readEvent(t.EventID, s); err == nil && found {
		e = ev
	}
	return code, t, e, originOf(e.URL, s.SiteURL), true
}

// serveTicket is the page a ticket link opens. A forged, unknown and
// anonymised token all get the same answer.
func serveTicket(tok string) sdk.HTTPResponse {
	code, t, e, origin, ok := ticketContext(tok)
	if !ok {
		return notFound()
	}
	name := e.Name
	if name == "" {
		name = t.EventName
	}
	p := pageData{Title: name, Code: groupCode(code)}
	if p.Title == "" {
		p.Title = "Your ticket"
	}
	if !e.Start.IsZero() {
		p.Facts = append(p.Facts, pageFact{"When", whenText(e)})
	}
	p.Facts = append(p.Facts,
		pageFact{"Ticket", t.TypeName},
		pageFact{"Name", t.Holder},
		pageFact{"Status", ticketStatusText(t.Status)})
	if t.Status == "active" {
		p.QR = template.URL(qrDataURI(ticketURL(origin, tok)))
		if e.OnlineURL != "" && (e.Mode == "online" || e.Mode == "hybrid") {
			p.Links = append(p.Links, pageLink{e.OnlineURL, "Join online"})
		}
	}
	return htmlPage(http.StatusOK, p)
}

// ticketStatusText is the status of a ticket as the page says it. Only an
// active ticket has a QR code; a held, refunded or lapsed one is told apart
// from a cancelled one.
func ticketStatusText(status string) string {
	switch status {
	case "active":
		return "Active"
	case "pending":
		return "Awaiting payment"
	case "refunded":
		return "Refunded"
	case "expired":
		return "Expired"
	}
	return "Cancelled"
}

// serveTicketQR answers the QR image mails embed.
func serveTicketQR(tok string) sdk.HTTPResponse {
	_, _, _, origin, ok := ticketContext(tok)
	if !ok {
		return notFound()
	}
	b, err := qrPNG(ticketURL(origin, tok), qrSize)
	if err != nil {
		return notFound()
	}
	return sdk.HTTPResponse{
		Status: http.StatusOK,
		Headers: map[string]string{
			"Content-Type":  "image/png",
			"Cache-Control": "public, max-age=86400, immutable",
		},
		BodyB64: base64.StdEncoding.EncodeToString(b),
	}
}

// groupCode writes a 32-character code as four groups of eight.
func groupCode(code string) string {
	var parts []string
	for len(code) > 8 {
		parts = append(parts, code[:8])
		code = code[8:]
	}
	return strings.Join(append(parts, code), " ")
}

// whenText is the start (and end) of an event in its own zone.
func whenText(e event) string {
	layout := "Mon 2 Jan 2006, 15:04 MST"
	if e.AllDay {
		layout = "Mon 2 Jan 2006"
	}
	s := e.Start.Format(layout)
	if e.HasEnd && !e.AllDay {
		s += " to " + e.End.Format("15:04 MST")
		if e.End.YearDay() != e.Start.YearDay() || e.End.Year() != e.Start.Year() {
			s = e.Start.Format(layout) + " to " + e.End.Format(layout)
		}
	}
	return s
}
