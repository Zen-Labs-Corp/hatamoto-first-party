package main

import (
	"net/http"
	"strconv"

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

// handleCSV answers up to maxCSVRows payments as a download.
func handleCSV(sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var rows []payment
	for len(rows) < maxCSVRows {
		batch, err := listRange(csvBatch, len(rows))
		if err != nil {
			return busy()
		}
		rows = append(rows, batch...)
		if len(batch) < csvBatch {
			break
		}
	}
	body, err := paymentsCSV(rows[:min(len(rows), maxCSVRows)])
	if err != nil {
		return busy()
	}
	return sdk.HTTPResponse{Status: http.StatusOK, Body: body, Headers: map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": `attachment; filename="payments.csv"`,
	}}, nil
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
