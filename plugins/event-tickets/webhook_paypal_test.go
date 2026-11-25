package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	ppVerify  = "POST " + sandboxAPI + "/v1/notifications/verify-webhook-signature"
	ppCapture = "POST " + sandboxAPI + "/v2/checkout/orders/ORD1/capture"
	ppToken   = "POST " + sandboxAPI + "/v1/oauth2/token"
	ppRefund  = "POST " + sandboxAPI + "/v2/payments/captures/CAP1/refund"
)

// newPayPalShop is a shop whose order is a PayPal order, with PayPal answering
// a good verify, a token and a completed capture of the order's total.
func newPayPalShop(t *testing.T) *shop {
	w := newShop(t)
	w.provider, w.ref = "paypal", "ORD1"
	w.answers[ppToken] = [2]string{"200", `{"access_token":"` + testPayPalToken + `","expires_in":32400}`}
	w.answers[ppVerify] = [2]string{"200", `{"verification_status":"SUCCESS"}`}
	w.answers[ppCapture] = [2]string{"201", w.captureAnswer("COMPLETED", "20.30", "USD", w.payCode)}
	w.answers[ppRefund] = [2]string{"201", `{"id":"RF1","status":"COMPLETED"}`}
	return w
}

func (w *shop) captureAnswer(status, value, cur, customID string) string {
	return `{"id":"ORD1","status":"COMPLETED","purchase_units":[{"payments":{"captures":[{"id":"CAP1","status":"` + status +
		`","amount":{"value":"` + value + `","currency_code":"` + cur + `"},"custom_id":"` + customID + `"}]}}]}`
}

func (w *shop) approvedBody(evID string) string {
	return `{"id":"` + evID + `","event_type":"CHECKOUT.ORDER.APPROVED","resource":{"id":"ORD1","status":"APPROVED","purchase_units":[{"custom_id":"` + w.payCode + `"}]}}`
}

func (w *shop) captureCompletedBody(evID, customID string) string {
	return `{"id":"` + evID + `","event_type":"PAYMENT.CAPTURE.COMPLETED","resource":{"id":"CAP1","status":"COMPLETED","custom_id":"` + customID +
		`","amount":{"value":"20.30","currency_code":"USD"},"supplementary_data":{"related_ids":{"order_id":"ORD1"}}}}`
}

func ppHeaders() map[string]string {
	return map[string]string{
		"content-type": "application/json", "Paypal-Transmission-Id": "tid-1", "paypal-transmission-time": "2027-01-01T12:00:00Z",
		"paypal-transmission-sig": "c2lnbmF0dXJl", "paypal-cert-url": "https://api.sandbox.paypal.com/v1/notifications/certs/CERT-1", "paypal-auth-algo": "SHA256withRSA",
	}
}

func payPalHook(t *testing.T, body, b64 string, headers map[string]string) (int, string, string) {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "POST", Pattern: "/paypal/webhook", Body: body, BodyB64: b64, Headers: headers})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var resp sdk.HTTPResponse
	_ = json.Unmarshal(out, &resp)
	var m struct{ Status string }
	_ = json.Unmarshal([]byte(resp.Body), &m)
	return resp.Status, m.Status, resp.Body
}

func (w *shop) hook(t *testing.T, body string) (int, string) {
	t.Helper()
	code, status, _ := payPalHook(t, body, "", ppHeaders())
	return code, status
}

func (w *shop) fetched(call string) []fetched {
	var out []fetched
	for _, f := range w.fetches {
		if f.method+" "+f.url == call {
			out = append(out, f)
		}
	}
	return out
}

func TestPayPalWebhookCapturesAnApprovedOrder(t *testing.T) {
	w := newPayPalShop(t)
	body := "{\n \"id\": \"WH-1\", \"event_type\":\"CHECKOUT.ORDER.APPROVED\",\"resource\":{\"id\":\"ORD1\",\"purchase_units\":[{\"custom_id\":\"" + w.payCode + "\"}]}}"
	code, status := w.hook(t, body)
	if code != 200 || status != "paid" || w.status != "paid" || w.attend != "active" || w.events["WH-1"] != "paid" {
		t.Fatalf("%d %q, order %q attendees %q events %v", code, status, w.status, w.attend, w.events)
	}
	verify := w.fetched(ppVerify)
	if len(verify) != 1 || !strings.Contains(verify[0].body, `"webhook_event":`+body+"}") || !strings.Contains(verify[0].body, `"webhook_id":"WEBHOOKID123"`) ||
		!strings.Contains(verify[0].body, `"transmission_id":"tid-1"`) {
		t.Fatalf("verify call %+v", verify)
	}
	capture := w.fetched(ppCapture)
	if len(capture) != 1 || capture[0].headers["PayPal-Request-Id"] != "et-"+w.payCode+"-capture" || capture[0].headers["Authorization"] != "Bearer "+testPayPalToken {
		t.Fatalf("capture call %+v", capture)
	}
	if len(w.mails) != 1 || len(w.queued) != 0 {
		t.Fatalf("%d mails", len(w.mails))
	}
	// the verify call comes before the capture
	var order []string
	for _, f := range w.fetches {
		order = append(order, f.url[strings.LastIndex(f.url, "/")+1:])
	}
	if strings.Join(order, " ") != "token verify-webhook-signature token capture" {
		t.Errorf("calls in order: %v", order)
	}
}

// Nothing is decoded, read or written before PayPal says SUCCESS.
func TestPayPalWebhookNeedsASuccessfulVerify(t *testing.T) {
	w := newPayPalShop(t)
	body := w.approvedBody("WH-1")
	for name, ans := range map[string][2]string{
		"FAILURE":    {"200", `{"verification_status":"FAILURE"}`},
		"no status":  {"200", `{}`},
		"success lc": {"200", `{"verification_status":"success"}`},
	} {
		w.answers[ppVerify] = ans
		if code, status, resp := payPalHook(t, body, "", ppHeaders()); code != 400 || status != "bad signature" {
			t.Errorf("%s: %d %s", name, code, resp)
		}
	}
	w.answers[ppVerify] = [2]string{"200", `<html>` + testPayPalSecret}
	if code, _, resp := payPalHook(t, body, "", ppHeaders()); code != 503 || strings.Contains(resp, testPayPalSecret) {
		t.Errorf("verify garbage: %d %s", code, resp)
	}
	w.answers[ppVerify] = [2]string{"500", `{"details":"` + testPayPalSecret + `"}`}
	if code, _, resp := payPalHook(t, body, "", ppHeaders()); code != 503 || strings.Contains(resp, testPayPalSecret) {
		t.Errorf("verify unavailable: %d %s", code, resp)
	}
	w.answers[ppVerify] = [2]string{"200", `{"verification_status":"SUCCESS"}`}
	delete(w.answers, ppToken) // no token: cannot verify
	if code, _, _ := payPalHook(t, body, "", ppHeaders()); code != 503 {
		t.Errorf("no token: %d", code)
	}
	if len(w.writes) != 0 || len(w.mails) != 0 || len(w.fetched(ppCapture)) != 0 || w.status != "pending" || len(w.events) != 0 {
		t.Fatalf("an unverified webhook changed something: %d writes", len(w.writes))
	}
}

func TestPayPalWebhookRefusesBadRequestsBeforeAnyCall(t *testing.T) {
	w := newPayPalShop(t)
	body := w.approvedBody("WH-1")
	without := func(name string) map[string]string {
		h := ppHeaders()
		for k := range h {
			if strings.EqualFold(k, name) {
				delete(h, k)
			}
		}
		return h
	}
	for _, name := range []string{"paypal-transmission-id", "paypal-transmission-time", "paypal-transmission-sig", "paypal-cert-url", "paypal-auth-algo"} {
		if code, _, _ := payPalHook(t, body, "", without(name)); code != 400 {
			t.Errorf("without %s: %d", name, code)
		}
	}
	long := ppHeaders()
	long["paypal-transmission-sig"] = strings.Repeat("a", 2049)
	if code, _, _ := payPalHook(t, body, "", long); code != 400 {
		t.Errorf("long header: %d", code)
	}
	text := ppHeaders()
	text["content-type"] = "text/plain"
	if code, _, _ := payPalHook(t, body, "", text); code != 400 {
		t.Errorf("content type: %d", code)
	}
	if code, _, _ := payPalHook(t, body, "e30=", ppHeaders()); code != 400 {
		t.Errorf("body_b64: %d", code)
	}
	if code, _, _ := payPalHook(t, "", "", ppHeaders()); code != 400 {
		t.Errorf("empty: %d", code)
	}
	if code, _, _ := payPalHook(t, body+strings.Repeat(" ", maxWebhookBody), "", ppHeaders()); code != 400 {
		t.Errorf("oversized: %d", code)
	}
	if code, _, _ := payPalHook(t, "not json", "", ppHeaders()); code != 400 { // verify refuses what is not JSON
		t.Errorf("not json: %d", code)
	}
	w.settingsNoHook()
	if code, _, _ := payPalHook(t, body, "", ppHeaders()); code != 400 {
		t.Errorf("no webhook id: %d", code)
	}
	if len(w.fetches) != 0 || w.dbCalls != 0 {
		t.Fatalf("a refused request reached PayPal or the database: %d calls", len(w.fetches))
	}
}

// settingsNoHook drops the webhook id from the settings the fake host answers.
func (w *shop) settingsNoHook() {
	orig := callHost
	callHost = func(fn string, args, out any) error {
		if fn == sdk.FnSettingsGet {
			return jsonInto(`{"paypal_client_id":"`+testPayPalClient+`","paypal_secret":"`+testPayPalSecret+`","paypal_sandbox":true}`, out)
		}
		return orig(fn, args, out)
	}
}

func TestPayPalWebhookReplayDoesNotCaptureAgain(t *testing.T) {
	w := newPayPalShop(t)
	body := w.approvedBody("WH-1")
	if code, status := w.hook(t, body); code != 200 || status != "paid" {
		t.Fatalf("%d %q", code, status)
	}
	writes := len(w.writes)
	for range 2 {
		if code, status := w.hook(t, body); code != 200 || status != "duplicate" {
			t.Fatalf("replay: %d %q", code, status)
		}
	}
	if len(w.writes) != writes || len(w.mails) != 1 || len(w.fetched(ppCapture)) != 1 || len(w.fetched(ppRefund)) != 0 {
		t.Errorf("a replay wrote %d statements, sent %d mails, captured %d times", len(w.writes)-writes, len(w.mails), len(w.fetched(ppCapture)))
	}
}

func TestPayPalWebhookWithALapsedHold(t *testing.T) {
	// the hold lapsed but nobody released it yet: it is ended and taken again, then captured
	w := newPayPalShop(t)
	w.live = false
	if code, status := w.hook(t, w.approvedBody("WH-1")); code != 200 || status != "paid" || w.released != 1 || len(w.fetched(ppCapture)) != 1 {
		t.Fatalf("lapsed, seats free: %d %q, released %d", code, status, w.released)
	}
	// released already, seats free
	w = newPayPalShop(t)
	w.status, w.attend = "expired", "expired"
	if code, status := w.hook(t, w.approvedBody("WH-1")); code != 200 || status != "paid" || len(w.fetched(ppCapture)) != 1 {
		t.Fatalf("expired, seats free: %d %q", code, status)
	}
	// no seats left: nothing is captured, so nothing is charged
	for _, status := range []string{"pending", "expired", "cancelled"} {
		w = newPayPalShop(t)
		w.status, w.attend, w.seats, w.live = status, "expired", false, status != "pending"
		code, got := w.hook(t, w.approvedBody("WH-1"))
		if code != 200 || got != "expired" || len(w.fetched(ppCapture)) != 0 || len(w.mails) != 0 || w.paid {
			t.Errorf("%s, sold out: %d %q, %d captures", status, code, got, len(w.fetched(ppCapture)))
		}
		if st := w.status; st == "paid" || st == "pending" {
			t.Errorf("%s, sold out: order %q", status, st)
		}
	}
}

func TestPayPalCaptureMustMatchTheOrder(t *testing.T) {
	cases := map[string]struct {
		answer  string
		outcome string
		problem string
	}{
		"pending capture": {captureStatus("PENDING", "20.30", "USD", "00112233445566778899aabbccddeeff"), "pending", ""},
		"declined":        {captureStatus("DECLINED", "20.30", "USD", "00112233445566778899aabbccddeeff"), "pending", ""},
		"other custom id": {captureStatus("COMPLETED", "20.30", "USD", "someoneelses"), outcomeMismatch, "amount_mismatch"},
		"no custom id":    {captureStatus("COMPLETED", "20.30", "USD", ""), outcomeMismatch, "amount_mismatch"},
		"less":            {captureStatus("COMPLETED", "20.29", "USD", "00112233445566778899aabbccddeeff"), outcomeMismatch, "amount_mismatch"},
		"more":            {captureStatus("COMPLETED", "21.00", "USD", "00112233445566778899aabbccddeeff"), outcomeMismatch, "amount_mismatch"},
		"other currency":  {captureStatus("COMPLETED", "20.30", "EUR", "00112233445566778899aabbccddeeff"), outcomeMismatch, "amount_mismatch"},
		"odd amount":      {captureStatus("COMPLETED", "20.3x", "USD", "00112233445566778899aabbccddeeff"), outcomeMismatch, "amount_mismatch"},
	}
	for name, c := range cases {
		w := newPayPalShop(t)
		w.answers[ppCapture] = [2]string{"201", c.answer}
		code, status := w.hook(t, w.approvedBody("WH-1"))
		if code != 200 || status != c.outcome || w.status != "pending" || w.attend != "pending" || w.problem != c.problem || len(w.mails) != 0 {
			t.Errorf("%s: %d %q, order %q attendees %q problem %q, %d mails", name, code, status, w.status, w.attend, w.problem, len(w.mails))
		}
	}
	// a capture of a different order's pay code never pays this order, whatever the amount
	if w := newPayPalShop(t); w.payCode != "00112233445566778899aabbccddeeff" {
		t.Fatal("test pay code changed")
	}
}

func captureStatus(status, value, cur, customID string) string {
	return `{"id":"ORD1","status":"COMPLETED","purchase_units":[{"payments":{"captures":[{"id":"CAP1","status":"` + status +
		`","amount":{"value":"` + value + `","currency_code":"` + cur + `"},"custom_id":"` + customID + `"}]}}]}`
}

func TestPayPalWebhookCaptureFailureIsRetriedWithTheSameRequestID(t *testing.T) {
	w := newPayPalShop(t)
	w.answers[ppCapture] = [2]string{"500", `{"message":"` + testPayPalSecret + `"}`}
	body := w.approvedBody("WH-1")
	if code, _, resp := payPalHook(t, body, "", ppHeaders()); code != 503 || strings.Contains(resp, testPayPalSecret) || len(w.events) != 0 || w.status != "pending" {
		t.Fatalf("%d %s, events %v", code, resp, w.events)
	}
	w.answers[ppCapture] = [2]string{"201", w.captureAnswer("COMPLETED", "20.30", "USD", w.payCode)}
	if code, status := w.hook(t, body); code != 200 || status != "paid" {
		t.Fatalf("retry: %d %q", code, status)
	}
	captures := w.fetched(ppCapture)
	if len(captures) != 2 || captures[0].headers["PayPal-Request-Id"] != captures[1].headers["PayPal-Request-Id"] {
		t.Errorf("captures %+v", captures)
	}
	if len(w.mails) != 1 {
		t.Errorf("%d mails", len(w.mails))
	}
}

func TestPayPalWebhookNeedsTimeToCapture(t *testing.T) {
	w := newPayPalShop(t)
	w.tick, w.tickOn = 6*time.Second, "verify-webhook-signature" // verifying took 6 of the 8 seconds
	code, _, _ := payPalHook(t, w.approvedBody("WH-1"), "", ppHeaders())
	if code != 503 || len(w.fetched(ppCapture)) != 0 || w.status != "pending" || len(w.events) != 0 {
		t.Fatalf("%d, %d captures, order %q", code, len(w.fetched(ppCapture)), w.status)
	}
}

func TestPayPalWebhookCaptureCompleted(t *testing.T) {
	w := newPayPalShop(t)
	code, status := w.hook(t, w.captureCompletedBody("WH-2", w.payCode))
	if code != 200 || status != "paid" || w.status != "paid" || len(w.mails) != 1 || len(w.fetched(ppCapture)) != 0 {
		t.Fatalf("%d %q, order %q, %d captures", code, status, w.status, len(w.fetched(ppCapture)))
	}
	// the same payment seen again under another event id: still one mail
	if code, status = w.hook(t, w.captureCompletedBody("WH-3", w.payCode)); code != 200 || status != "duplicate" || len(w.mails) != 1 {
		t.Fatalf("second event: %d %q", code, status)
	}
	// another order's pay code, an unknown order, a capture that is not completed
	w = newPayPalShop(t)
	if code, status = w.hook(t, w.captureCompletedBody("WH-2", "someoneelses")); status != "ignored" || w.status != "pending" {
		t.Errorf("other pay code: %d %q", code, status)
	}
	body := strings.Replace(w.captureCompletedBody("WH-4", w.payCode), `"status":"COMPLETED"`, `"status":"PENDING"`, 1)
	if code, status = w.hook(t, body); status != "ignored" || w.status != "pending" {
		t.Errorf("pending capture: %d %q", code, status)
	}
	body = strings.Replace(w.captureCompletedBody("WH-5", w.payCode), `"value":"20.30"`, `"value":"1.00"`, 1)
	if code, status = w.hook(t, body); status != "mismatch" || w.status != "pending" || w.problem != "amount_mismatch" {
		t.Errorf("wrong amount: %d %q", code, status)
	}
	if code, status = w.hook(t, `{"id":"WH-6","event_type":"PAYMENT.CAPTURE.DENIED","resource":{}}`); status != "ignored" {
		t.Errorf("other event: %d %q", code, status)
	}
}

// A capture of a payment that arrives for an order whose hold ended and whose
// seats are gone is paid back with the PayPal refund API.
func TestPayPalLateCaptureIsPaidBack(t *testing.T) {
	w := newPayPalShop(t)
	w.status, w.attend, w.seats = "expired", "expired", false
	code, status := w.hook(t, w.captureCompletedBody("WH-2", w.payCode))
	if code != 200 || status != "refunded" || w.status != "refunded" || len(w.mails) != 1 || w.mails[0]["subject"] != "Your refund for Gig" {
		t.Fatalf("%d %q, order %q", code, status, w.status)
	}
	refund := w.fetched(ppRefund)
	if len(refund) != 1 || refund[0].headers["PayPal-Request-Id"] != "et-"+w.payCode+"-late-refund" || refund[0].body != `{"amount":{"currency_code":"USD","value":"20.30"}}` {
		t.Fatalf("refund %+v", refund)
	}
}

// The return page and the webhook racing each other: one capture, one mail.
func TestPayPalReturnPageAndWebhookCaptureOnce(t *testing.T) {
	for _, pageFirst := range []bool{true, false} {
		w := newPayPalShop(t)
		visit := func() sdk.HTTPResponse {
			return w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"token": {"ORD1"}, "PayerID": {"PAYER1"}})
		}
		var r sdk.HTTPResponse
		if pageFirst {
			r = visit()
			if code, status := w.hook(t, w.approvedBody("WH-1")); code != 200 || status != "duplicate" {
				t.Fatalf("webhook after the page: %d %q", code, status)
			}
		} else {
			if code, status := w.hook(t, w.approvedBody("WH-1")); code != 200 || status != "paid" {
				t.Fatalf("webhook: %d %q", code, status)
			}
			r = visit()
		}
		if r.Status != 200 || !strings.Contains(r.Body, "Payment received") || len(w.fetched(ppCapture)) != 1 || len(w.mails) != 1 || w.status != "paid" {
			t.Errorf("pageFirst=%v: %d, %d captures, %d mails: %s", pageFirst, r.Status, len(w.fetched(ppCapture)), len(w.mails), r.Body)
		}
	}
}

func TestPayPalReturnPage(t *testing.T) {
	// a token that is not the order's PayPal id captures nothing
	w := newPayPalShop(t)
	r := w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"token": {"OTHER"}})
	if !strings.Contains(r.Body, "Confirming") || len(w.fetches) != 0 || w.status != "pending" {
		t.Fatalf("other order id: %d captures: %s", len(w.fetches), r.Body)
	}
	r = w.page(t, "/return/{token}", w.orderToken(), nil)
	if !strings.Contains(r.Body, "Confirming") || len(w.fetches) != 0 {
		t.Fatalf("no query: %s", r.Body)
	}

	// the hold is gone and the seats too: told so, not charged, no capture
	w = newPayPalShop(t)
	w.status, w.attend, w.seats = "expired", "expired", false
	r = w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"token": {"ORD1"}})
	if !strings.Contains(r.Body, "You were not charged") || len(w.fetched(ppCapture)) != 0 {
		t.Fatalf("sold out: %d captures: %s", len(w.fetched(ppCapture)), r.Body)
	}

	// PayPal cannot be reached: the page still answers
	w = newPayPalShop(t)
	w.answers[ppCapture] = [2]string{"500", `{}`}
	if r = w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"token": {"ORD1"}}); r.Status != 200 || !strings.Contains(r.Body, "Confirming") || w.status != "pending" {
		t.Fatalf("capture fails: %d %s", r.Status, r.Body)
	}
	// and the buyer reloads once PayPal is back
	w.answers[ppCapture] = [2]string{"201", w.captureAnswer("COMPLETED", "20.30", "USD", w.payCode)}
	if r = w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"token": {"ORD1"}}); !strings.Contains(r.Body, "Payment received") || len(w.mails) != 1 {
		t.Fatalf("reload: %s", r.Body)
	}
}

func TestParseAmount(t *testing.T) {
	for _, c := range []struct {
		in, cur string
		want    int64
		ok      bool
	}{
		{"20.30", "USD", 2030, true}, {"20.3", "USD", 2030, true}, {"20", "USD", 2000, true}, {"0.05", "USD", 5, true}, {"0", "USD", 0, true},
		{"1200", "JPY", 1200, true}, {"1200.00", "JPY", 0, false}, {"20.305", "USD", 0, false},
		{"", "USD", 0, false}, {".5", "USD", 0, false}, {"5.", "USD", 0, false}, {"-5.00", "USD", 0, false}, {"+5.00", "USD", 0, false},
		{"1e3", "USD", 0, false}, {"1,000.00", "USD", 0, false}, {" 5.00", "USD", 0, false}, {"9999999999999.00", "USD", 0, false},
	} {
		got, ok := parseAmount(c.in, c.cur)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseAmount(%q, %s) = %d, %v; want %d, %v", c.in, c.cur, got, ok, c.want, c.ok)
		}
	}
	for _, cents := range []int64{0, 5, 99, 100, 2030, 99999999} {
		for _, cur := range []string{"USD", "JPY"} {
			if got, ok := parseAmount(decimalAmount(cents, cur), cur); !ok || got != cents {
				t.Errorf("round trip %d %s: %d %v", cents, cur, got, ok)
			}
		}
	}
}

func TestPayPalSecretsNeverLeave(t *testing.T) {
	w := newPayPalShop(t)
	var responses []string
	run := func(body string) {
		_, _, resp := payPalHook(t, body, "", ppHeaders())
		responses = append(responses, resp)
	}
	w.answers[ppCapture] = [2]string{"500", `{"message":"` + testPayPalSecret + " " + testPayPalToken + `"}`}
	run(w.approvedBody("WH-1")) // capture fails
	w.answers[ppCapture] = [2]string{"201", w.captureAnswer("COMPLETED", "20.30", "USD", w.payCode)}
	run(w.approvedBody("WH-1")) // paid
	run(w.approvedBody("WH-1")) // replay
	w.answers[ppVerify] = [2]string{"200", `{"verification_status":"FAILURE"}`}
	run(w.approvedBody("WH-2")) // not genuine
	page := w.page(t, "/return/{token}", w.orderToken(), map[string][]string{"token": {"ORD1"}})
	responses = append(responses, page.Body)
	if len(w.mails) != 1 {
		t.Fatalf("%d mails", len(w.mails))
	}
	if leaks := w.leaks(responses...); len(leaks) != 0 {
		t.Errorf("secrets left the plugin: %v", leaks)
	}
	for _, r := range responses {
		for _, v := range ppHeaders() {
			if v != "application/json" && strings.Contains(r, v) {
				t.Errorf("a response carries a transmission header value %q", v)
			}
		}
	}
}

// A PENDING capture may still turn into money: the order keeps the capture id,
// which keeps the retention delete away from it; a declined one keeps nothing.
func TestPayPalPendingCaptureKeepsItsID(t *testing.T) {
	w := newPayPalShop(t)
	w.answers[ppCapture] = [2]string{"201", captureStatus("PENDING", "20.30", "USD", w.payCode)}
	if _, status := w.hook(t, w.approvedBody("WH-1")); status != "pending" || w.payRef != "CAP1" {
		t.Errorf("pending: %q, payment_ref %q", status, w.payRef)
	}
	w = newPayPalShop(t)
	w.answers[ppCapture] = [2]string{"201", captureStatus("DECLINED", "20.30", "USD", w.payCode)}
	if _, status := w.hook(t, w.approvedBody("WH-1")); status != "pending" || w.payRef != "" {
		t.Errorf("declined: %q, payment_ref %q", status, w.payRef)
	}
}
