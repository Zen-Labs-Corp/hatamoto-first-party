package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testPayPalClient = "PLACEHOLDERCLIENTID"
	testPayPalSecret = "PLACEHOLDERPAYPALSECRET"
	testPayPalToken  = "PLACEHOLDERACCESSTOKEN"
)

const sandboxAPI = "https://api-m.sandbox.paypal.com"

func paypalSettings() settings {
	return settings{PayPalClientID: testPayPalClient, PayPalSecret: testPayPalSecret, PayPalWebhookID: "WEBHOOKID123", PayPalSandbox: true}
}

func (h *provHost) paypalToken(expiresIn int) {
	h.answers["POST "+sandboxAPI+"/v1/oauth2/token"] = [2]string{"200", `{"access_token":"` + testPayPalToken + `","expires_in":` + itoa(int64(expiresIn)) + `}`}
}

func TestPayPalBase(t *testing.T) {
	if paypalBase(settings{}) != "https://api-m.paypal.com" || paypalBase(settings{PayPalSandbox: true}) != sandboxAPI {
		t.Fatal("wrong base")
	}
}

func TestPayPalTokenIsCached(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	s := paypalSettings()

	tok, err := paypalToken(s, deadline{})
	if err != nil || tok != testPayPalToken {
		t.Fatalf("%q %v", tok, err)
	}
	c := h.calls[0]
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(testPayPalClient+":"+testPayPalSecret))
	if c.headers["Authorization"] != wantAuth || c.body != "grant_type=client_credentials" || c.headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("%+v", c)
	}
	var cached cachedToken
	if err := json.Unmarshal([]byte(h.meta["paypal-token"]), &cached); err != nil || cached.T != testPayPalToken || len(cached.H) != 16 ||
		cached.Exp != now().Unix()+32400 {
		t.Fatalf("cache %q %v", h.meta["paypal-token"], err)
	}
	if tok, err = paypalToken(s, deadline{}); err != nil || tok != testPayPalToken || len(h.calls) != 1 {
		t.Fatalf("second call: %q %v, %d fetches", tok, err, len(h.calls))
	}

	// another client id or mode is another token
	other := s
	other.PayPalClientID = "OTHERPLACEHOLDERID"
	if _, err := paypalToken(other, deadline{}); err != nil || len(h.calls) != 2 {
		t.Fatalf("other client: %v, %d fetches", err, len(h.calls))
	}
	// within five minutes of its end the token is renewed
	h.meta["paypal-token"] = jsonText(cachedToken{H: tokenBinding(s), T: "OLDTOKEN", Exp: now().Unix() + 299})
	if tok, err = paypalToken(s, deadline{}); err != nil || tok != testPayPalToken || len(h.calls) != 3 {
		t.Fatalf("near expiry: %q %v, %d fetches", tok, err, len(h.calls))
	}
	// a garbled cache is a miss
	h.meta["paypal-token"] = "{broken"
	if tok, err = paypalToken(s, deadline{}); err != nil || tok != testPayPalToken || len(h.calls) != 4 {
		t.Fatalf("broken cache: %q %v, %d fetches", tok, err, len(h.calls))
	}
}

func TestPayPalTokenNeedsCredentials(t *testing.T) {
	h := newProvHost(t)
	if _, err := paypalToken(settings{PayPalClientID: "x"}, deadline{}); !errors.Is(err, errProviderRefused) {
		t.Fatalf("%v", err)
	}
	if len(h.calls) != 0 {
		t.Fatal("a call was made without credentials")
	}
}

func TestPayPalCreateOrder(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	h.answers["POST "+sandboxAPI+"/v2/checkout/orders"] = [2]string{"201", `{"id":"5O190127TN364715T","status":"PAYER_ACTION_REQUIRED","links":[
{"rel":"self","href":"https://api.sandbox.paypal.com/v2/checkout/orders/5O190127TN364715T"},
{"rel":"payer-action","href":"https://www.sandbox.paypal.com/checkoutnow?token=5O190127TN364715T"}]}`}
	id, page, err := paypalCreateOrder(paypalSettings(), payIn{
		OrderID: 42, PayCode: "paycode", Title: "Tickets: Gig", Currency: "usd", Total: 1205,
		SuccessURL: "https://example.org/p/event-tickets/return/tok", CancelURL: "https://example.org/p/event-tickets/cancel/tok",
	}, deadline{})
	if err != nil || id != "5O190127TN364715T" || page != "https://www.sandbox.paypal.com/checkoutnow?token=5O190127TN364715T" {
		t.Fatalf("%q %q %v", id, page, err)
	}
	c := h.calls[1]
	if c.headers["Authorization"] != "Bearer "+testPayPalToken || c.headers["PayPal-Request-Id"] != "et-42-create" {
		t.Fatalf("headers %v", c.headers)
	}
	var body struct {
		Intent string `json:"intent"`
		Units  []struct {
			CustomID string            `json:"custom_id"`
			Amount   map[string]string `json:"amount"`
		} `json:"purchase_units"`
		Source struct {
			PayPal struct {
				Ctx map[string]string `json:"experience_context"`
			} `json:"paypal"`
		} `json:"payment_source"`
	}
	if err := json.Unmarshal([]byte(c.body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Intent != "CAPTURE" || len(body.Units) != 1 || body.Units[0].CustomID != "paycode" ||
		body.Units[0].Amount["value"] != "12.05" || body.Units[0].Amount["currency_code"] != "USD" ||
		body.Source.PayPal.Ctx["return_url"] != "https://example.org/p/event-tickets/return/tok" ||
		body.Source.PayPal.Ctx["cancel_url"] != "https://example.org/p/event-tickets/cancel/tok" {
		t.Fatalf("body %s", c.body)
	}
}

func TestPayPalCreateOrderAmountsPerCurrency(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	h.answers["POST "+sandboxAPI+"/v2/checkout/orders"] = [2]string{"201", `{"id":"ORD1","links":[{"rel":"approve","href":"https://www.sandbox.paypal.com/x"}]}`}
	for cur, want := range map[string]string{"JPY": "1200", "EUR": "12.00"} {
		h.calls = nil
		if _, _, err := paypalCreateOrder(paypalSettings(), payIn{OrderID: 1, Currency: cur, Total: map[string]int64{"JPY": 1200, "EUR": 1200}[cur]}, deadline{}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(h.calls[len(h.calls)-1].body, `"value":"`+want+`"`) {
			t.Errorf("%s: %s", cur, h.calls[len(h.calls)-1].body)
		}
	}
}

func TestPayPalCreateOrderWithoutApproveLink(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	h.answers["POST "+sandboxAPI+"/v2/checkout/orders"] = [2]string{"201", `{"id":"ORD1","links":[{"rel":"self","href":"https://x"}]}`}
	if _, _, err := paypalCreateOrder(paypalSettings(), payIn{OrderID: 1, Currency: "USD", Total: 100}, deadline{}); !errors.Is(err, errProviderBadAnswer) {
		t.Fatalf("%v", err)
	}
}

const captureAnswer = `{"id":"ORD1","status":"COMPLETED","purchase_units":[{"payments":{"captures":[
{"id":"CAP1","status":"COMPLETED","amount":{"value":"12.05","currency_code":"USD"},"custom_id":"paycode"}]}}]}`

func TestPayPalCapture(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	h.answers["POST "+sandboxAPI+"/v2/checkout/orders/ORD1/capture"] = [2]string{"201", captureAnswer}
	got, err := paypalCaptureOrder(paypalSettings(), "ORD1", "et-42-capture", deadline{})
	want := paypalCaptured{Status: "COMPLETED", CaptureID: "CAP1", CaptureStatus: "COMPLETED", Value: "12.05", Currency: "USD", CustomID: "paycode"}
	if err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
	c := h.calls[1]
	if c.headers["PayPal-Request-Id"] != "et-42-capture" || c.headers["Authorization"] != "Bearer "+testPayPalToken || c.body != "{}" {
		t.Fatalf("%+v", c)
	}
}

func TestPayPalCaptureWithoutCaptures(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	h.answers["POST "+sandboxAPI+"/v2/checkout/orders/ORD1/capture"] = [2]string{"201", `{"id":"ORD1","status":"APPROVED"}`}
	got, err := paypalCaptureOrder(paypalSettings(), "ORD1", "r", deadline{})
	if err != nil || got.Status != "APPROVED" || got.CaptureID != "" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := paypalCaptureOrder(paypalSettings(), "ORD1/../x", "r", deadline{}); !errors.Is(err, errProviderRefused) {
		t.Fatalf("bad id: %v", err)
	}
}

func goodHeaders() paypalHeaders {
	return paypalHeaders{TransmissionID: "tid-1", TransmissionTime: "2027-01-01T12:00:00Z", TransmissionSig: "c2lnbmF0dXJl", CertURL: "https://api.sandbox.paypal.com/v1/notifications/certs/CERT-1", AuthAlgo: "SHA256withRSA"}
}

func TestPayPalVerifyWebhook(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	verify := "POST " + sandboxAPI + "/v1/notifications/verify-webhook-signature"
	h.answers[verify] = [2]string{"200", `{"verification_status":"SUCCESS"}`}
	body := "{\n \"id\": \"WH-1\",  \"event_type\":\"CHECKOUT.ORDER.APPROVED\"\n}"

	ok, err := paypalVerifyWebhook(paypalSettings(), goodHeaders(), body, deadline{})
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	sent := h.calls[1].body
	if !strings.Contains(sent, `"webhook_event":`+body+"}") {
		t.Fatalf("the body was not embedded unchanged: %s", sent)
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal([]byte(sent), &req); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"transmission_id": "tid-1", "transmission_time": "2027-01-01T12:00:00Z", "transmission_sig": "c2lnbmF0dXJl",
		"cert_url": "https://api.sandbox.paypal.com/v1/notifications/certs/CERT-1", "auth_algo": "SHA256withRSA", "webhook_id": "WEBHOOKID123"} {
		if string(req[k]) != `"`+v+`"` {
			t.Errorf("%s = %s", k, req[k])
		}
	}

	h.answers[verify] = [2]string{"200", `{"verification_status":"FAILURE"}`}
	if ok, err := paypalVerifyWebhook(paypalSettings(), goodHeaders(), body, deadline{}); ok || err != nil {
		t.Fatalf("FAILURE: %v %v", ok, err)
	}
	h.answers[verify] = [2]string{"200", `{}`}
	if ok, _ := paypalVerifyWebhook(paypalSettings(), goodHeaders(), body, deadline{}); ok {
		t.Fatal("an answer without a status verified")
	}
	h.answers[verify] = [2]string{"500", `{}`}
	if ok, err := paypalVerifyWebhook(paypalSettings(), goodHeaders(), body, deadline{}); ok || !errors.Is(err, errProviderDown) {
		t.Fatalf("500: %v %v", ok, err)
	}
}

func TestPayPalVerifyWebhookRefusesIncompleteInput(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	s := paypalSettings()
	headers := func(edit func(*paypalHeaders)) paypalHeaders { g := goodHeaders(); edit(&g); return g }
	noHook := s
	noHook.PayPalWebhookID = ""
	cases := []struct {
		name string
		s    settings
		h    paypalHeaders
		body string
	}{
		{"no webhook id", noHook, goodHeaders(), `{}`},
		{"no id header", s, headers(func(h *paypalHeaders) { h.TransmissionID = "" }), `{}`},
		{"no time header", s, headers(func(h *paypalHeaders) { h.TransmissionTime = "" }), `{}`},
		{"no sig header", s, headers(func(h *paypalHeaders) { h.TransmissionSig = "" }), `{}`},
		{"no cert header", s, headers(func(h *paypalHeaders) { h.CertURL = "" }), `{}`},
		{"no algo header", s, headers(func(h *paypalHeaders) { h.AuthAlgo = "" }), `{}`},
		{"huge header", s, headers(func(h *paypalHeaders) { h.TransmissionSig = strings.Repeat("a", 2049) }), `{}`},
		{"body not JSON", s, goodHeaders(), `{"a":1}, "x": {`},
		{"empty body", s, goodHeaders(), ``},
	}
	for _, c := range cases {
		if ok, err := paypalVerifyWebhook(c.s, c.h, c.body, deadline{}); ok || err != nil {
			t.Errorf("%s: %v %v", c.name, ok, err)
		}
	}
	if len(h.calls) != 0 {
		t.Fatalf("%d calls reached the host", len(h.calls))
	}
}

func TestPayPalRefund(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	refund := "POST " + sandboxAPI + "/v2/payments/captures/CAP1/refund"
	h.answers[refund] = [2]string{"201", `{"id":"RF1","status":"COMPLETED"}`}
	id, err := paypalRefund(paypalSettings(), "CAP1", 500, "usd", "et-42-refund-0-500", deadline{})
	if err != nil || id != "RF1" {
		t.Fatalf("%q %v", id, err)
	}
	c := h.calls[1]
	if c.headers["PayPal-Request-Id"] != "et-42-refund-0-500" || c.body != `{"amount":{"currency_code":"USD","value":"5.00"}}` {
		t.Fatalf("%+v", c)
	}
	h.answers[refund] = [2]string{"201", `{"id":"RF1","status":"FAILED"}`}
	if _, err := paypalRefund(paypalSettings(), "CAP1", 500, "USD", "k", deadline{}); !errors.Is(err, errProviderBadAnswer) {
		t.Fatalf("FAILED: %v", err)
	}
	if _, err := paypalRefund(paypalSettings(), "CAP1/../x", 500, "USD", "k", deadline{}); !errors.Is(err, errProviderRefused) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := paypalRefund(paypalSettings(), "CAP1", 0, "USD", "k", deadline{}); !errors.Is(err, errProviderRefused) {
		t.Fatalf("zero: %v", err)
	}
}

// No error string carries a credential, the access token or what PayPal said.
func TestPayPalErrorsAreFixedStrings(t *testing.T) {
	h := newProvHost(t)
	s := paypalSettings()
	leak := `{"name":"AUTHENTICATION_FAILURE","message":"` + testPayPalSecret + " " + testPayPalToken + `"}`
	check := func(name string, err error, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", name, err, want)
			return
		}
		for _, secret := range []string{testPayPalSecret, testPayPalToken, testPayPalClient, "Basic ", "Bearer "} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error carries %q: %v", name, secret, err)
			}
		}
	}
	tok := "POST " + sandboxAPI + "/v1/oauth2/token"
	h.answers[tok] = [2]string{"401", leak}
	_, err := paypalToken(s, deadline{})
	check("token 401", err, errProviderRefused)
	h.answers[tok] = [2]string{"200", `{"access_token":"","expires_in":0}`}
	_, err = paypalToken(s, deadline{})
	check("token empty", err, errProviderBadAnswer)
	h.netErr = errors.New("dial " + testPayPalSecret)
	_, err = paypalToken(s, deadline{})
	check("token network", err, errProviderDown)
	h.netErr = nil

	h.paypalToken(32400)
	create := "POST " + sandboxAPI + "/v2/checkout/orders"
	for status, want := range map[string]error{"422": errProviderRefused, "500": errProviderDown} {
		h.answers[create] = [2]string{status, leak}
		_, _, err = paypalCreateOrder(s, payIn{OrderID: 1, Currency: "USD", Total: 100}, deadline{})
		check("create "+status, err, want)
	}
	h.answers[create] = [2]string{"201", "not json " + testPayPalToken}
	_, _, err = paypalCreateOrder(s, payIn{OrderID: 1, Currency: "USD", Total: 100}, deadline{})
	check("create not json", err, errProviderBadAnswer)
}

func TestPayPalCallsRespectTheDeadline(t *testing.T) {
	h := newProvHost(t)
	h.paypalToken(32400)
	if _, err := paypalToken(paypalSettings(), newDeadline(time.Second)); !errors.Is(err, errNoTime) {
		t.Fatalf("%v", err)
	}
	if len(h.calls) != 0 {
		t.Fatal("a call was made with no time left")
	}
}
