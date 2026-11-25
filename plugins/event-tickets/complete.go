package main

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// completePayment is the one path that turns a payment into paid tickets,
// whichever provider and whichever message (webhook, return page) brought it.
// It is idempotent: the pending -> paid change is one guarded statement, and
// the confirmation mail is sent only by the call that made the change.

// Outcomes of completePayment.
const (
	outcomePaid      = "paid"
	outcomeDuplicate = "duplicate" // already paid, refunded or not ours to change
	outcomeMismatch  = "mismatch"  // amount or currency is not the order's
	outcomeRefunded  = "refunded"  // late payment, no seats: paid back
	outcomeUnknown   = "unknown"   // no such order
)

// errRefundFailed is the fixed error of a late payment that could not be paid back.
var errRefundFailed = errors.New("refund failed")

// orderState is what completePayment reads when the transition did not happen.
type orderState struct {
	Status string `json:"status"`
	Total  int64  `json:"total_cents"`
	Cur    string `json:"currency"`
	Paid   bool   `json:"paid"`
}

// payOrder flips a pending order to paid and its pending tickets to active in
// one statement. It reports whether this call made the change.
func payOrder(orderID int64, provider, paymentRef string, amount int64, cur string) (bool, error) {
	var done bool
	err := dbJSON(`WITH o AS (UPDATE orders SET status = 'paid', paid_at = now(), payment_ref = $2, problem = NULL
    WHERE id = $1 AND status = 'pending' AND provider = $5 AND total_cents = $3 AND currency = $4 RETURNING id),
 a AS (UPDATE attendees SET status = 'active' WHERE order_id IN (SELECT id FROM o) AND status = 'pending' RETURNING id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM o))`, &done, orderID, paymentRef, amount, cur, provider)
	return done, err
}

func readOrderState(orderID int64) (*orderState, error) {
	var st *orderState
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('status', status, 'total_cents', total_cents,
    'currency', coalesce(currency, ''), 'paid', paid_at IS NOT NULL) FROM orders WHERE id = $1), 'null'::jsonb)`, &st, orderID)
	return st, err
}

// completePayment records a payment of amount (minor units) in cur for an
// order. paymentRef is the Stripe payment intent or the PayPal capture id.
//
//   - pending: tickets become active, one confirmation mail, outcomePaid.
//   - already paid: outcomeDuplicate, nothing written, no mail.
//   - amount or currency differ: problem = amount_mismatch, outcomeMismatch,
//     no tickets, no mail; the owner decides.
//   - expired or cancelled (the hold ended before the payment arrived): the
//     seats are taken again if they are free, else the payment is paid back.
//
// A non-nil error means the caller should answer "try again" (the provider
// retries): a database error, or a refund that could not be made.
func completePayment(orderID int64, provider, paymentRef string, amount int64, cur string) (string, error) {
	cur = strings.ToUpper(cur)
	done, err := payOrder(orderID, provider, paymentRef, amount, cur)
	if err != nil {
		return "", err
	}
	if done {
		sendNow(kindConfirm, orderID)
		return outcomePaid, nil
	}
	st, err := readOrderState(orderID)
	if err != nil {
		return "", err
	}
	switch {
	case st == nil:
		return outcomeUnknown, nil
	case st.Paid || (st.Status != "pending" && st.Status != "expired" && st.Status != "cancelled"):
		return outcomeDuplicate, nil
	case st.Status == "pending" || st.Total != amount || st.Cur != cur:
		// still pending means the provider did not match; otherwise the amount or currency
		if err := dbExec(`UPDATE orders SET problem = 'amount_mismatch' WHERE id = $1 AND status IN ('pending', 'expired', 'cancelled') AND paid_at IS NULL`, orderID); err != nil {
			return "", err
		}
		return outcomeMismatch, nil
	}
	return lateAfterExpiry(orderID, provider, paymentRef, amount, cur)
}

// lateAfterExpiry handles a payment for an order whose hold ended: take the
// seats again and finish the payment, or pay the buyer back. Another request
// for the same order may be doing the same at this moment, so the refund is
// claimed in the database first and only the claim's winner calls the provider.
// A loser tries the payment again: the order may have been taken back, and
// payOrder answers for it.
func lateAfterExpiry(orderID int64, provider, paymentRef string, amount int64, cur string) (string, error) {
	back, err := retakeHold(orderID)
	if err != nil {
		return "", err
	}
	if !back {
		claimed, err := claimLateRefund(orderID, paymentRef)
		if err != nil {
			return "", err
		}
		if claimed {
			return refundLate(orderID, provider, paymentRef, amount, cur)
		}
	}
	done, err := payOrder(orderID, provider, paymentRef, amount, cur)
	if err != nil {
		return "", err
	}
	if !done {
		return outcomeDuplicate, nil
	}
	sendNow(kindConfirm, orderID)
	return outcomePaid, nil
}

// claimLateRefund moves an expired or cancelled, never paid order to refunded
// before any money moves. Of concurrent requests exactly one gets true; the
// seats are not held, so taken is not touched.
func claimLateRefund(orderID int64, paymentRef string) (bool, error) {
	var done bool
	err := dbJSON(`WITH o AS (UPDATE orders SET status = 'refunded', refunded_cents = total_cents, payment_ref = $2, problem = NULL
    WHERE id = $1 AND status IN ('expired', 'cancelled') AND paid_at IS NULL RETURNING id),
 a AS (UPDATE attendees SET status = 'refunded' WHERE order_id IN (SELECT id FROM o) AND status = 'expired' RETURNING id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM o))`, &done, orderID, paymentRef)
	return done, err
}

// refundLate pays a claimed late payment back in full. The idempotency key is
// the same on every try, so a retry after a failure reuses the refund. When the
// provider refuses, the claim is undone (the order is expired again) and marked
// refund_failed for the owner, and the error is returned so the provider sends
// the message again.
func refundLate(orderID int64, provider, paymentRef string, amount int64, cur string) (string, error) {
	s := loadSettings()
	key := "et-" + strconv.FormatInt(orderID, 10) + "-late-refund"
	var err error
	switch provider {
	case "stripe":
		_, err = stripeRefund(s, paymentRef, amount, key)
	case "paypal":
		_, err = paypalRefund(s, paymentRef, amount, cur, key, newDeadline(5*time.Second))
	default:
		err = errRefundFailed
	}
	if err != nil {
		_ = dbExec(`WITH o AS (UPDATE orders SET status = 'expired', problem = 'refund_failed', refunded_cents = 0, payment_ref = $2
    WHERE id = $1 AND status = 'refunded' AND paid_at IS NULL RETURNING id)
UPDATE attendees SET status = 'expired' WHERE order_id IN (SELECT id FROM o) AND status = 'refunded'`, orderID, paymentRef)
		return "", errRefundFailed
	}
	sendNow(kindRefund, orderID)
	return outcomeRefunded, nil
}

// completeFree marks a pending order whose total is zero (a coupon covered
// everything) as paid and activates its tickets, in one statement. It reports
// whether this call made the change, so the confirmation is sent once.
func completeFree(orderID int64) (bool, error) {
	var done bool
	err := dbJSON(`WITH o AS (UPDATE orders SET status = 'paid', paid_at = now()
    WHERE id = $1 AND status = 'pending' AND total_cents = 0 RETURNING id),
 a AS (UPDATE attendees SET status = 'active' WHERE order_id IN (SELECT id FROM o) AND status = 'pending' RETURNING id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM o))`, &done, orderID)
	return done, err
}
