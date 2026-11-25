package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// payHost fakes the host for /quote and /checkout: settings, the event, the
// database (each statement answered by a canned cell) and the provider APIs.
type payHost struct {
	settings string
	types    string
	counts   string
	coupon   string // answer of lookupCoupon
	bundles  string
	hold     string // answer of holdCart
	complete string // answer of completeFree
	setRef   int64  // rows the provider_ref update changes
	answers  map[string][2]string
	key      []byte
	clock    time.Time

	calls   []call
	fetches []fetched
}

const payTypes = `[{"id":5,"event_id":"e1","name":"GA","capacity":10,"taken":4,"waitlist":false,"on_sale":true,"position":0,"price_cents":1000},
{"id":6,"event_id":"e1","name":"Kids","capacity":10,"taken":0,"waitlist":false,"on_sale":true,"position":1,"price_cents":0},
{"id":7,"event_id":"e1","name":"Closed","capacity":10,"taken":0,"waitlist":false,"on_sale":false,"position":2,"price_cents":500}]`

func newPayHost(t *testing.T) *payHost {
	t.Helper()
	h := &payHost{
		settings: `{"stripe_secret_key":"` + testStripeKey + `","stripe_webhook_secret":"` + testStripeSecret + `","paypal_client_id":"` + testPayPalClient +
			`","paypal_secret":"` + testPayPalSecret + `","paypal_sandbox":true,"fee_fixed_cents":30}`,
		types: payTypes, counts: `{"by_email":0,"hour":0,"waiting":0,"pending_by_email":0}`, coupon: `null`, bundles: `[]`,
		hold: `{"order_id":77,"full":[],"changed":[],"coupon_gone":false}`, complete: `true`, setRef: 1,
		answers: map[string][2]string{}, key: []byte("0123456789abcdef0123456789abcdef"),
		clock: time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = [2]string{"200", `{"id":"cs_test_1","url":"https://checkout.stripe.com/c/pay/cs_test_1"}`}
	h.answers["POST https://api.stripe.com/v1/checkout/sessions/cs_test_1/expire"] = [2]string{"200", `{}`}
	h.answers["POST "+sandboxAPI+"/v1/oauth2/token"] = [2]string{"200", `{"access_token":"` + testPayPalToken + `","expires_in":32400}`}
	h.answers["POST "+sandboxAPI+"/v2/checkout/orders"] = [2]string{"201", `{"id":"ORD1","links":[{"rel":"payer-action","href":"https://www.sandbox.paypal.com/checkoutnow?token=ORD1"}]}`}
	now = func() time.Time { return h.clock }
	t.Cleanup(func() { now = time.Now })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		if fn == sdk.FnDBQuery || fn == sdk.FnDBExec {
			h.calls = append(h.calls, call{fn, sql, ar})
		}
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(h.settings, out)
		case fn == sdk.FnEntitiesGet:
			return jsonInto(gigEntity, out)
		case fn == sdk.FnHTTPFetch:
			f := fetched{method: a["method"].(string), url: a["url"].(string), headers: a["headers"].(map[string]string), timeoutMS: a["timeout_ms"].(int)}
			f.body, _ = a["body"].(string)
			h.fetches = append(h.fetches, f)
			ans, ok := h.answers[f.method+" "+f.url]
			if !ok {
				ans = [2]string{"404", `{}`}
			}
			return jsonInto(`{"status":`+ans[0]+`,"body":`+jsonText(ans[1])+`}`, out)
		case strings.HasPrefix(sql, "INSERT INTO meta"):
			return nil
		case strings.Contains(sql, "FROM meta WHERE key"):
			if ar[0] == tokenMetaKey {
				return cell(`null`)
			}
			return cell(`"` + hex.EncodeToString(h.key) + `"`)
		case strings.Contains(sql, "FROM ticket_types WHERE event_id"):
			return cell(h.types)
		case strings.Contains(sql, "FROM fields"):
			return cell(`[]`)
		case strings.Contains(sql, "'by_email'"):
			return cell(h.counts)
		case strings.Contains(sql, "INSERT INTO orders"):
			return cell(h.hold)
		case strings.Contains(sql, "FROM bundle_rules"):
			return cell(h.bundles)
		case strings.Contains(sql, "FROM coupons"):
			return cell(h.coupon)
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = 'expired'"):
			return cell(`{"released":0,"sessions":[]}`)
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = $2"):
			return cell(`true`)
		case strings.Contains(sql, "total_cents = 0 RETURNING"):
			return cell(h.complete)
		case strings.HasPrefix(sql, "UPDATE orders SET provider_ref"):
			return jsonInto(`{"rows_affected":`+strconv.FormatInt(h.setRef, 10)+`}`, out)
		case strings.Contains(sql, "FROM orders o WHERE o.id"):
			return cell(`null`) // the confirmation has nothing to send in this fake
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

// same compares a statement argument (a Go value) with what the test expects.
func same(got, want any) bool { return fmt.Sprint(got) == fmt.Sprint(want) }

func (h *payHost) token(t *testing.T) string {
	t.Helper()
	issued := strconv.FormatInt(h.clock.Add(-10*time.Second).UnixMilli(), 10)
	return issued + "." + tokenMAC(h.key, issued)
}

func (h *payHost) with(sqlPart string) []call {
	var out []call
	for _, c := range h.calls {
		if strings.Contains(c.sql, sqlPart) {
			out = append(out, c)
		}
	}
	return out
}

func postJSON(t *testing.T, pattern, contentType, body string, b64 string) (int, map[string]any) {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "POST", Pattern: pattern, Body: body, BodyB64: b64,
		Headers: map[string]string{"content-type": contentType}})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var resp sdk.HTTPResponse
	_ = json.Unmarshal(out, &resp)
	var m map[string]any
	_ = json.Unmarshal([]byte(resp.Body), &m)
	return resp.Status, m
}

func (h *payHost) checkout(t *testing.T, fields map[string]any) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"event": "e1", "name": "Ana", "email": "ana@example.com", "token": h.token(t), "provider": "stripe",
		"items": items(5, 2)}
	for k, v := range fields {
		body[k] = v
	}
	return postJSON(t, "/checkout", "application/json", jsonText(body), "")
}

func TestCheckoutRefusesBadPosts(t *testing.T) {
	h := newPayHost(t)
	good := jsonText(map[string]any{"event": "e1", "name": "Ana", "email": "ana@example.com", "token": h.token(t), "provider": "stripe", "items": items(5, 2)})
	if code, _ := postJSON(t, "/checkout", "text/plain", good, ""); code != 415 {
		t.Errorf("content type: %d", code)
	}
	if code, _ := postJSON(t, "/checkout", "application/json", good, "eA=="); code != 400 {
		t.Errorf("body_b64: %d", code)
	}
	if code, _ := postJSON(t, "/checkout", "application/json", good+strings.Repeat(" ", maxCheckoutBody), ""); code != 413 {
		t.Errorf("oversized: %d", code)
	}
	if code, _ := postJSON(t, "/checkout", "application/json", good+` {}`, ""); code != 400 {
		t.Errorf("trailing data: %d", code)
	}
	for _, key := range []string{"price", "price_cents", "amount", "total", "unit", "total_cents", "discount", "fee", "currency"} {
		code, _ := h.checkout(t, map[string]any{key: 1})
		if code != 400 {
			t.Errorf("unknown key %q: %d", key, code)
		}
	}
	for name, fields := range map[string]map[string]any{
		"no provider":      {"provider": ""},
		"other provider":   {"provider": "square"},
		"no items":         {"items": []any{}},
		"bad event id":     {"event": "../x"},
		"no name":          {"name": " "},
		"bad email":        {"email": "not-an-address"},
		"unknown type":     {"items": items(99, 1)},
		"type off sale":    {"items": items(7, 1)},
		"free cart":        {"items": items(6, 2)},
		"repeated type":    {"items": items(5, 1, 5, 1)},
		"too many tickets": {"items": items(5, 11)},
	} {
		if code, _ := h.checkout(t, fields); code != 422 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if len(h.with("INSERT INTO orders")) != 0 || len(h.fetches) != 0 {
		t.Fatal("a refused post held seats or called a provider")
	}
}

func TestCheckoutBotsGetOKAndStoreNothing(t *testing.T) {
	h := newPayHost(t)
	code, m := h.checkout(t, map[string]any{"website": "http://spam.example"})
	if code != 200 || m["ok"] != true || m["redirect"] != nil {
		t.Errorf("honeypot: %d %v", code, m)
	}
	code, m = h.checkout(t, map[string]any{"token": "1.2"})
	if code != 200 || m["ok"] != true || m["redirect"] != nil {
		t.Errorf("bad token: %d %v", code, m)
	}
	h.clock = h.clock.Add(2 * time.Hour) // the token went stale
	if code, m = postJSON(t, "/checkout", "application/json", jsonText(map[string]any{"event": "e1", "name": "Ana", "email": "ana@example.com",
		"token": "x", "provider": "stripe", "items": items(5, 1)}), ""); code != 200 || m["redirect"] != nil {
		t.Errorf("stale token: %d %v", code, m)
	}
	if len(h.with("INSERT INTO orders")) != 0 || len(h.fetches) != 0 {
		t.Fatal("a bot held seats or called a provider")
	}
}

// A priced cart is held by one statement (never the compensating free path),
// the buyer is sent to Stripe, and the amount is the server's.
func TestCheckoutStripe(t *testing.T) {
	h := newPayHost(t)
	code, m := h.checkout(t, map[string]any{"items": items(5, 2, 6, 1)})
	if code != 200 || m["ok"] != true || m["redirect"] != "https://checkout.stripe.com/c/pay/cs_test_1" {
		t.Fatalf("%d %v", code, m)
	}
	holds := h.with("INSERT INTO orders")
	if len(holds) != 1 {
		t.Fatalf("%d hold statements", len(holds))
	}
	for _, part := range []string{"FOR UPDATE", "ORDER BY t.id", "taken + w.qty <= tt.capacity", "UPDATE coupons"} {
		if !strings.Contains(holds[0].sql, part) {
			t.Errorf("hold statement lacks %q", part)
		}
	}
	for _, c := range h.calls {
		if strings.Contains(c.sql, "reserve") || strings.HasPrefix(c.sql, "DELETE FROM orders") || strings.HasPrefix(c.sql, "WITH c AS (UPDATE attendees") {
			t.Errorf("the free path ran: %s", c.sql)
		}
	}
	// 2 x 10.00 from the database + the 0.30 fee: $1 = event, $13 = total, $14 = provider
	ar := holds[0].args
	if !same(ar[0], "e1") || !same(ar[8], 2000) || !same(ar[10], 30) || !same(ar[12], 2030) || !same(ar[13], "stripe") || !same(ar[6], "USD") {
		t.Errorf("hold args %v", ar)
	}
	var codes []holdCode
	_ = json.Unmarshal([]byte(ar[2].(string)), &codes)
	if len(codes) != 3 || codes[0].Type != 5 || codes[2].Type != 6 {
		t.Errorf("codes %+v", codes)
	}
	var lines []cartLine
	_ = json.Unmarshal([]byte(ar[1].(string)), &lines)
	if len(lines) != 2 || lines[0].Unit != 1000 || lines[1].Unit != 0 {
		t.Errorf("lines %+v", lines)
	}

	if len(h.fetches) != 1 || h.fetches[0].method != "POST" || h.fetches[0].url != "https://api.stripe.com/v1/checkout/sessions" {
		t.Fatalf("fetches %+v", h.fetches)
	}
	form, _ := url.ParseQuery(h.fetches[0].body)
	payCode := ar[14].(string)
	if form.Get("line_items[0][price_data][unit_amount]") != "2030" || form.Get("client_reference_id") != payCode || form.Get("metadata[order_id]") != "77" ||
		form.Get("customer_email") != "ana@example.com" {
		t.Errorf("form %v", form)
	}
	tok := signCode(h.key, kindOrder, payCode)
	if form.Get("success_url") != "https://example.org/p/event-tickets/return/"+tok+"?session_id={CHECKOUT_SESSION_ID}" ||
		form.Get("cancel_url") != "https://example.org/p/event-tickets/cancel/"+tok {
		t.Errorf("urls %q %q", form.Get("success_url"), form.Get("cancel_url"))
	}
	if set := h.with("UPDATE orders SET provider_ref"); len(set) != 1 || !same(set[0].args[0], 77) || !same(set[0].args[1], "cs_test_1") {
		t.Errorf("provider_ref %+v", set)
	}
	if len(h.with("WITH o AS (UPDATE orders SET status = $2")) != 0 {
		t.Error("the hold was released on success")
	}
	if len(h.with("expires_at < now()")) != 1 {
		t.Error("lapsed holds of the event were not released first")
	}
}

func TestCheckoutPayPal(t *testing.T) {
	h := newPayHost(t)
	code, m := h.checkout(t, map[string]any{"provider": "paypal"})
	if code != 200 || m["redirect"] != "https://www.sandbox.paypal.com/checkoutnow?token=ORD1" {
		t.Fatalf("%d %v", code, m)
	}
	if len(h.fetches) != 2 || !strings.HasSuffix(h.fetches[0].url, "/v1/oauth2/token") || !strings.HasSuffix(h.fetches[1].url, "/v2/checkout/orders") {
		t.Fatalf("at most the token and the order: %+v", h.fetches)
	}
	if !same(h.with("INSERT INTO orders")[0].args[13], "paypal") {
		t.Error("provider not recorded")
	}
	if set := h.with("UPDATE orders SET provider_ref"); len(set) != 1 || !same(set[0].args[1], "ORD1") {
		t.Errorf("provider_ref %+v", set)
	}
}

func TestCheckoutNeedsAReadyProvider(t *testing.T) {
	h := newPayHost(t)
	h.settings = `{"stripe_secret_key":"` + testStripeKey + `"}` // no webhook secret
	for _, p := range []string{"stripe", "paypal"} {
		if code, _ := h.checkout(t, map[string]any{"provider": p}); code != 503 {
			t.Errorf("%s: %d", p, code)
		}
	}
	h.settings = `{"stripe_secret_key":"` + testStripeKey + `","stripe_webhook_secret":"` + testStripeSecret + `","currency":"KWD"}`
	if code, _ := h.checkout(t, nil); code != 503 {
		t.Errorf("three-decimal currency: %d", code)
	}
	if len(h.with("INSERT INTO orders")) != 0 || len(h.fetches) != 0 {
		t.Fatal("a held cart without a ready provider")
	}
}

func TestCheckoutCaps(t *testing.T) {
	h := newPayHost(t)
	h.counts = `{"by_email":0,"hour":0,"waiting":0,"pending_by_email":2}`
	if code, _ := h.checkout(t, nil); code != 429 {
		t.Errorf("two open holds: %d", code)
	}
	h.counts = `{"by_email":0,"hour":200,"waiting":0,"pending_by_email":0}`
	if code, _ := h.checkout(t, nil); code != 429 {
		t.Errorf("orders per hour: %d", code)
	}
	h.counts = `{"by_email":0,"hour":0,"waiting":0,"pending_by_email":0,"open_holds":20}`
	if code, _ := h.checkout(t, nil); code != 429 {
		t.Errorf("open holds of the event: %d", code)
	}
	if len(h.with("INSERT INTO orders")) != 0 {
		t.Fatal("a capped post held seats")
	}
}

func TestCheckoutSoldOutAndChanged(t *testing.T) {
	h := newPayHost(t)
	h.hold = `{"order_id":null,"full":[5],"changed":[],"coupon_gone":false}`
	code, m := h.checkout(t, nil)
	if code != 409 || m["error"] != "sold out" || jsonText(m["sold_out"]) != `[5]` {
		t.Errorf("sold out: %d %v", code, m)
	}
	h.hold = `{"order_id":null,"full":[],"changed":[5],"coupon_gone":false}`
	if code, m = h.checkout(t, nil); code != 409 || m["error"] != "changed" || jsonText(m["changed"]) != `[5]` {
		t.Errorf("changed: %d %v", code, m)
	}
	h.hold = `{"order_id":null,"full":[],"changed":[],"coupon_gone":true}`
	if code, m = h.checkout(t, nil); code != 409 || m["error"] != "coupon" {
		t.Errorf("coupon gone: %d %v", code, m)
	}
	if len(h.fetches) != 0 || len(h.with("WITH o AS (UPDATE orders SET status = $2")) != 0 {
		t.Fatal("nothing was held, so no provider call and no release")
	}
}

func TestCheckoutCouponIsPricedServerSide(t *testing.T) {
	h := newPayHost(t)
	h.coupon = `{"id":4,"code":"SAVE10","kind":"percent","value":10,"event_id":"","type_id":0}`
	code, m := h.checkout(t, map[string]any{"coupon": "save10"})
	if code != 200 || m["redirect"] == nil {
		t.Fatalf("%d %v", code, m)
	}
	// 2000 - 200 = 1800, + 30 fee
	ar := h.with("INSERT INTO orders")[0].args
	if !same(ar[7], 4) || !same(ar[9], 200) || !same(ar[12], 1830) {
		t.Errorf("hold args %v", ar)
	}
	if !same(h.with("FROM coupons")[0].args[0], "SAVE10") {
		t.Errorf("coupon lookup %v", h.with("FROM coupons")[0].args)
	}

	h = newPayHost(t) // a code nobody knows
	if code, m = h.checkout(t, map[string]any{"coupon": "NOPE"}); code != 409 || m["error"] != "coupon" {
		t.Errorf("unknown coupon: %d %v", code, m)
	}
	if len(h.with("INSERT INTO orders")) != 0 {
		t.Error("an unknown coupon held seats")
	}
}

// A provider that answers with a page outside the checkout hosts, fails or
// cannot be recorded leaves nothing held.
func TestCheckoutReleasesTheHoldWhenPaymentCannotStart(t *testing.T) {
	cases := map[string]func(h *payHost){
		"evil host": func(h *payHost) {
			h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = [2]string{"200", `{"id":"cs_test_1","url":"https://evil.example.org/c/pay"}`}
		},
		"http scheme": func(h *payHost) {
			h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = [2]string{"200", `{"id":"cs_test_1","url":"http://checkout.stripe.com/c/pay"}`}
		},
		"userinfo trick": func(h *payHost) {
			h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = [2]string{"200", `{"id":"cs_test_1","url":"https://checkout.stripe.com@evil.example.org/"}`}
		},
		"provider 500": func(h *payHost) {
			h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = [2]string{"500", `{"error":{"message":"` + testStripeKey + `"}}`}
		},
		"provider refuses": func(h *payHost) {
			h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = [2]string{"400", `{}`}
		},
		"reference not stored": func(h *payHost) { h.setRef = 0 },
	}
	for name, setup := range cases {
		h := newPayHost(t)
		setup(h)
		code, m := h.checkout(t, nil)
		if code != 503 || m["ok"] != false || m["redirect"] != nil || m["error"] != laterError {
			t.Errorf("%s: %d %v", name, code, m)
		}
		rel := h.with("WITH o AS (UPDATE orders SET status = $2")
		if len(rel) != 1 || !same(rel[0].args[0], 77) || !same(rel[0].args[1], "cancelled") {
			t.Errorf("%s: release %+v", name, rel)
		}
		for _, secret := range []string{testStripeKey, testStripeSecret} {
			if b, _ := json.Marshal(m); strings.Contains(string(b), secret) {
				t.Errorf("%s: the answer carries a secret", name)
			}
		}
	}
	h := newPayHost(t)
	h.answers["POST "+sandboxAPI+"/v2/checkout/orders"] = [2]string{"201", `{"id":"ORD1","links":[{"rel":"payer-action","href":"https://evil.example.org/x"}]}`}
	if code, _ := h.checkout(t, map[string]any{"provider": "paypal"}); code != 503 || len(h.with("WITH o AS (UPDATE orders SET status = $2")) != 1 {
		t.Errorf("paypal evil host: %d", code)
	}
}

// A coupon that covers everything makes a free order: no provider is called.
func TestCheckoutZeroTotalCompletesAtOnce(t *testing.T) {
	h := newPayHost(t)
	h.settings = `{"stripe_secret_key":"` + testStripeKey + `","stripe_webhook_secret":"` + testStripeSecret + `"}`
	h.coupon = `{"id":4,"code":"FREE100","kind":"percent","value":100,"event_id":"","type_id":0}`
	code, m := h.checkout(t, map[string]any{"coupon": "FREE100"})
	tickets, _ := m["tickets"].([]any)
	if code != 200 || len(tickets) != 2 || m["redirect"] != nil {
		t.Fatalf("%d %v", code, m)
	}
	for _, p := range tickets {
		if !strings.HasPrefix(p.(string), "/p/event-tickets/ticket/") {
			t.Errorf("ticket link %v", p)
		}
	}
	ar := h.with("INSERT INTO orders")[0].args
	if !same(ar[12], 0) || !same(ar[13], "none") {
		t.Errorf("hold args %v", ar)
	}
	if len(h.fetches) != 0 {
		t.Errorf("provider called for a zero total: %+v", h.fetches)
	}
	if len(h.with("total_cents = 0 RETURNING")) != 1 || len(h.with("FROM orders o WHERE o.id")) != 1 {
		t.Error("the order was not completed and mailed once")
	}
}

func TestCheckoutZeroTotalFailureReleasesTheHold(t *testing.T) {
	h := newPayHost(t)
	h.coupon = `{"id":4,"code":"FREE100","kind":"percent","value":100,"event_id":"","type_id":0}`
	h.complete = `{"not":"a bool"}`
	if code, _ := h.checkout(t, map[string]any{"coupon": "FREE100"}); code != 503 || len(h.with("WITH o AS (UPDATE orders SET status = $2")) != 1 {
		t.Fatalf("%d", code)
	}
}

func postQuote(t *testing.T, h *payHost, fields map[string]any) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"event": "e1", "items": items(5, 2, 6, 1)}
	for k, v := range fields {
		body[k] = v
	}
	return postJSON(t, "/quote", "application/json", jsonText(body), "")
}

func TestQuote(t *testing.T) {
	h := newPayHost(t)
	h.coupon = `{"id":4,"code":"SAVE10","kind":"percent","value":10,"event_id":"","type_id":0}`
	h.bundles = `[{"id":1,"type_ids":[],"all_types":false,"min_qty":3,"kind":"fixed","value":100}]`
	code, m := postQuote(t, h, map[string]any{"coupon": "SAVE10"})
	q, _ := m["quote"].(map[string]any)
	if code != 200 || m["coupon_ok"] != true || q["subtotal_cents"] != float64(2000) || q["bundle_cents"] != float64(100) ||
		q["coupon_cents"] != float64(200) || q["discount_cents"] != float64(300) || q["fee_cents"] != float64(30) || q["total_cents"] != float64(1730) ||
		q["currency"] != "USD" {
		t.Fatalf("%d %v", code, m)
	}
	if len(h.with("INSERT")) != 0 || len(h.fetches) != 0 {
		t.Error("a quote wrote or called a provider")
	}
	// the island shows these texts and adds up nothing itself
	d, _ := m["display"].(map[string]any)
	if d["subtotal"] != "20.00 USD" || d["discount"] != "3.00 USD" || d["fee"] != "0.30 USD" || d["total"] != "17.30 USD" {
		t.Errorf("display %v", d)
	}

	h.coupon = `null`
	code, m = postQuote(t, h, map[string]any{"coupon": "NOPE"})
	q, _ = m["quote"].(map[string]any)
	if code != 200 || m["coupon_ok"] != false || q["total_cents"] != float64(1930) {
		t.Errorf("unknown coupon: %d %v", code, m)
	}
}

func TestQuoteRefusesBadPosts(t *testing.T) {
	h := newPayHost(t)
	for _, key := range []string{"price", "total", "unit", "currency", "provider"} {
		if code, _ := postQuote(t, h, map[string]any{key: 1}); code != 400 {
			t.Errorf("unknown key %q: %d", key, code)
		}
	}
	if code, _ := postJSON(t, "/quote", "text/plain", `{}`, ""); code != 415 {
		t.Errorf("content type: %d", code)
	}
	if code, _ := postJSON(t, "/quote", "application/json", `{"event":"e1","items":[{"type":5,"qty":1}]}`+strings.Repeat(" ", maxQuoteBody), ""); code != 413 {
		t.Errorf("oversized: %d", code)
	}
	for name, fields := range map[string]map[string]any{
		"no items": {"items": []any{}}, "bad event": {"event": "a b"}, "type off sale": {"items": items(7, 1)}, "unknown type": {"items": items(99, 1)},
	} {
		if code, _ := postQuote(t, h, fields); code != 422 {
			t.Errorf("%s: %d", name, code)
		}
	}
}

func TestTokenListsTheReadyProviders(t *testing.T) {
	h := newPayHost(t)
	providers := func() (string, []string) {
		r := publicGet(t, "/token", nil, map[string][]string{"event": {"e1"}})
		var b struct {
			Currency  string
			Providers []string
		}
		if err := json.Unmarshal([]byte(r.Body), &b); err != nil || r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		for _, secret := range []string{testStripeKey, testStripeSecret, testPayPalSecret} {
			if strings.Contains(r.Body, secret) {
				t.Errorf("the token answer holds a secret: %s", r.Body)
			}
		}
		return b.Currency, b.Providers
	}
	if cur, got := providers(); cur != "USD" || !slices.Equal(got, []string{"stripe", "paypal"}) {
		t.Errorf("both set up: %q %v", cur, got)
	}
	h.settings = `{"stripe_secret_key":"` + testStripeKey + `","stripe_webhook_secret":"` + testStripeSecret + `"}`
	if _, got := providers(); !slices.Equal(got, []string{"stripe"}) {
		t.Errorf("only Stripe: %v", got)
	}
	h.settings = `{}`
	if _, got := providers(); got == nil || len(got) != 0 {
		t.Errorf("none set up must be an empty list: %v", got)
	}
	h.settings = `{"currency":"KWD","stripe_secret_key":"` + testStripeKey + `","stripe_webhook_secret":"` + testStripeSecret + `"}`
	if _, got := providers(); len(got) != 0 {
		t.Errorf("a currency that cannot be charged: %v", got)
	}
}
