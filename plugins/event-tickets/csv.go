package main

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	// maxCSVBytes keeps the file well under the 1 MiB answer cap of a plugin
	// call, leaving room for the JSON escaping of the body.
	maxCSVBytes = 700 << 10
	maxCSVRows  = 10000
	csvBatch    = 500
)

// csvCell neutralises a cell a spreadsheet would run as a formula: one that
// starts with = + - @ or a tab or carriage return gets a leading quote.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// csvRow is one ticket as the export reads it.
type csvRow struct {
	AttendeeID int64             `json:"attendee_id"`
	OrderID    int64             `json:"order_id"`
	TicketType string            `json:"ticket_type"`
	Name       string            `json:"name"`
	Email      string            `json:"email"`
	Status     string            `json:"status"`
	BookedAt   time.Time         `json:"booked_at"`
	Code       string            `json:"code"`
	Seat       string            `json:"seat"`
	Answers    map[string]string `json:"answers"`
}

// attendeesCSV renders the header and rows, one column per custom field. It
// stops before the output would pass maxBytes and reports how many rows it
// wrote. Every text cell, field labels included, goes through csvCell.
func attendeesCSV(fields []field, rows []csvRow, maxBytes int) (string, int, error) {
	head := []string{"attendee_id", "order_id", "ticket_type", "name", "email", "status", "booked_at", "ticket_code", "seat"}
	for _, f := range fields {
		head = append(head, csvCell(f.Label))
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(head); err != nil {
		return "", 0, err
	}
	for i, r := range rows {
		rec := []string{
			strconv.FormatInt(r.AttendeeID, 10), strconv.FormatInt(r.OrderID, 10), csvCell(r.TicketType), csvCell(r.Name), csvCell(r.Email),
			r.Status, r.BookedAt.UTC().Format(time.RFC3339), r.Code, csvCell(r.Seat),
		}
		for _, f := range fields {
			rec = append(rec, csvCell(r.Answers[f.Key]))
		}
		w.Flush()
		before := buf.Len()
		if err := w.Write(rec); err != nil {
			return "", 0, err
		}
		w.Flush()
		if buf.Len() > maxBytes {
			buf.Truncate(before)
			return buf.String(), i, w.Error()
		}
	}
	w.Flush()
	return buf.String(), len(rows), w.Error()
}

// csvBatchRows reads the next rows after the given attendee id.
func csvBatchRows(eventID string, after int64) ([]csvRow, error) {
	var rows []csvRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.attendee_id), '[]'::jsonb) FROM (
SELECT a.id AS attendee_id, a.order_id, t.name AS ticket_type,
  CASE WHEN o.anonymised_at IS NULL THEN o.name ELSE '(removed)' END AS name, o.email, a.status,
  a.created_at AS booked_at, a.code, coalesce(a.seat_label, '') AS seat, o.answers
FROM attendees a JOIN orders o ON o.id = a.order_id JOIN ticket_types t ON t.id = a.type_id
WHERE a.event_id = $1 AND a.id > $2 ORDER BY a.id LIMIT $3) x`, &rows, eventID, after, csvBatch)
	return rows, err
}

// serveAttendeesCSV answers GET /admin/events/{id}/attendees.csv as a
// download: up to maxCSVRows tickets and about maxCSVBytes bytes. An event
// that does not fit is refused with 413 rather than cut short, because core
// passes no custom header and a partial file would look complete.
func serveAttendeesCSV(u *sdk.HTTPUser, eventID string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	if !idOK(eventID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	fields, err := listFields(eventID)
	if err != nil {
		return unavailable()
	}
	var rows []csvRow
	for len(rows) <= maxCSVRows {
		var after int64
		if len(rows) > 0 {
			after = rows[len(rows)-1].AttendeeID
		}
		batch, err := csvBatchRows(eventID, after)
		if err != nil {
			return unavailable()
		}
		rows = append(rows, batch...)
		if len(batch) < csvBatch {
			break
		}
	}
	if len(rows) > maxCSVRows {
		return csvTooLarge()
	}
	body, n, err := attendeesCSV(fields, rows, maxCSVBytes)
	if err != nil {
		return unavailable()
	}
	if n < len(rows) {
		return csvTooLarge()
	}
	return sdk.HTTPResponse{Status: http.StatusOK, Body: body, Headers: map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": `attachment; filename="attendees.csv"`,
	}}
}

func csvTooLarge() sdk.HTTPResponse {
	return failReply(http.StatusRequestEntityTooLarge, "too many tickets for one file: cancel old tickets or ask for a split export")
}
