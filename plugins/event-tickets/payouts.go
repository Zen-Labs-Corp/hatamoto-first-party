package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
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
