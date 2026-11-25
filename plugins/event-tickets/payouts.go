package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// The Payouts tab is a report the owner acts on by hand: what each payee is
// owed for one UTC month of paid orders. No money moves here. Amounts are
// integer minor units, never floats.

var periodRe = regexp.MustCompile(`^(20[0-9]{2})-(0[1-9]|1[0-2])$`)

// periodRange is the UTC calendar month p ("2026-06") as [start, end).
func periodRange(p string) (start, end time.Time, ok bool) {
	if !periodRe.MatchString(p) {
		return start, end, false
	}
	start, err := time.Parse("2006-01", p)
	if err != nil {
		return start, end, false
	}
	return start, start.AddDate(0, 1, 0), true
}

// eventAgg is what the paid orders of one event brought in during a period,
// in one currency. Base is the sum of each order's total minus refunds and the
// plugin's own fee, never below zero per order. Tickets are the paid-for
// tickets that were not refunded.
type eventAgg struct {
	EventID   string `json:"event_id"`
	Currency  string `json:"currency"`
	EventName string `json:"event_name"`
	Gross     int64  `json:"gross"`
	Refunds   int64  `json:"refunds"`
	Fees      int64  `json:"fees"`
	Base      int64  `json:"base"`
	Tickets   int64  `json:"tickets"`
}

// payoutRecord is a payout the owner marked as paid.
type payoutRecord struct {
	Email     string `json:"email"`
	Name      string `json:"name"`
	Currency  string `json:"currency"`
	Owed      int64  `json:"owed"`
	PaidOn    string `json:"paid_on"`
	Reference string `json:"reference"`
}

type payeeShare struct {
	Kind  string `json:"kind"` // "percent" | "fixed" | "mixed"
	Value int64  `json:"value,omitempty"`
}

type paidInfo struct {
	PaidOn    string `json:"paid_on"`
	Reference string `json:"reference"`
	PaidCents int64  `json:"paid_cents"`
	Changed   bool   `json:"changed"` // the amount owed now differs from what was paid
}

// payeeRow is one line of the report: a payee in one currency.
type payeeRow struct {
	Name     string     `json:"name"`
	Email    string     `json:"email"`
	Currency string     `json:"currency"`
	Events   int        `json:"events"`
	Gross    int64      `json:"gross"`
	Refunds  int64      `json:"refunds"`
	Fees     int64      `json:"fees"`
	Share    payeeShare `json:"share"`
	Owed     int64      `json:"owed"`
	Paid     *paidInfo  `json:"paid"`
}

// ruleOwed is what one rule earns from one event's counted orders. A percent
// rule rounds half up; a fixed rule never takes more than the base.
func ruleOwed(r splitRule, a eventAgg) int64 {
	if a.Base <= 0 {
		return 0
	}
	if r.Kind == "percent" {
		return (a.Base*r.Value + 5000) / 10000
	}
	return min(a.Tickets*r.Value, a.Base)
}

// buildPayouts applies the rules (by event id) to the aggregates and groups
// the result per payee address and currency. Paid records are attached; one
// with no current line still shows, owed 0 and flagged as changed.
func buildPayouts(aggs []eventAgg, rules map[string][]splitRule, paid []payoutRecord) []payeeRow {
	type acc struct {
		row    payeeRow
		events map[string]bool
		share  payeeShare
	}
	byKey := map[string]*acc{}
	var order []string
	for _, a := range aggs {
		for _, r := range rules[a.EventID] {
			email := strings.ToLower(strings.TrimSpace(r.Email))
			key := email + "|" + a.Currency
			p := byKey[key]
			if p == nil {
				p = &acc{row: payeeRow{Name: r.Name, Email: email, Currency: a.Currency}, events: map[string]bool{}, share: payeeShare{Kind: r.Kind, Value: r.Value}}
				byKey[key] = p
				order = append(order, key)
			} else if p.share.Kind != r.Kind || p.share.Value != r.Value {
				p.share = payeeShare{Kind: "mixed"}
			}
			if !p.events[a.EventID] {
				p.events[a.EventID] = true
				p.row.Gross += a.Gross
				p.row.Refunds += a.Refunds
				p.row.Fees += a.Fees
			}
			p.row.Owed += ruleOwed(r, a)
		}
	}
	rows := make([]payeeRow, 0, len(order))
	for _, k := range order {
		p := byKey[k]
		p.row.Events = len(p.events)
		p.row.Share = p.share
		rows = append(rows, p.row)
	}
	for _, rec := range paid {
		email := strings.ToLower(strings.TrimSpace(rec.Email))
		i := slices.IndexFunc(rows, func(r payeeRow) bool { return r.Email == email && r.Currency == rec.Currency })
		if i < 0 {
			rows = append(rows, payeeRow{Name: rec.Name, Email: email, Currency: rec.Currency, Share: payeeShare{Kind: "mixed"}})
			i = len(rows) - 1
		}
		rows[i].Paid = &paidInfo{PaidOn: rec.PaidOn, Reference: rec.Reference, PaidCents: rec.Owed, Changed: rec.Owed != rows[i].Owed}
	}
	slices.SortStableFunc(rows, func(a, b payeeRow) int {
		if c := strings.Compare(a.Currency, b.Currency); c != 0 {
			return c
		}
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Email, b.Email)
	})
	return rows
}

// readPayouts reads one month and builds the report. heldBack counts the
// orders of the month left out because they carry a problem flag.
func readPayouts(period string) (rows []payeeRow, heldBack int, err error) {
	start, end, ok := periodRange(period)
	if !ok {
		return nil, 0, nil
	}
	from, to := start.Format(time.RFC3339), end.Format(time.RFC3339)
	var aggs []eventAgg
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.event_id, x.currency), '[]'::jsonb) FROM (
SELECT o.event_id, o.currency, coalesce(max(e.name), '') AS event_name,
  sum(o.total_cents) AS gross, sum(o.refunded_cents) AS refunds, sum(o.fee_cents) AS fees,
  sum(greatest(o.total_cents - o.refunded_cents - o.fee_cents, 0)) AS base,
  sum((SELECT count(*) FROM attendees a JOIN ticket_types t ON t.id = a.type_id
       WHERE a.order_id = o.id AND a.status <> 'refunded' AND t.price_cents > 0)) AS tickets
FROM orders o LEFT JOIN events e ON e.entity_id = o.event_id
WHERE o.paid_at >= $1::timestamptz AND o.paid_at < $2::timestamptz AND o.total_cents > 0
  AND o.currency IS NOT NULL AND o.problem IS NULL AND o.status IN ('paid', 'refunded', 'cancelled')
GROUP BY o.event_id, o.currency) x`, &aggs, from, to)
	if err != nil {
		return nil, 0, err
	}
	if err = dbJSON(`SELECT to_jsonb(count(*)) FROM orders WHERE paid_at >= $1::timestamptz AND paid_at < $2::timestamptz AND problem IS NOT NULL`, &heldBack, from, to); err != nil {
		return nil, 0, err
	}
	rules := map[string][]splitRule{}
	if len(aggs) > 0 {
		ids := make([]string, len(aggs))
		for i, a := range aggs {
			ids[i] = a.EventID
		}
		doc, _ := json.Marshal(ids)
		var list []struct {
			EventID string `json:"event_id"`
			splitRule
		}
		if err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.event_id, x.position, x.id), '[]'::jsonb) FROM (
SELECT id, event_id, payee_name AS name, payee_email AS email, kind, value, position FROM split_rules
WHERE event_id IN (SELECT jsonb_array_elements_text($1::jsonb))) x`, &list, string(doc)); err != nil {
			return nil, 0, err
		}
		for _, x := range list {
			rules[x.EventID] = append(rules[x.EventID], x.splitRule)
		}
	}
	paid := []payoutRecord{}
	if err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id), '[]'::jsonb) FROM (
SELECT id, payee_email AS email, payee_name AS name, currency, owed_cents AS owed,
  to_char(paid_on, 'YYYY-MM-DD') AS paid_on, reference FROM payouts WHERE period = $1) x`, &paid, period); err != nil {
		return nil, 0, err
	}
	return buildPayouts(aggs, rules, paid), heldBack, nil
}

// servePayouts answers GET /admin/payouts?period=YYYY-MM. Owner only: the
// amounts are money and the rows hold payout addresses.
func servePayouts(u *sdk.HTTPUser, query map[string][]string) sdk.HTTPResponse {
	if u == nil || u.Role != "owner" {
		return forbidden()
	}
	period := queryValue(query["period"])
	if !periodRe.MatchString(period) {
		return failReply(http.StatusBadRequest, "bad period")
	}
	rows, held, err := readPayouts(period)
	if err != nil {
		return unavailable()
	}
	exp := map[string]int{}
	for _, r := range rows {
		exp[r.Currency] = currencyExp(r.Currency)
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "period": period, "payees": rows, "held_back": held, "exponents": exp})
}

// shareText writes a payee's share for the CSV: "80%", "12.5%", "2.00 per
// ticket" or "mixed". Basis points are split by integer division, no float.
func shareText(s payeeShare, cur string) string {
	switch s.Kind {
	case "percent":
		out := strconv.FormatInt(s.Value/100, 10)
		if r := s.Value % 100; r != 0 {
			out += "." + strings.TrimRight(strconv.FormatInt(100+r, 10)[1:], "0")
		}
		return out + "%"
	case "fixed":
		return decimalAmount(s.Value, cur) + " per ticket"
	}
	return "mixed"
}

// payoutsCSV renders the report, one row per payee and currency. Every text
// cell goes through csvCell; money is decimal in the currency's own unit.
// It fails with errCSVTooLarge rather than cut the file short.
func payoutsCSV(period string, rows []payeeRow, maxBytes int) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"period", "payee", "email", "currency", "events", "gross", "refunds", "fees", "share", "owed", "status", "paid_on", "reference", "paid_amount"}); err != nil {
		return "", err
	}
	for _, r := range rows {
		status, paidOn, ref, paidAmount := "owed", "", "", ""
		if r.Paid != nil {
			status, paidOn, ref, paidAmount = "paid", r.Paid.PaidOn, r.Paid.Reference, decimalAmount(r.Paid.PaidCents, r.Currency)
			if r.Paid.Changed {
				status = "paid, changed"
			}
		}
		if err := w.Write([]string{
			period, csvCell(r.Name), csvCell(r.Email), r.Currency, strconv.Itoa(r.Events),
			decimalAmount(r.Gross, r.Currency), decimalAmount(r.Refunds, r.Currency), decimalAmount(r.Fees, r.Currency),
			csvCell(shareText(r.Share, r.Currency)), decimalAmount(r.Owed, r.Currency), status, paidOn, csvCell(ref), paidAmount,
		}); err != nil {
			return "", err
		}
		w.Flush()
		if buf.Len() > maxBytes {
			return "", errCSVTooLarge
		}
	}
	w.Flush()
	return buf.String(), w.Error()
}

var errCSVTooLarge = errors.New("csv too large")

// servePayoutsCSV answers GET /admin/payouts.csv?period=YYYY-MM as a
// download. Owner only.
func servePayoutsCSV(u *sdk.HTTPUser, query map[string][]string) sdk.HTTPResponse {
	if u == nil || u.Role != "owner" {
		return forbidden()
	}
	period := queryValue(query["period"])
	if !periodRe.MatchString(period) {
		return failReply(http.StatusBadRequest, "bad period")
	}
	rows, _, err := readPayouts(period)
	if err != nil {
		return unavailable()
	}
	body, err := payoutsCSV(period, rows, maxCSVBytes)
	if errors.Is(err, errCSVTooLarge) {
		return csvTooLarge()
	}
	if err != nil {
		return unavailable()
	}
	return sdk.HTTPResponse{Status: http.StatusOK, Body: body, Headers: map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": `attachment; filename="payouts-` + period + `.csv"`,
	}}
}

// markBody is the body of POST /admin/payouts/mark. Owed is the amount the
// owner saw; the server recomputes it and refuses a different one.
type markBody struct {
	Period    string `json:"period"`
	Email     string `json:"email"`
	Currency  string `json:"currency"`
	PaidOn    string `json:"paid_on"`
	Reference string `json:"reference"`
	Owed      int64  `json:"owed_cents"`
}

// serveMarkPaid answers POST /admin/payouts/mark: it records that a payee
// was paid by hand, with the amount owed at that moment as a snapshot.
func serveMarkPaid(r sdk.HTTPRequest) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	var b markBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	email := strings.ToLower(strings.TrimSpace(b.Email))
	paidOn, err := time.Parse("2006-01-02", b.PaidOn)
	ref := strings.TrimSpace(b.Reference)
	if _, ok := cleanText(ref, 80); ref != "" && !ok {
		return failReply(http.StatusBadRequest, "bad request")
	}
	if !periodRe.MatchString(b.Period) || !currencyRe.MatchString(b.Currency) || len(email) > maxEmailLen || !plainAddress(email) ||
		err != nil || paidOn.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || paidOn.After(time.Now().UTC().AddDate(0, 0, 1)) {
		return failReply(http.StatusBadRequest, "bad request")
	}
	rows, _, err := readPayouts(b.Period)
	if err != nil {
		return unavailable()
	}
	i := slices.IndexFunc(rows, func(p payeeRow) bool { return p.Email == email && p.Currency == b.Currency })
	if i < 0 || rows[i].Owed == 0 {
		return failReply(http.StatusConflict, "nothing is owed")
	}
	if rows[i].Owed != b.Owed {
		return failReply(http.StatusConflict, "the amount changed, reload")
	}
	var inserted bool
	err = dbJSON(`WITH i AS (INSERT INTO payouts (period, payee_email, payee_name, currency, owed_cents, paid_on, reference, marked_by)
VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8) ON CONFLICT (period, payee_email, currency) DO NOTHING RETURNING id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM i))`, &inserted, b.Period, email, rows[i].Name, b.Currency, rows[i].Owed, b.PaidOn, ref, r.User.ID)
	if err != nil {
		return unavailable()
	}
	if !inserted {
		return failReply(http.StatusConflict, "already marked as paid")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true})
}

// unmarkBody is the body of POST /admin/payouts/unmark.
type unmarkBody struct {
	Period   string `json:"period"`
	Email    string `json:"email"`
	Currency string `json:"currency"`
}

// serveUnmarkPaid answers POST /admin/payouts/unmark: it removes a paid
// record, so a wrong mark can be undone.
func serveUnmarkPaid(r sdk.HTTPRequest) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	var b unmarkBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	email := strings.ToLower(strings.TrimSpace(b.Email))
	if !periodRe.MatchString(b.Period) || !currencyRe.MatchString(b.Currency) || len(email) > maxEmailLen || !plainAddress(email) {
		return failReply(http.StatusBadRequest, "bad request")
	}
	var deleted bool
	err := dbJSON(`WITH d AS (DELETE FROM payouts WHERE period = $1 AND payee_email = $2 AND currency = $3 RETURNING id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM d))`, &deleted, b.Period, email, b.Currency)
	if err != nil {
		return unavailable()
	}
	if !deleted {
		return failReply(http.StatusNotFound, "not found")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true})
}
