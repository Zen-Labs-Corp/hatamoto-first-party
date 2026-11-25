package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func (w *shop) orderToken() string { return signCode(w.key, kindOrder, w.payCode) }

func (w *shop) page(t *testing.T, pattern, tok string, query map[string][]string) sdk.HTTPResponse {
	t.Helper()
	return publicGet(t, pattern, map[string]string{"token": tok}, query)
}

func (w *shop) sessionPaid(amount int64) {
	w.answers["GET https://api.stripe.com/v1/checkout/sessions/cs_test_1"] = [2]string{"200",
		`{"id":"cs_test_1","status":"complete","payment_status":"paid","amount_total":` + itoa(amount) + `,"currency":"usd","payment_intent":"pi_1","client_reference_id":"` + w.payCode + `"}`}
}

func TestReturnPageReconcilesAPaidStripeSession(t *testing.T) {
	w := newShop(t)
	w.sessionPaid(2030)
	r := w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"session_id": {"cs_test_1"}})
	if r.Status != 200 || !strings.Contains(r.Body, "Payment received") || w.status != "paid" {
		t.Fatalf("%d, order %q: %s", r.Status, w.status, r.Body)
	}
	if !strings.Contains(r.Body, `href="/p/event-tickets/ticket/`+shopCode+".") || !strings.Contains(r.Body, "Open ticket: GA") {
		t.Errorf("no ticket link: %s", r.Body)
	}
	if r.Headers["Cache-Control"] != "no-store, private" {
		t.Errorf("cache: %v", r.Headers)
	}
	if len(w.mails) != 1 {
		t.Fatalf("%d mails", len(w.mails))
	}
	// the webhook for the same payment arrives afterwards: nothing more happens
	if code, status := w.deliver(t, w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd")); code != 200 || status != "duplicate" || len(w.mails) != 1 {
		t.Errorf("webhook after return: %d %q, %d mails", code, status, len(w.mails))
	}
	// and the page reloaded
	if r = w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"session_id": {"cs_test_1"}}); !strings.Contains(r.Body, "Payment received") || len(w.fetches) != 1 {
		t.Errorf("reload: %d provider calls", len(w.fetches))
	}
}

func TestReturnPageWaitsForAnUnpaidSession(t *testing.T) {
	w := newShop(t)
	w.answers["GET https://api.stripe.com/v1/checkout/sessions/cs_test_1"] = [2]string{"200",
		`{"id":"cs_test_1","status":"open","payment_status":"unpaid","amount_total":2030,"currency":"usd","client_reference_id":"` + w.payCode + `"}`}
	r := w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"session_id": {"cs_test_1"}})
	if r.Status != 200 || !strings.Contains(r.Body, "Confirming your payment") || w.status != "pending" || len(w.mails) != 0 || strings.Contains(r.Body, "/ticket/") {
		t.Fatalf("%d, order %q: %s", r.Status, w.status, r.Body)
	}
	// Stripe cannot be reached: the page still answers, the order stays pending
	delete(w.answers, "GET https://api.stripe.com/v1/checkout/sessions/cs_test_1")
	if r = w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"session_id": {"cs_test_1"}}); r.Status != 200 || !strings.Contains(r.Body, "Confirming") {
		t.Fatalf("%d: %s", r.Status, r.Body)
	}
}

func TestReturnPageAsksStripeOnlyAboutItsOwnSession(t *testing.T) {
	w := newShop(t)
	w.sessionPaid(2030)
	for name, q := range map[string]map[string][]string{
		"no session":    nil,
		"other session": {"session_id": {"cs_test_9"}},
		"array":         {"session_id": {`["cs_test_9"]`}},
	} {
		if r := w.page(t, "/return/{token}", w.orderToken(), q); r.Status != 200 || !strings.Contains(r.Body, "Confirming") {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if len(w.fetches) != 0 || w.status != "pending" {
		t.Fatalf("%d provider calls, order %q", len(w.fetches), w.status)
	}
}

// A session whose reference is another order's is never completed here.
func TestReturnPageChecksTheSessionReference(t *testing.T) {
	w := newShop(t)
	w.answers["GET https://api.stripe.com/v1/checkout/sessions/cs_test_1"] = [2]string{"200",
		`{"id":"cs_test_1","payment_status":"paid","amount_total":2030,"currency":"usd","payment_intent":"pi_1","client_reference_id":"someoneelses"}`}
	r := w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"session_id": {"cs_test_1"}})
	if !strings.Contains(r.Body, "Confirming") || w.status != "pending" || len(w.mails) != 0 {
		t.Fatalf("order %q: %s", w.status, r.Body)
	}
}

func TestReturnPageByStatus(t *testing.T) {
	for status, want := range map[string]string{
		"expired":   "You were not charged",
		"cancelled": "You were not charged",
		"refunded":  "refunded",
		"pending":   "Confirming your payment",
		"paid":      "Payment received",
	} {
		w := newShop(t)
		w.status = status
		if status == "paid" {
			w.attend = "active"
		}
		r := w.page(t, "/return/{token}", w.orderToken(), nil)
		if r.Status != 200 || !strings.Contains(r.Body, want) {
			t.Errorf("%s: %d %s", status, r.Status, r.Body)
		}
		if status != "paid" && strings.Contains(r.Body, "/ticket/") {
			t.Errorf("%s: a ticket link on the page", status)
		}
		if !strings.Contains(r.Body, "https://example.org/events/gig") {
			t.Errorf("%s: no link back to the event", status)
		}
	}
}

// An order cancelled after it was paid is not "not charged", on either page.
func TestPayPagesDoNotDenyAChargeOnACancelledPaidOrder(t *testing.T) {
	w := newShop(t)
	w.status, w.paid = "cancelled", true
	for _, pattern := range []string{"/return/{token}", "/cancel/{token}"} {
		r := w.page(t, pattern, w.orderToken(), nil)
		if r.Status != 200 || strings.Contains(r.Body, "not charged") || !strings.Contains(r.Body, "If you paid") {
			t.Errorf("%s: %d %s", pattern, r.Status, r.Body)
		}
	}
}

func TestPayPagesRefuseForgedTokens(t *testing.T) {
	w := newShop(t)
	good := w.orderToken()
	flipped := good[:len(good)-1] + map[bool]string{true: "1", false: "0"}[good[len(good)-1] == '0']
	ticketTok := signCode(w.key, kindTicket, w.payCode) // a token of another kind
	for name, tok := range map[string]string{"flipped": flipped, "ticket kind": ticketTok, "garbage": "x", "unknown code": signCode(w.key, kindOrder, "0123456789abcdef0123456789abcdef"), "empty": ""} {
		for _, pattern := range []string{"/return/{token}", "/cancel/{token}"} {
			if r := w.page(t, pattern, tok, nil); r.Status != 404 || strings.Contains(r.Body, "Gig") {
				t.Errorf("%s %s: %d", name, pattern, r.Status)
			}
		}
	}
	if w.released != 0 || len(w.fetches) != 0 || w.status != "pending" {
		t.Fatalf("a forged token changed something: released %d", w.released)
	}
}

func TestCancelPage(t *testing.T) {
	w := newShop(t)
	r := w.page(t, "/cancel/{token}", w.orderToken(), nil)
	if r.Status != 200 || !strings.Contains(r.Body, "Payment cancelled") || w.status != "cancelled" || w.released != 1 {
		t.Fatalf("%d, order %q: %s", r.Status, w.status, r.Body)
	}
	// again: nothing more to release
	if r = w.page(t, "/cancel/{token}", w.orderToken(), nil); r.Status != 200 || w.released != 1 {
		t.Fatalf("second visit: %d, released %d", r.Status, w.released)
	}
	// only a pending order is cancelled
	for _, status := range []string{"paid", "refunded"} {
		w = newShop(t)
		w.status = status
		r = w.page(t, "/cancel/{token}", w.orderToken(), nil)
		if r.Status != 200 || w.status != status || w.released != 0 || !strings.Contains(r.Body, "already paid") {
			t.Errorf("%s: %d, order %q: %s", status, r.Status, w.status, r.Body)
		}
	}
}
