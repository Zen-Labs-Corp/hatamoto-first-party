package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// webhookBudget is the time a PayPal webhook may spend on provider calls. The
// route limit is 10 s; a capture needs at least minCaptureLeft of it.
const (
	webhookBudget  = 8 * time.Second
	minCaptureLeft = 3 * time.Second
)

// paypalEvent is the part of a PayPal webhook the plugin reads. It is decoded
// only after PayPal has said the webhook is genuine, and leniently.
type paypalEvent struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Resource  struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		CustomID      string `json:"custom_id"`
		PurchaseUnits []struct {
			CustomID string `json:"custom_id"`
		} `json:"purchase_units"`
		Amount struct {
			Value    string `json:"value"`
			Currency string `json:"currency_code"`
		} `json:"amount"`
		Supplementary struct {
			Related struct {
				OrderID string `json:"order_id"`
			} `json:"related_ids"`
		} `json:"supplementary_data"`
	} `json:"resource"`
}

// paypalOrder is an order as the capture reads it.
type paypalOrder struct {
	Status   string `json:"status"`
	Provider string `json:"provider"`
	Ref      string `json:"provider_ref"`
	PayCode  string `json:"pay_code"`
	Total    int64  `json:"total_cents"`
	Cur      string `json:"currency"`
	Paid     bool   `json:"paid"`
	Live     bool   `json:"live"` // the hold has not lapsed
}

func readPayPalOrder(orderID int64) (*paypalOrder, error) {
	var o *paypalOrder
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('status', status, 'provider', coalesce(provider, ''),
    'provider_ref', coalesce(provider_ref, ''), 'pay_code', coalesce(pay_code, ''), 'total_cents', total_cents,
    'currency', coalesce(currency, ''), 'paid', paid_at IS NOT NULL, 'live', coalesce(expires_at > now(), false))
FROM orders WHERE id = $1), 'null'::jsonb)`, &o, orderID)
	return o, err
}

// findPayPalOrder finds the order of a PayPal order id. nil when there is none.
func findPayPalOrder(ref string) (*orderRef, error) {
	if !idRe.MatchString(ref) {
		return nil, nil
	}
	var o *orderRef
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('id', id, 'status', status) FROM orders
WHERE provider = 'paypal' AND provider_ref = $1), 'null'::jsonb)`, &o, ref)
	return o, err
}

// capturePayPal is the one path that moves PayPal money in. A capture is only
// made while the order's seats are held (a lapsed hold is taken again first;
// without seats nothing is captured and nothing is charged). The capture is
// accepted only when it is COMPLETED and names this order's pay code, amount
// and currency. The request id is the same for every capture of an order, so
// the return page and the webhook racing each other capture once.
//
// outcome is one of the completePayment outcomes, or "expired" (no seats, no
// capture) or "pending" (captured but not completed yet).
func capturePayPal(s settings, orderID int64, dl deadline) (outcome string, err error) {
	o, err := readPayPalOrder(orderID)
	switch {
	case err != nil:
		return "", err
	case o == nil:
		return outcomeUnknown, nil
	case o.Provider != "paypal" || o.Paid || (o.Status != "pending" && o.Status != "expired" && o.Status != "cancelled"):
		return outcomeDuplicate, nil
	}
	if o.Status == "pending" && !o.Live {
		// the hold lapsed but was not released yet: end it, then take the seats again
		if _, err := releaseHold(orderID, "expired"); err != nil {
			return "", err
		}
		o.Status = "expired"
	}
	if o.Status != "pending" {
		back, err := retakeHold(orderID)
		if err != nil {
			return "", err
		}
		if !back {
			return "expired", nil
		}
	}
	got, err := paypalCaptureOrder(s, o.Ref, idemKey(o.PayCode, "capture"), dl)
	if err != nil {
		return "", err
	}
	if got.CaptureStatus != "COMPLETED" {
		if got.CaptureStatus == "PENDING" { // money may still arrive: keep the order out of retention deletes
			if err := markPaymentRef(orderID, got.CaptureID); err != nil {
				return "", err
			}
		}
		return "pending", nil
	}
	minor, ok := parseAmount(got.Value, got.Currency)
	if !ok || got.CustomID != o.PayCode || got.CaptureID == "" {
		if err := markMismatch(orderID); err != nil {
			return "", err
		}
		return outcomeMismatch, nil
	}
	return completePayment(orderID, "paypal", got.CaptureID, minor, got.Currency)
}

// idemKey is the idempotency key (Stripe) or request id (PayPal) of one call
// about an order. It is built from the order's random pay code, not its id: ids
// repeat across sites that share a provider account and after a reinstall, and
// a repeated key would hand back another order's answer.
func idemKey(payCode, what string) string { return "et-" + payCode + "-" + what }

// markPaymentRef records the payment id of a payment that is not settled yet
// (a PayPal capture that is PENDING, a Stripe delayed method), so the order
// shows that money may have moved. completePayment overwrites it on success.
func markPaymentRef(orderID int64, ref string) error {
	if ref == "" {
		return nil
	}
	return dbExec(`UPDATE orders SET payment_ref = $2 WHERE id = $1 AND status IN ('pending', 'expired', 'cancelled') AND paid_at IS NULL AND payment_ref IS NULL`, orderID, ref)
}

// markMismatch flags an order whose payment does not match it, for the owner.
func markMismatch(orderID int64) error {
	return dbExec(`UPDATE orders SET problem = 'amount_mismatch' WHERE id = $1 AND status IN ('pending', 'expired', 'cancelled') AND paid_at IS NULL`, orderID)
}

// PayPal sends a webhook again for days after a failure; a transmission time
// further in the future than a clock error is never genuine.
const (
	transmissionMaxAge  = 72 * time.Hour
	transmissionMaxSkew = time.Hour
)

var (
	authAlgoRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	sigRe      = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)
)

// plausiblePayPalHeaders is the cheap part of the signature check: all five
// headers well-formed, the certificate on PayPal's own domain over https, and
// a transmission time near now. PayPal's verify call still decides.
func plausiblePayPalHeaders(h paypalHeaders) bool {
	if !h.complete() || !idRe.MatchString(h.TransmissionID) || !sigRe.MatchString(h.TransmissionSig) || !authAlgoRe.MatchString(h.AuthAlgo) {
		return false
	}
	u, err := url.Parse(h.CertURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	if host := strings.ToLower(u.Hostname()); host != "paypal.com" && !strings.HasSuffix(host, ".paypal.com") {
		return false
	}
	ts, err := time.Parse(time.RFC3339, h.TransmissionTime)
	if err != nil {
		return false
	}
	age := now().Sub(ts)
	return age <= transmissionMaxAge && age >= -transmissionMaxSkew
}

// servePayPalWebhook handles POST /paypal/webhook. Nothing is decoded or
// stored before PayPal's verify-webhook-signature answers SUCCESS for the body
// as it came; junk, other event types and events handled before never reach it. Bad input is a 400, trouble on our side or at PayPal a 503
// (PayPal retries), the rest a 200.
func servePayPalWebhook(r sdk.HTTPRequest) sdk.HTTPResponse {
	if !webhookBody(r) {
		return webhookReply(http.StatusBadRequest, "bad request")
	}
	s := loadSettings()
	h := paypalHeaders{
		TransmissionID:   headerValue(r.Headers, "paypal-transmission-id"),
		TransmissionTime: headerValue(r.Headers, "paypal-transmission-time"),
		TransmissionSig:  headerValue(r.Headers, "paypal-transmission-sig"),
		CertURL:          headerValue(r.Headers, "paypal-cert-url"),
		AuthAlgo:         headerValue(r.Headers, "paypal-auth-algo"),
	}
	if !s.paypalReady() || s.PayPalWebhookID == "" || !h.complete() {
		return webhookReply(http.StatusBadRequest, "bad signature")
	}
	// Anyone can post here, and every verify is an outbound call (and perhaps an
	// OAuth call): what can be told apart from junk without PayPal is rejected first.
	if !plausiblePayPalHeaders(h) {
		return webhookReply(http.StatusBadRequest, "bad signature")
	}
	var ev paypalEvent
	if json.Unmarshal([]byte(r.Body), &ev) != nil || !idRe.MatchString(ev.ID) {
		return webhookReply(http.StatusBadRequest, "bad request")
	}
	if ev.EventType != "CHECKOUT.ORDER.APPROVED" && ev.EventType != "PAYMENT.CAPTURE.COMPLETED" {
		return webhookReply(http.StatusOK, "ignored")
	}
	seen, err := eventSeen("paypal", ev.ID)
	if err != nil {
		return webhookReply(http.StatusServiceUnavailable, "unavailable")
	}
	if seen {
		return webhookReply(http.StatusOK, outcomeDuplicate)
	}
	dl := newDeadline(webhookBudget)
	genuine, err := paypalVerifyWebhook(s, h, r.Body, dl)
	if err != nil {
		return webhookReply(http.StatusServiceUnavailable, "unavailable")
	}
	if !genuine {
		return webhookReply(http.StatusBadRequest, "bad signature")
	}

	var orderID int64
	var outcome string
	if ev.EventType == "CHECKOUT.ORDER.APPROVED" {
		orderID, outcome, err = approvedPayPal(s, ev, dl)
	} else {
		orderID, outcome, err = completedPayPalCapture(ev)
	}
	if err != nil {
		return webhookReply(http.StatusServiceUnavailable, "unavailable")
	}
	if orderID == 0 { // not an order of ours
		return webhookReply(http.StatusOK, "ignored")
	}
	if recordEvent("paypal", ev.ID, orderID, outcome) != nil {
		return webhookReply(http.StatusServiceUnavailable, "unavailable")
	}
	return webhookReply(http.StatusOK, outcome)
}

// approvedPayPal captures an order the buyer approved, for the case that they
// closed the tab before returning to the site.
func approvedPayPal(s settings, ev paypalEvent, dl deadline) (orderID int64, outcome string, err error) {
	o, err := findPayPalOrder(ev.Resource.ID)
	if err != nil || o == nil {
		return 0, "", err
	}
	if dl.left() < int(minCaptureLeft/time.Millisecond) {
		return 0, "", errNoTime // PayPal sends it again
	}
	outcome, err = capturePayPal(s, o.ID, dl)
	return o.ID, outcome, err
}

// completedPayPalCapture completes an order from a PAYMENT.CAPTURE.COMPLETED
// event, for a capture made elsewhere (the webhook of a capture the return
// page started, or a retry). It is matched by order id and pay code.
func completedPayPalCapture(ev paypalEvent) (orderID int64, outcome string, err error) {
	res := ev.Resource
	if res.Status != "COMPLETED" || !idRe.MatchString(res.ID) {
		return 0, "", nil
	}
	o, err := findOrder("paypal", res.Supplementary.Related.OrderID, strings.TrimSpace(res.CustomID))
	if err != nil || o == nil {
		return 0, "", err
	}
	minor, ok := parseAmount(res.Amount.Value, res.Amount.Currency)
	if !ok {
		return o.ID, outcomeMismatch, markMismatch(o.ID)
	}
	outcome, err = completePayment(o.ID, "paypal", res.ID, minor, res.Amount.Currency)
	return o.ID, outcome, err
}
