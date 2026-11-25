package main

import (
	"net/http"
	"time"

	"hatamoto.dev/sdk"
)

// The pages a buyer lands on after paying or cancelling at the provider. They
// are GET and links only (the route CSP is a sandbox: no forms, no scripts).
// The link carries a token signed by the plugin over the order's pay code, so
// only the buyer the provider redirected can open it.

const (
	holdExpireCalls  = 3                       // Stripe sessions closed per cron run, at most
	holdExpireBudget = 1600 * time.Millisecond // all of them together; the cron has a 10 s limit
)

func init() {
	onHoldsExpired = func(sessions []string) {
		s := loadSettings()
		if s.StripeKey == "" {
			return
		}
		dl := newDeadline(holdExpireBudget)
		for _, id := range sessions[:min(len(sessions), holdExpireCalls)] {
			_ = stripeExpireSessionBy(s, id, dl) // best effort: a late payment is handled anyway
		}
	}
}

// payOrderRow is an order as the return and cancel pages read it.
type payOrderRow struct {
	ID       int64  `json:"id"`
	Status   string `json:"status"`
	Provider string `json:"provider"`
	Ref      string `json:"provider_ref"`
	EventID  string `json:"event_id"`
	PayCode  string `json:"pay_code"`
}

// orderByToken verifies a return or cancel token and reads its order.
func orderByToken(tok string) (key []byte, o *payOrderRow, ok bool) {
	key, err := hmacSecret()
	if err != nil {
		return nil, nil, false
	}
	payCode, valid := verifyToken(key, kindOrder, tok)
	if !valid {
		return nil, nil, false
	}
	err = dbJSON(`SELECT coalesce((SELECT jsonb_build_object('id', id, 'status', status, 'provider', coalesce(provider, ''),
    'provider_ref', coalesce(provider_ref, ''), 'event_id', event_id, 'pay_code', pay_code)
FROM orders WHERE pay_code = $1), 'null'::jsonb)`, &o, payCode)
	if err != nil || o == nil {
		return nil, nil, false
	}
	return key, o, true
}

// eventLink is the link back to the event page, none when it cannot be read.
func eventLink(eventID string) []pageLink {
	if e, ok, _ := readEvent(eventID, loadSettings()); ok && e.URL != "" {
		return []pageLink{{e.URL, "Back to the event"}}
	}
	return nil
}

func readOrderStatus(id int64) (string, error) {
	st, err := readOrderState(id)
	if err != nil || st == nil {
		return "", err
	}
	return st.Status, nil
}

// servePayReturn is where the provider sends the buyer after paying. For Stripe
// the order is reconciled here when the webhook has not arrived yet.
func servePayReturn(tok string, query map[string][]string) sdk.HTTPResponse {
	key, o, ok := orderByToken(tok)
	if !ok {
		return notFound()
	}
	status := o.Status
	if o.Provider == "stripe" && (status == "pending" || status == "expired") && queryValue(query["session_id"]) == o.Ref {
		if st, err := reconcileStripe(o); err == nil && st != "" {
			status = st
		}
	}
	back := eventLink(o.EventID)
	switch status {
	case "paid", "confirmed":
		return paidPage(key, o.ID, back)
	case "refunded":
		return htmlPage(http.StatusOK, pageData{Title: "Order refunded", Lines: []string{"This order was refunded. The tickets are no longer valid."}, Links: back})
	case "pending":
		return htmlPage(http.StatusOK, pageData{Title: "Confirming your payment", Lines: []string{
			"We are confirming your payment. Your tickets are sent by email as soon as it is confirmed. This page can be reloaded.",
		}, Links: back})
	}
	return htmlPage(http.StatusOK, pageData{Title: "Reservation ended", Lines: []string{"Your reservation ended. You were not charged."}, Links: back})
}

// reconcileStripe asks Stripe whether the order's session was paid and, if so,
// completes the order. It returns the order's status afterwards.
func reconcileStripe(o *payOrderRow) (string, error) {
	s := loadSettings()
	sess, err := stripeGetSession(s, o.Ref)
	if err != nil || sess.PaymentStatus != "paid" || sess.ClientReference != o.PayCode {
		return "", err
	}
	if _, err := completePayment(o.ID, "stripe", sess.PaymentIntent, sess.AmountTotal, sess.Currency); err != nil {
		return "", err
	}
	return readOrderStatus(o.ID)
}

// paidPage thanks the buyer and links the ticket pages.
func paidPage(key []byte, orderID int64, back []pageLink) sdk.HTTPResponse {
	var tickets []struct {
		Code     string `json:"code"`
		TypeName string `json:"type_name"`
	}
	err := dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('code', a.code, 'type_name', t.name) ORDER BY a.id), '[]'::jsonb)
FROM attendees a JOIN ticket_types t ON t.id = a.type_id WHERE a.order_id = $1 AND a.status = 'active'`, &tickets, orderID)
	if err != nil {
		return plain(http.StatusServiceUnavailable, "unavailable")
	}
	p := pageData{Title: "Payment received", Lines: []string{"Thank you. Your tickets are below and are sent to you by email."}}
	for _, t := range tickets {
		p.Links = append(p.Links, pageLink{ticketPath(signCode(key, kindTicket, t.Code)), "Open ticket: " + plainText(t.TypeName, false)})
	}
	p.Links = append(p.Links, back...)
	return htmlPage(http.StatusOK, p)
}

// servePayCancel is where the provider sends a buyer who backed out. It gives the
// seats back, but only of an order that is still unpaid.
func servePayCancel(tok string) sdk.HTTPResponse {
	_, o, ok := orderByToken(tok)
	if !ok {
		return notFound()
	}
	if o.Status == "pending" {
		if _, err := releaseHold(o.ID, "cancelled"); err != nil {
			return plain(http.StatusServiceUnavailable, "unavailable")
		}
	}
	st, err := readOrderStatus(o.ID)
	if err != nil {
		return plain(http.StatusServiceUnavailable, "unavailable")
	}
	back := eventLink(o.EventID)
	title, line := "Payment cancelled", "Your payment was cancelled and you were not charged."
	if st == "paid" || st == "refunded" {
		title, line = "Order already paid", "This order was already paid, so it was not cancelled. Use the link in your confirmation email."
	}
	return htmlPage(http.StatusOK, pageData{Title: title, Lines: []string{line}, Links: back})
}
