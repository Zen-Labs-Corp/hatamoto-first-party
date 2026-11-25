package main

import (
	"encoding/json"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"hatamoto.dev/sdk"
)

// maxWebhookBody is the most a webhook body may be. Stripe events pass 4 KiB.
const maxWebhookBody = 256 << 10

var stripeEventRe = regexp.MustCompile(`^evt_[A-Za-z0-9]{1,250}$`)

// stripeEvent is the part of a Stripe event the plugin reads. It is decoded
// only after the signature is verified, and leniently: unknown keys are fine.
type stripeEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object struct {
			ID              string `json:"id"`
			ClientReference string `json:"client_reference_id"`
			PaymentStatus   string `json:"payment_status"`
			AmountTotal     int64  `json:"amount_total"`
			Currency        string `json:"currency"`
			PaymentIntent   string `json:"payment_intent"`
		} `json:"object"`
	} `json:"data"`
}

// webhookReply is the answer of a webhook route: a status word only, never a
// header, a secret or anything the provider sent.
func webhookReply(code int, status string) sdk.HTTPResponse {
	return jsonReply(code, map[string]string{"status": status})
}

// headerValue looks a header up whatever the case of its name.
func headerValue(h map[string]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// webhookBody checks the request shape both webhooks share: JSON content
// type, no base64 body, not empty, at most maxWebhookBody. false is a 400.
func webhookBody(r sdk.HTTPRequest) bool {
	if r.BodyB64 != "" || len(r.Body) == 0 || len(r.Body) > maxWebhookBody {
		return false
	}
	mt, _, err := mime.ParseMediaType(headerValue(r.Headers, "content-type"))
	return err == nil && mt == "application/json"
}

// orderRef is an order as a webhook finds it.
type orderRef struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// findOrder looks an order up by the provider's reference and the pay code
// only we and the provider know. nil when there is none.
func findOrder(provider, ref, payCode string) (*orderRef, error) {
	if ref == "" || payCode == "" {
		return nil, nil
	}
	var o *orderRef
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('id', id, 'status', status) FROM orders
WHERE provider = $1 AND provider_ref = $2 AND pay_code = $3), 'null'::jsonb)`, &o, provider, ref, payCode)
	return o, err
}

// eventSeen reports whether a provider event was handled before.
func eventSeen(provider, eventID string) (bool, error) {
	var seen bool
	err := dbJSON(`SELECT to_jsonb(EXISTS (SELECT 1 FROM payment_events WHERE provider = $1 AND event_id = $2))`, &seen, provider, eventID)
	return seen, err
}

// recordEvent remembers a handled event, so a replay does nothing. The primary
// key makes a second insert a no-op.
func recordEvent(provider, eventID string, orderID int64, outcome string) error {
	return dbExec(`INSERT INTO payment_events (provider, event_id, order_id, outcome) VALUES ($1, $2, $3, $4)
ON CONFLICT (provider, event_id) DO NOTHING`, provider, eventID, orderID, outcome)
}

// serveStripeWebhook handles POST /stripe/webhook. The signature is checked
// over the raw body before anything is decoded or stored. Bad input is a 400,
// trouble on our side or at Stripe a 503 (Stripe retries), the rest a 200.
func serveStripeWebhook(r sdk.HTTPRequest) sdk.HTTPResponse {
	if !webhookBody(r) {
		return webhookReply(http.StatusBadRequest, "bad request")
	}
	s := loadSettings()
	if !verifyStripeSignature(s.StripeWebhookSecret, headerValue(r.Headers, "stripe-signature"), r.Body, now()) {
		return webhookReply(http.StatusBadRequest, "bad signature")
	}
	var ev stripeEvent
	if json.Unmarshal([]byte(r.Body), &ev) != nil || !stripeEventRe.MatchString(ev.ID) {
		return webhookReply(http.StatusBadRequest, "bad request")
	}
	obj := ev.Data.Object
	switch ev.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded",
		"checkout.session.async_payment_failed", "checkout.session.expired":
	default:
		return webhookReply(http.StatusOK, "ignored")
	}
	seen, err := eventSeen("stripe", ev.ID)
	if err != nil {
		return webhookReply(http.StatusServiceUnavailable, "unavailable")
	}
	if seen {
		return webhookReply(http.StatusOK, outcomeDuplicate)
	}
	order, err := findOrder("stripe", obj.ID, obj.ClientReference)
	if err != nil {
		return webhookReply(http.StatusServiceUnavailable, "unavailable")
	}
	if order == nil { // not an order of ours (the account may serve other checkouts)
		return webhookReply(http.StatusOK, "ignored")
	}

	outcome := "ignored"
	switch {
	case ev.Type == "checkout.session.expired" || ev.Type == "checkout.session.async_payment_failed":
		if _, err := releaseHold(order.ID, "expired"); err != nil {
			return webhookReply(http.StatusServiceUnavailable, "unavailable")
		}
		outcome = "expired"
	case obj.PaymentStatus == "unpaid" && ev.Type == "checkout.session.completed":
		// a delayed method: money may still arrive, so the payment is marked
		// and the order is never taken for abandoned
		if err := markPaymentRef(order.ID, obj.PaymentIntent); err != nil {
			return webhookReply(http.StatusServiceUnavailable, "unavailable")
		}
	case obj.PaymentStatus == "paid":
		outcome, err = completePayment(order.ID, "stripe", obj.PaymentIntent, obj.AmountTotal, obj.Currency)
		if err != nil {
			return webhookReply(http.StatusServiceUnavailable, "unavailable")
		}
	}
	if recordEvent("stripe", ev.ID, order.ID, outcome) != nil {
		return webhookReply(http.StatusServiceUnavailable, "unavailable")
	}
	return webhookReply(http.StatusOK, outcome)
}
