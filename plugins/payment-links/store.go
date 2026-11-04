package main

import "time"

const (
	pageSize     = 50
	pruneBatch   = 1000
	pruneBatches = 5
	totalsMonths = 24
)

// payment is one confirmed Stripe payment.
type payment struct {
	EventID   string    `json:"event_id"`
	SessionID string    `json:"session_id"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Email     string    `json:"email"`
	Label     string    `json:"label"`
	Kind      string    `json:"kind"`
	LinkID    string    `json:"link_id"`
	Live      bool      `json:"live"`
	Created   time.Time `json:"created"`
}

// total is the sum of one currency over one UTC month.
type total struct {
	Month    string `json:"month"`
	Currency string `json:"currency"`
	Total    int64  `json:"total"`
	Count    int64  `json:"count"`
}

const (
	sqlEventSeen = `SELECT jsonb_build_object('seen', EXISTS (SELECT 1 FROM payments WHERE event_id = $1))`

	sqlInsertPayment = `INSERT INTO payments (event_id, session_id, amount, currency, customer_email, link_label, link_kind, payment_link_id, livemode, created)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, to_timestamp($10::double precision))
ON CONFLICT DO NOTHING`

	sqlListPayments = `SELECT coalesce(jsonb_agg(jsonb_build_object(
  'event_id', event_id, 'session_id', session_id, 'amount', amount, 'currency', currency,
  'email', customer_email, 'label', link_label, 'kind', link_kind, 'link_id', payment_link_id,
  'live', livemode, 'created', to_char(created at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
) ORDER BY created DESC, id DESC), '[]'::jsonb)
FROM (SELECT * FROM payments ORDER BY created DESC, id DESC LIMIT $1 OFFSET $2) p`

	sqlListBetween = `SELECT coalesce(jsonb_agg(jsonb_build_object(
  'event_id', event_id, 'session_id', session_id, 'amount', amount, 'currency', currency,
  'email', customer_email, 'label', link_label, 'kind', link_kind, 'link_id', payment_link_id,
  'live', livemode, 'created', to_char(created at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
) ORDER BY created DESC, id DESC), '[]'::jsonb)
FROM (SELECT * FROM payments
  WHERE created >= to_timestamp($3::double precision) AND created < to_timestamp($4::double precision)
  ORDER BY created DESC, id DESC LIMIT $1 OFFSET $2) p`

	sqlMonthTotals = `SELECT coalesce(jsonb_agg(jsonb_build_object('month', month, 'currency', currency, 'total', total, 'count', n)
  ORDER BY month DESC, currency), '[]'::jsonb)
FROM (SELECT to_char(created at time zone 'utc', 'YYYY-MM') AS month, currency, sum(amount) AS total, count(*) AS n
  FROM payments
  WHERE created at time zone 'utc' >= date_trunc('month', now() at time zone 'utc') - make_interval(months => $1)
  GROUP BY 1, 2 ORDER BY 1 DESC, 2 LIMIT 240) t`

	sqlPrune = `DELETE FROM payments WHERE id IN (
  SELECT id FROM payments WHERE created < now() - make_interval(months => $1) ORDER BY created LIMIT $2)`
)

// eventSeen reports whether a Stripe event id is already recorded.
func eventSeen(id string) (bool, error) {
	var out struct {
		Seen bool `json:"seen"`
	}
	if err := dbJSON(sqlEventSeen, &out, id); err != nil {
		return false, err
	}
	return out.Seen, nil
}

// insertPayment stores p and reports whether a row was added. A repeated event
// or a second event for the same checkout session inserts nothing.
func insertPayment(p payment) (bool, error) {
	n, err := dbExecN(sqlInsertPayment, p.EventID, p.SessionID, p.Amount, p.Currency, p.Email,
		p.Label, p.Kind, p.LinkID, p.Live, p.Created.Unix())
	return n > 0, err
}

// listRange returns up to limit payments, newest first, after skipping offset.
func listRange(limit, offset int) ([]payment, error) {
	rows := []payment{}
	err := dbJSON(sqlListPayments, &rows, limit, offset)
	return rows, err
}

// listBetween is listRange limited to payments created in [from, to).
func listBetween(limit, offset int, from, to time.Time) ([]payment, error) {
	rows := []payment{}
	err := dbJSON(sqlListBetween, &rows, limit, offset, from.Unix(), to.Unix())
	return rows, err
}

// listPayments returns one page (1-based) and whether older rows exist.
func listPayments(page int) ([]payment, bool, error) {
	rows, err := listRange(pageSize+1, (page-1)*pageSize)
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > pageSize
	if more {
		rows = rows[:pageSize]
	}
	return rows, more, nil
}

// monthTotals returns the totals per UTC month and currency for the last
// totalsMonths months, newest first.
func monthTotals() ([]total, error) {
	out := []total{}
	err := dbJSON(sqlMonthTotals, &out, totalsMonths-1)
	return out, err
}

// prune deletes payments older than months, in bounded batches. It stops after
// a short batch, so a repeated run finds nothing left to do.
func prune(months int) error {
	for range pruneBatches {
		n, err := dbExecN(sqlPrune, months, pruneBatch)
		if err != nil {
			return err
		}
		if n < pruneBatch {
			return nil
		}
	}
	return nil
}
