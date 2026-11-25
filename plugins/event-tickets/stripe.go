package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// stripeTolerance is how far a webhook timestamp may be from the clock.
const stripeTolerance = 300 // seconds

const (
	maxSigHeader = 1024
	maxSigV1     = 8
	// stripeMinLife is the shortest life Stripe gives a checkout session.
	stripeMinLife = 30 * time.Minute
)

// payIn is what a Stripe session or a PayPal order is created for. Total is the
// amount the server computed, in minor units.
type payIn struct {
	OrderID    int64
	PayCode    string
	Email      string
	Title      string // product name shown at Stripe
	Currency   string
	Total      int64
	SuccessURL string // Stripe: may hold the {CHECKOUT_SESSION_ID} template
	CancelURL  string
}

// stripeSession is the part of a checkout session the plugin reads.
type stripeSession struct {
	ID              string `json:"id"`
	URL             string `json:"url"`
	Status          string `json:"status"`
	PaymentStatus   string `json:"payment_status"`
	AmountTotal     int64  `json:"amount_total"`
	Currency        string `json:"currency"`
	PaymentIntent   string `json:"payment_intent"`
	ClientReference string `json:"client_reference_id"`
}

// stripeCall runs one Stripe API request with the secret key. A form body is
// sent form-encoded. The answer is decoded into out for a 2xx status; every
// other status is an error without the answer's text.
func stripeCall(s settings, method, path string, form url.Values, idemKey string, dl deadline, out any) error {
	if s.StripeKey == "" {
		return errProviderRefused
	}
	headers := map[string]string{"Authorization": "Bearer " + s.StripeKey, "Accept": "application/json"}
	body := ""
	if form != nil {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
		body = form.Encode()
	}
	if idemKey != "" {
		headers["Idempotency-Key"] = idemKey
	}
	status, raw, err := fetchJSON(method, stripeAPI+path, headers, body, dl)
	if err != nil {
		return err
	}
	switch {
	case status >= 500 || status == 429:
		return errProviderDown
	case status < 200 || status > 299:
		return errProviderRefused
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return errProviderBadAnswer
	}
	return nil
}

// stripeCreateSession opens a Checkout session for an order and returns its id
// and the page to send the buyer to. The amount is one line item for the
// whole order; the idempotency key makes a retry give the same session.
func stripeCreateSession(s settings, o payIn) (id, pageURL string, err error) {
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", strings.ToLower(o.Currency))
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(o.Total, 10))
	form.Set("line_items[0][price_data][product_data][name]", o.Title)
	form.Set("client_reference_id", o.PayCode)
	form.Set("metadata[order_id]", strconv.FormatInt(o.OrderID, 10))
	form.Set("success_url", o.SuccessURL)
	form.Set("cancel_url", o.CancelURL)
	form.Set("expires_at", strconv.FormatInt(now().Add(stripeMinLife).Unix(), 10))
	if o.Email != "" {
		form.Set("customer_email", o.Email)
	}
	var res stripeSession
	if err := stripeCall(s, "POST", "/v1/checkout/sessions", form, "et-"+strconv.FormatInt(o.OrderID, 10)+"-create", deadline{}, &res); err != nil {
		return "", "", err
	}
	if !idRe.MatchString(res.ID) || res.URL == "" {
		return "", "", errProviderBadAnswer
	}
	return res.ID, res.URL, nil
}

// stripeGetSession reads a session, to reconcile an order when the buyer
// returns before the webhook arrived.
func stripeGetSession(s settings, id string) (stripeSession, error) {
	var res stripeSession
	if !idRe.MatchString(id) {
		return res, errProviderRefused
	}
	err := stripeCall(s, "GET", "/v1/checkout/sessions/"+id, nil, "", deadline{}, &res)
	return res, err
}

// stripeExpireSession closes a session whose hold ended.
func stripeExpireSession(s settings, id string) error {
	return stripeExpireSessionBy(s, id, deadline{})
}

// stripeExpireSessionBy is stripeExpireSession within an overall deadline.
func stripeExpireSessionBy(s settings, id string, dl deadline) error {
	if !idRe.MatchString(id) {
		return errProviderRefused
	}
	return stripeCall(s, "POST", "/v1/checkout/sessions/"+id+"/expire", url.Values{}, "", dl, nil)
}

// stripeRefund refunds amount of a payment. The idempotency key makes a retry
// reuse the refund the first call made.
func stripeRefund(s settings, paymentIntent string, amount int64, idemKey string) (refundID string, err error) {
	if !idRe.MatchString(paymentIntent) || amount < 1 || amount > maxTotalCents {
		return "", errProviderRefused
	}
	form := url.Values{}
	form.Set("payment_intent", paymentIntent)
	form.Set("amount", strconv.FormatInt(amount, 10))
	var res struct {
		ID string `json:"id"`
	}
	if err := stripeCall(s, "POST", "/v1/refunds", form, idemKey, deadline{}, &res); err != nil {
		return "", err
	}
	if !idRe.MatchString(res.ID) {
		return "", errProviderBadAnswer
	}
	return res.ID, nil
}

// verifyStripeSignature checks a Stripe-Signature header against the raw body:
// header "t=<unix>,v1=<hex>[,v1=<hex>...]", HMAC-SHA256 of "t.body" keyed with
// the webhook secret, compared in constant time, timestamp within the
// tolerance of at. Anything malformed is false.
func verifyStripeSignature(secret, header, body string, at time.Time) bool {
	if secret == "" || header == "" || len(header) > maxSigHeader {
		return false
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			if ts != "" {
				return false
			}
			ts = v
		case "v1":
			if len(sigs) == maxSigV1 {
				return false
			}
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 || len(ts) > 12 {
		return false
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || t < 0 {
		return false
	}
	if d := at.Unix() - t; d > stripeTolerance || d < -stripeTolerance {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "." + body))
	want := m.Sum(nil)
	match := false
	for _, sig := range sigs {
		got, err := hex.DecodeString(sig)
		if err == nil && hmac.Equal(got, want) { // no early exit: every v1 is compared
			match = true
		}
	}
	return match
}
