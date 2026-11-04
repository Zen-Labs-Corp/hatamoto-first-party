package main

import (
	"net/http"
	"strconv"
	"time"

	"hatamoto.dev/sdk"
)

const (
	maxPage        = 10000
	maxCSVRows     = 10000
	csvBatch       = 500 // rows per query: one jsonb cell stays well under the 1 MiB reply cap
	webhookPath    = "/p/payment-links/webhook"
	errUnavailable = "unavailable"
)

// paymentView is a payment as the admin page shows it.
type paymentView struct {
	payment
	Display string `json:"display"`
}

// totalView is a month total as the admin page shows it.
type totalView struct {
	total
	Display string `json:"display"`
}

func busy() (sdk.HTTPResponse, error) {
	return reply(http.StatusServiceUnavailable, map[string]string{"error": errUnavailable})
}

// handlePayments answers one page of the log plus the monthly totals. A
// database problem is a 503, never a Go error.
func handlePayments(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	page := 1
	if n, err := strconv.Atoi(first(r.Query["page"])); err == nil && n >= 1 {
		page = min(n, maxPage)
	}
	rows, more, err := listPayments(page)
	if err != nil {
		return busy()
	}
	totals, err := monthTotals()
	if err != nil {
		return busy()
	}
	pv := make([]paymentView, len(rows))
	for i, p := range rows {
		pv[i] = paymentView{p, formatAmount(p.Amount, p.Currency)}
	}
	tv := make([]totalView, len(totals))
	for i, t := range totals {
		tv[i] = totalView{t, formatAmount(t.Total, t.Currency)}
	}
	return reply(http.StatusOK, map[string]any{"payments": pv, "page": page, "has_more": more, "totals": tv})
}

// handleCSV answers payments as a download: up to maxCSVRows rows and about
// maxCSVBytes bytes, newest first. The query from and to (YYYY-MM, both
// inclusive) pick the months, so a larger log can be exported in parts. A log
// that does not fit is refused with 413 rather than cut short, because core
// passes no custom header and a partial file would look complete.
func handleCSV(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	from, to, ok := csvWindow(first(r.Query["from"]), first(r.Query["to"]))
	if !ok {
		return reply(http.StatusBadRequest, map[string]string{"error": "bad request"})
	}
	var rows []payment
	for len(rows) <= maxCSVRows {
		batch, err := listBetween(csvBatch, len(rows), from, to)
		if err != nil {
			return busy()
		}
		rows = append(rows, batch...)
		if len(batch) < csvBatch {
			break
		}
	}
	if len(rows) > maxCSVRows {
		return tooLarge()
	}
	body, n, err := paymentsCSV(rows, maxCSVBytes)
	if err != nil {
		return busy()
	}
	if n < len(rows) {
		return tooLarge()
	}
	return sdk.HTTPResponse{Status: http.StatusOK, Body: body, Headers: map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": `attachment; filename="payments.csv"`,
	}}, nil
}

// tooLarge refuses an export that would not fit, and says how to split it.
func tooLarge() (sdk.HTTPResponse, error) {
	return reply(http.StatusRequestEntityTooLarge, map[string]string{
		"error": "too many payments for one file: export by month range, for example payments.csv?from=2026-01&to=2026-06 (YYYY-MM)",
	})
}

// csvWindow turns the from and to months (empty means open) into a half-open
// UTC range [from, to).
func csvWindow(fromQ, toQ string) (from, to time.Time, ok bool) {
	from, to = time.Unix(0, 0).UTC(), time.Date(9000, 1, 1, 0, 0, 0, 0, time.UTC)
	var err error
	if fromQ != "" {
		if from, err = time.Parse("2006-01", fromQ); err != nil {
			return from, to, false
		}
	}
	if toQ != "" {
		t, err := time.Parse("2006-01", toQ)
		if err != nil {
			return from, to, false
		}
		to = t.AddDate(0, 1, 0)
	}
	return from, to, true
}

// handleLinks answers the setup status of the admin page. It says whether a
// key is set, never what it is.
func handleLinks(sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return busy()
	}
	links := s.Links
	if links == nil {
		links = []link{}
	}
	return reply(http.StatusOK, map[string]any{
		"key_set":          s.SecretKey != "",
		"webhook_path":     webhookPath,
		"links":            links,
		"notify":           s.notifyOn(),
		"notify_email_set": s.NotifyEmail != "",
	})
}
