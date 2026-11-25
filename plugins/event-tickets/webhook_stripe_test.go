package main

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// stripeBody is a checkout.session event for the shop's order.
func (w *shop) stripeBody(evID, typ, status string, amount int64, cur string) string {
	return `{"id":"` + evID + `","object":"event","type":"` + typ + `","data":{"object":{"id":"` + w.ref + `","client_reference_id":"` + w.payCode +
		`","payment_status":"` + status + `","amount_total":` + strconv.FormatInt(amount, 10) + `,"currency":"` + cur + `","payment_intent":"pi_1","livemode":false}}}`
}

func (w *shop) signed(body string) map[string]string {
	ts := strconv.FormatInt(w.clock.Unix(), 10)
	return map[string]string{"content-type": "application/json", "Stripe-Signature": "t=" + ts + ",v1=" + sign(w.secret, ts, body)}
}

func stripeHook(t *testing.T, body, b64 string, headers map[string]string) (int, string, string) {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "POST", Pattern: "/stripe/webhook", Body: body, BodyB64: b64, Headers: headers})
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

func (w *shop) deliver(t *testing.T, body string) (int, string) {
	t.Helper()
	code, status, _ := stripeHook(t, body, "", w.signed(body))
	return code, status
}

func TestStripeWebhookPaysTheOrder(t *testing.T) {
	w := newShop(t)
	code, status := w.deliver(t, w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd"))
	if code != 200 || status != "paid" || w.status != "paid" || w.attend != "active" || w.events["evt_1"] != "paid" {
		t.Fatalf("%d %q, order %q attendees %q events %v", code, status, w.status, w.attend, w.events)
	}
	if len(w.mails) != 1 || len(w.queued) != 0 || !strings.Contains(w.mails[0]["html"].(string), "/qr.png") {
		t.Fatalf("%d mails", len(w.mails))
	}
	if len(w.fetches) != 0 {
		t.Errorf("provider called: %+v", w.fetches)
	}
	if f := w.leaks(); len(f) != 0 {
		t.Errorf("secrets in what left the plugin: %v", f)
	}
}

// Nothing is decoded, read or written before the signature holds.
func TestStripeWebhookRejectsBadSignaturesWithoutWriting(t *testing.T) {
	w := newShop(t)
	body := w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd")
	ts := strconv.FormatInt(w.clock.Unix(), 10)
	stale := strconv.FormatInt(w.clock.Add(-301*time.Second).Unix(), 10)
	ct := "application/json"
	cases := map[string]map[string]string{
		"no header":      {"content-type": ct},
		"empty header":   {"content-type": ct, "stripe-signature": ""},
		"malformed":      {"content-type": ct, "stripe-signature": "garbage"},
		"no v1":          {"content-type": ct, "stripe-signature": "t=" + ts},
		"no t":           {"content-type": ct, "stripe-signature": "v1=" + sign(w.secret, ts, body)},
		"wrong secret":   {"content-type": ct, "stripe-signature": "t=" + ts + ",v1=" + sign("whsec_otherfake01", ts, body)},
		"stale":          {"content-type": ct, "stripe-signature": "t=" + stale + ",v1=" + sign(w.secret, stale, body)},
		"other body":     {"content-type": ct, "stripe-signature": "t=" + ts + ",v1=" + sign(w.secret, ts, body+" ")},
		"signed no dot":  {"content-type": ct, "stripe-signature": "t=" + ts + ",v1=" + sign(w.secret, ts+"x", body)},
		"upper-case key": {"content-type": ct, "STRIPE-SIGNATURE": "t=" + ts + ",v1=" + strings.Repeat("0", 64)},
	}
	for name, headers := range cases {
		code, status, resp := stripeHook(t, body, "", headers)
		if code != 400 || status != "bad signature" {
			t.Errorf("%s: %d %s", name, code, resp)
		}
	}
	w.secret = "" // not configured: nothing can be verified
	ts2 := strconv.FormatInt(w.clock.Unix(), 10)
	if code, _, _ := stripeHook(t, body, "", map[string]string{"content-type": ct, "stripe-signature": "t=" + ts2 + ",v1=" + sign("", ts2, body)}); code != 400 {
		t.Errorf("no secret configured: %d", code)
	}
	if w.dbCalls != 0 || len(w.writes) != 0 || len(w.mails) != 0 || len(w.fetches) != 0 || w.status != "pending" {
		t.Fatalf("a rejected webhook touched something: %d db calls, %d mails", w.dbCalls, len(w.mails))
	}
}

func TestStripeWebhookRejectsBadRequests(t *testing.T) {
	w := newShop(t)
	body := w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd")
	good := w.signed(body)
	other := func(h map[string]string, ct string) map[string]string {
		c := map[string]string{}
		for k, v := range h {
			c[k] = v
		}
		c["content-type"] = ct
		return c
	}
	if code, _, _ := stripeHook(t, body, "", other(good, "text/plain")); code != 400 {
		t.Errorf("content type: %d", code)
	}
	if code, _, _ := stripeHook(t, body, "", other(good, "")); code != 400 {
		t.Errorf("no content type: %d", code)
	}
	if code, _, _ := stripeHook(t, body, "e30=", good); code != 400 {
		t.Errorf("body_b64: %d", code)
	}
	if code, _, _ := stripeHook(t, "", "", good); code != 400 {
		t.Errorf("empty: %d", code)
	}
	big := body + strings.Repeat(" ", maxWebhookBody)
	if code, _, _ := stripeHook(t, big, "", w.signed(big)); code != 400 {
		t.Errorf("oversized: %d", code)
	}
	// signed but not an event
	for name, junk := range map[string]string{"not json": `not json`, "no id": `{"type":"checkout.session.completed"}`, "bad id": `{"id":"x/../y","type":"checkout.session.completed"}`} {
		if code, _ := w.deliver(t, junk); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if w.dbCalls != 0 || len(w.mails) != 0 {
		t.Fatalf("a rejected webhook touched something: %d db calls", w.dbCalls)
	}
}

func TestStripeWebhookReplayDoesNothing(t *testing.T) {
	w := newShop(t)
	body := w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd")
	if code, status := w.deliver(t, body); code != 200 || status != "paid" {
		t.Fatalf("%d %q", code, status)
	}
	writes := len(w.writes)
	for range 3 {
		code, status := w.deliver(t, body)
		if code != 200 || status != "duplicate" {
			t.Fatalf("replay: %d %q", code, status)
		}
	}
	if len(w.writes) != writes || len(w.mails) != 1 || len(w.fetches) != 0 {
		t.Errorf("a replay wrote %d statements, sent %d mails, made %d calls", len(w.writes)-writes, len(w.mails), len(w.fetches))
	}
}

// The completed event and the later async-succeeded event are two events
// for one payment: still one transition and one mail.
func TestStripeWebhookTwoEventsOnePayment(t *testing.T) {
	w := newShop(t)
	for _, ev := range []string{"checkout.session.completed", "checkout.session.async_payment_succeeded"} {
		if code, _ := w.deliver(t, w.stripeBody("evt_"+ev[len(ev)-5:], ev, "paid", 2030, "usd")); code != 200 {
			t.Fatalf("%s: %d", ev, code)
		}
	}
	if len(w.mails) != 1 || w.status != "paid" {
		t.Fatalf("%d mails, order %q", len(w.mails), w.status)
	}
}

func TestStripeWebhookUnpaidSessionWaits(t *testing.T) {
	w := newShop(t)
	code, status := w.deliver(t, w.stripeBody("evt_1", "checkout.session.completed", "unpaid", 2030, "usd"))
	if code != 200 || status != "ignored" || w.status != "pending" || len(w.mails) != 0 {
		t.Fatalf("%d %q, order %q", code, status, w.status)
	}
	if code, status = w.deliver(t, w.stripeBody("evt_2", "checkout.session.async_payment_succeeded", "paid", 2030, "usd")); code != 200 || status != "paid" || w.status != "paid" {
		t.Fatalf("async: %d %q, order %q", code, status, w.status)
	}
}

func TestStripeWebhookAmountMismatch(t *testing.T) {
	for name, body := range map[string]func(w *shop) string{
		"amount":   func(w *shop) string { return w.stripeBody("evt_1", "checkout.session.completed", "paid", 1, "usd") },
		"currency": func(w *shop) string { return w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "eur") },
	} {
		w := newShop(t)
		code, status := w.deliver(t, body(w))
		if code != 200 || status != "mismatch" || w.status != "pending" || w.attend != "pending" || w.problem != "amount_mismatch" || len(w.mails) != 0 {
			t.Errorf("%s: %d %q, order %q attendees %q problem %q, %d mails", name, code, status, w.status, w.attend, w.problem, len(w.mails))
		}
	}
}

func TestStripeWebhookOtherOrdersAndEventsAreIgnored(t *testing.T) {
	w := newShop(t)
	body := strings.Replace(w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd"), w.payCode, "someoneelses", 1)
	if code, status := w.deliver(t, body); code != 200 || status != "ignored" || w.status != "pending" || len(w.mails) != 0 || len(w.writes) != 0 {
		t.Errorf("wrong pay code: %d %q", code, status)
	}
	body = strings.Replace(w.stripeBody("evt_2", "checkout.session.completed", "paid", 2030, "usd"), `"id":"cs_test_1"`, `"id":"cs_test_9"`, 1)
	if code, status := w.deliver(t, body); code != 200 || status != "ignored" || w.status != "pending" {
		t.Errorf("unknown session: %d %q", code, status)
	}
	w.dbCalls = 0
	if code, status := w.deliver(t, `{"id":"evt_3","type":"charge.succeeded","data":{"object":{}}}`); code != 200 || status != "ignored" || w.dbCalls != 0 {
		t.Errorf("other event: %d %q, %d db calls", code, status, w.dbCalls)
	}
}

func TestStripeWebhookSessionExpiredReleasesTheHold(t *testing.T) {
	w := newShop(t)
	code, status := w.deliver(t, w.stripeBody("evt_1", "checkout.session.expired", "unpaid", 2030, "usd"))
	if code != 200 || status != "expired" || w.status != "expired" || w.released != 1 || len(w.mails) != 0 {
		t.Fatalf("%d %q, order %q", code, status, w.status)
	}
	// a paid order is not released by a late "expired" event
	w = newShop(t)
	w.status, w.paid = "paid", true
	if code, _ = w.deliver(t, w.stripeBody("evt_2", "checkout.session.expired", "unpaid", 2030, "usd")); code != 200 || w.status != "paid" {
		t.Fatalf("paid order: %d %q", code, w.status)
	}
}

// A payment that arrives after the hold ended.
func TestStripeWebhookLatePayment(t *testing.T) {
	w := newShop(t)
	w.status, w.attend = "expired", "expired"
	code, status := w.deliver(t, w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd"))
	if code != 200 || status != "paid" || w.status != "paid" || w.attend != "active" || len(w.mails) != 1 || len(w.refundCalls()) != 0 {
		t.Fatalf("seats free: %d %q, order %q", code, status, w.status)
	}

	w = newShop(t)
	w.status, w.attend, w.seats = "expired", "expired", false
	code, status = w.deliver(t, w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd"))
	if code != 200 || status != "refunded" || w.status != "refunded" || len(w.refundCalls()) != 1 || len(w.mails) != 1 ||
		w.mails[0]["subject"] != "Your refund for Gig" {
		t.Fatalf("sold out: %d %q, order %q, %d refund calls, mails %v", code, status, w.status, len(w.refundCalls()), w.mails)
	}
	form, _ := url.ParseQuery(w.refundCalls()[0].body)
	if form.Get("payment_intent") != "pi_1" || form.Get("amount") != "2030" || w.refundCalls()[0].headers["Idempotency-Key"] != "et-42-late-refund" {
		t.Errorf("refund %v %v", form, w.refundCalls()[0].headers)
	}

	// the refund fails: 503 so Stripe sends it again, and the event is not remembered
	w = newShop(t)
	w.status, w.attend, w.seats = "expired", "expired", false
	w.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"500", `{}`}
	body := w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd")
	if code, _ = w.deliver(t, body); code != 503 || w.problem != "refund_failed" || len(w.events) != 0 || len(w.mails) != 0 {
		t.Fatalf("refund fails: %d, problem %q, events %v", code, w.problem, w.events)
	}
	w.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"200", `{"id":"re_1"}`}
	if code, status = w.deliver(t, body); code != 200 || status != "refunded" || w.status != "refunded" {
		t.Fatalf("retry: %d %q", code, status)
	}
}

func TestStripeWebhookTroubleIsA503(t *testing.T) {
	w := newShop(t)
	body := w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd")
	headers := w.signed(body)
	orig := callHost
	callHost = func(fn string, args, out any) error {
		if fn == sdk.FnDBQuery {
			return &sdk.HostError{Code: sdk.ErrUnavailable, Message: "pgx: password=" + testStripeKey}
		}
		return orig(fn, args, out)
	}
	code, _, resp := stripeHook(t, body, "", headers)
	if code != 503 || strings.Contains(resp, "pgx") || strings.Contains(resp, "sk_") {
		t.Fatalf("%d %s", code, resp)
	}
}

// No secret, signature or provider answer reaches a response, a mail, a
// queued row or a written value over a full run of the Stripe paths.
func TestSecretsNeverLeave(t *testing.T) {
	w := newShop(t)
	var responses []string
	run := func(body string, headers map[string]string) {
		_, _, resp := stripeHook(t, body, "", headers)
		responses = append(responses, resp)
	}
	paid := w.stripeBody("evt_1", "checkout.session.completed", "paid", 2030, "usd")
	hdr := w.signed(paid)
	run(paid, hdr) // paid
	run(paid, hdr) // replay
	bad := map[string]string{"content-type": "application/json", "stripe-signature": hdr["Stripe-Signature"] + "00"}
	run(paid, bad) // bad signature
	w.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"500", `{"error":{"message":"bad key ` + testStripeKey + ` ` + testStripeSecret + `"}}`}
	w2 := w
	w2.status, w2.attend, w2.seats, w2.paid = "expired", "expired", false, false
	late := w2.stripeBody("evt_2", "checkout.session.completed", "paid", 2030, "usd")
	run(late, w2.signed(late)) // refund fails
	w2.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"200", `{"id":"re_1"}`}
	late3 := w2.stripeBody("evt_3", "checkout.session.completed", "paid", 2030, "usd")
	run(late3, w2.signed(late3)) // refunded, refund mail
	responses = append(responses, hdr["Stripe-Signature"])
	if len(w.mails) < 2 {
		t.Fatalf("only %d mails; the run did not cover the mails", len(w.mails))
	}
	leaks := w.leaks(responses[:len(responses)-1]...)
	for _, r := range responses[:len(responses)-1] {
		if strings.Contains(r, hdr["Stripe-Signature"]) || strings.Contains(r, sign(w.secret, strconv.FormatInt(w.clock.Unix(), 10), paid)) {
			t.Error("a response carries the signature")
		}
	}
	if len(leaks) != 0 {
		t.Errorf("secrets left the plugin: %v", leaks)
	}
}
