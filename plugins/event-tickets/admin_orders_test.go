package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

var owner = &sdk.HTTPUser{ID: "u0", Role: "owner"}

// orderHost fakes the host for the order routes: one paid order, provider
// answers, and the guarded statements.
type orderHost struct {
	order    string // readRefundable answer
	list     string
	refunded string // answer of the refund statement
	claim    string // answer of the refund claim
	cancel   string // answer of the order cancel statement
	hold     string // answer of releaseHold
	answers  map[string][2]string
	fetches  []fetched
	calls    []call
	mails    int
}

func newOrderHost(t *testing.T) *orderHost {
	t.Helper()
	h := &orderHost{
		order:    `{"status":"paid","provider":"stripe","pay_code":"paycode","payment_ref":"pi_1","total_cents":2030,"refunded_cents":0,"currency":"USD"}`,
		list:     `[]`,
		refunded: `{"status":"refunded","refunded_cents":2030}`,
		claim:    `true`,
		cancel:   `true`, hold: `false`,
		answers: map[string][2]string{
			"POST https://api.stripe.com/v1/refunds":                   {"200", `{"id":"re_1"}`},
			"POST " + sandboxAPI + "/v1/oauth2/token":                  {"200", `{"access_token":"` + testPayPalToken + `","expires_in":32400}`},
			"POST " + sandboxAPI + "/v2/payments/captures/CAP1/refund": {"201", `{"id":"RF1","status":"COMPLETED"}`},
		},
	}
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		if fn == sdk.FnDBQuery || fn == sdk.FnDBExec {
			h.calls = append(h.calls, call{fn, sql, ar})
		}
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{"stripe_secret_key":"`+testStripeKey+`","stripe_webhook_secret":"`+testStripeSecret+`","paypal_client_id":"`+testPayPalClient+
				`","paypal_secret":"`+testPayPalSecret+`","paypal_sandbox":true}`, out)
		case fn == sdk.FnHTTPFetch:
			f := fetched{method: a["method"].(string), url: a["url"].(string), headers: a["headers"].(map[string]string)}
			f.body, _ = a["body"].(string)
			h.fetches = append(h.fetches, f)
			ans, ok := h.answers[f.method+" "+f.url]
			if !ok {
				ans = [2]string{"500", `{}`}
			}
			return jsonInto(`{"status":`+ans[0]+`,"body":`+jsonText(ans[1])+`}`, out)
		case strings.HasPrefix(sql, "INSERT INTO meta"):
			return nil
		case strings.Contains(sql, "FROM meta WHERE key"):
			return cell(`null`)
		case strings.Contains(sql, "FROM orders o LEFT JOIN events"):
			return cell(h.list)
		case strings.Contains(sql, "'payment_ref', coalesce(payment_ref"):
			return cell(h.order)
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET refund_claimed_at = now()"):
			return cell(h.claim)
		case strings.HasPrefix(sql, "UPDATE orders SET refund_claimed_at = NULL"):
			return nil
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET refunded_cents"):
			return cell(h.refunded)
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = 'cancelled'"):
			return cell(h.cancel)
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = $2"):
			return cell(h.hold)
		case strings.Contains(sql, "FROM orders o WHERE o.id"):
			h.mails++
			return cell(`null`)
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *orderHost) stmt(prefix string) *call {
	for i, c := range h.calls {
		if strings.HasPrefix(c.sql, prefix) {
			return &h.calls[i]
		}
	}
	return nil
}

const refundPattern = "/admin/orders/{id}/refund"

func TestRefundFullStripe(t *testing.T) {
	h := newOrderHost(t)
	code, body := post(t, refundPattern, "42", owner, `{"amount_cents":2030}`)
	if code != 200 || body["status"] != "refunded" || body["refunded_cents"] != float64(2030) {
		t.Fatalf("%d %v", code, body)
	}
	if len(h.fetches) != 1 {
		t.Fatalf("%d provider calls", len(h.fetches))
	}
	f := h.fetches[0]
	if f.url != "https://api.stripe.com/v1/refunds" || !strings.Contains(f.body, "payment_intent=pi_1") || !strings.Contains(f.body, "amount=2030") ||
		f.headers["Idempotency-Key"] != "et-paycode-refund-0-2030" {
		t.Errorf("refund call %+v", f)
	}
	st := h.stmt("WITH o AS (UPDATE orders SET refunded_cents")
	if st == nil || !same(st.args[0], 42) || !same(st.args[1], 2030) || !same(st.args[2], 0) || st.args[3] != true {
		t.Fatalf("statement %+v", st)
	}
	for _, part := range []string{"refunded_cents = $3", "status = 'paid'", "UPDATE attendees SET status = 'refunded'", "taken - x.n"} {
		if !strings.Contains(st.sql, part) {
			t.Errorf("statement lacks %q", part)
		}
	}
	if h.mails != 1 {
		t.Errorf("%d refund mails", h.mails)
	}
}

func TestRefundPartialPayPal(t *testing.T) {
	h := newOrderHost(t)
	h.order = `{"status":"paid","provider":"paypal","pay_code":"paycode","payment_ref":"CAP1","total_cents":2030,"refunded_cents":500,"currency":"USD"}`
	h.refunded = `{"status":"paid","refunded_cents":1000}`
	code, body := post(t, refundPattern, "42", owner, `{"amount_cents":500,"attendees":[9,10]}`)
	if code != 200 || body["status"] != "paid" || body["refunded_cents"] != float64(1000) {
		t.Fatalf("%d %v", code, body)
	}
	var refund *fetched
	for i, f := range h.fetches {
		if strings.HasSuffix(f.url, "/captures/CAP1/refund") {
			refund = &h.fetches[i]
		}
	}
	if refund == nil || !strings.Contains(refund.body, `"value":"5.00"`) || refund.headers["PayPal-Request-Id"] != "et-paycode-refund-500-500" {
		t.Fatalf("refund call %+v", refund)
	}
	st := h.stmt("WITH o AS (UPDATE orders SET refunded_cents")
	if st == nil || !same(st.args[2], 500) || st.args[3] != false || !same(st.args[4], "[9,10]") {
		t.Fatalf("statement %+v", st)
	}
}

func TestRefundRefusals(t *testing.T) {
	cases := []struct {
		name  string
		user  *sdk.HTTPUser
		id    string
		order string
		body  string
		code  int
	}{
		{"editor", editor, "42", "", `{"amount_cents":100}`, 403},
		{"no user", nil, "42", "", `{"amount_cents":100}`, 403},
		{"bad id", owner, "x", "", `{"amount_cents":100}`, 400},
		{"unknown key", owner, "42", "", `{"amount_cents":100,"total":1}`, 400},
		{"zero", owner, "42", "", `{"amount_cents":0}`, 400},
		{"too much", owner, "42", "", `{"amount_cents":2031}`, 400},
		{"more than left", owner, "42", `{"status":"paid","provider":"stripe","pay_code":"paycode","payment_ref":"pi_1","total_cents":2030,"refunded_cents":2000,"currency":"USD"}`, `{"amount_cents":31}`, 400},
		{"free order", owner, "42", `{"status":"confirmed","provider":"","pay_code":"paycode","payment_ref":"","total_cents":0,"refunded_cents":0,"currency":""}`, `{"amount_cents":1}`, 409},
		{"no provider", owner, "42", `{"status":"paid","provider":"none","pay_code":"paycode","payment_ref":"","total_cents":0,"refunded_cents":0,"currency":"USD"}`, `{"amount_cents":1}`, 409},
		{"pending", owner, "42", `{"status":"pending","provider":"stripe","pay_code":"paycode","payment_ref":"","total_cents":2030,"refunded_cents":0,"currency":"USD"}`, `{"amount_cents":1}`, 409},
		{"gone", owner, "42", `null`, `{"amount_cents":1}`, 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newOrderHost(t)
			if c.order != "" {
				h.order = c.order
			}
			code, _ := post(t, refundPattern, c.id, c.user, c.body)
			if code != c.code {
				t.Errorf("status %d, want %d", code, c.code)
			}
			if len(h.fetches) != 0 || h.stmt("WITH o AS (UPDATE orders SET refunded_cents") != nil {
				t.Error("something was refunded")
			}
		})
	}
}

func TestRefundProviderFailureWritesNothing(t *testing.T) {
	h := newOrderHost(t)
	delete(h.answers, "POST https://api.stripe.com/v1/refunds")
	code, body := post(t, refundPattern, "42", owner, `{"amount_cents":100}`)
	if code != 503 || body["ok"] != false {
		t.Fatalf("%d %v", code, body)
	}
	if h.stmt("WITH o AS (UPDATE orders SET refunded_cents") != nil || h.mails != 0 {
		t.Error("the order changed although the provider refused")
	}
	if strings.Contains(jsonText(body), "sk_test") {
		t.Error("secret in the answer")
	}
}

func TestRefundRetryUsesTheSameKey(t *testing.T) {
	h := newOrderHost(t)
	post(t, refundPattern, "42", owner, `{"amount_cents":100}`)
	post(t, refundPattern, "42", owner, `{"amount_cents":100}`)
	if len(h.fetches) != 2 || h.fetches[0].headers["Idempotency-Key"] != h.fetches[1].headers["Idempotency-Key"] {
		t.Errorf("keys %+v", h.fetches)
	}
}

func TestRefundWhenTheOrderChangedMeanwhile(t *testing.T) {
	h := newOrderHost(t)
	h.refunded = `null`
	code, _ := post(t, refundPattern, "42", owner, `{"amount_cents":100}`)
	if code != 409 || h.mails != 0 {
		t.Errorf("status %d, %d mails", code, h.mails)
	}
}

func TestCancelOrder(t *testing.T) {
	const pattern = "/admin/orders/{id}/cancel"
	h := newOrderHost(t)
	if code, _ := post(t, pattern, "42", editor, `{}`); code != 403 {
		t.Errorf("editor: %d", code)
	}
	if code, _ := post(t, pattern, "x", owner, `{}`); code != 400 {
		t.Errorf("bad id: %d", code)
	}
	// a paid order: tickets cancelled, no provider call
	if code, body := post(t, pattern, "42", owner, `{}`); code != 200 || body["ok"] != true || len(h.fetches) != 0 {
		t.Fatalf("paid: %d %v", code, body)
	}
	st := h.stmt("WITH o AS (UPDATE orders SET status = 'cancelled'")
	if st == nil || !strings.Contains(st.sql, "taken - x.n") || !strings.Contains(st.sql, "'cancelled', cancelled_at") {
		t.Fatalf("statement %+v", st)
	}
	// a pending order is released instead
	h = newOrderHost(t)
	h.hold = `true`
	if code, _ := post(t, pattern, "42", owner, `{}`); code != 200 || h.stmt("WITH o AS (UPDATE orders SET status = 'cancelled'") != nil {
		t.Errorf("pending: %d", code)
	}
	// nothing to cancel
	h = newOrderHost(t)
	h.cancel = `false`
	if code, _ := post(t, pattern, "42", owner, `{}`); code != 409 {
		t.Errorf("done already: %d", code)
	}
}

func TestOrdersList(t *testing.T) {
	h := newOrderHost(t)
	list := func(q map[string][]string) (int, map[string]any) {
		return request(t, sdk.HTTPRequest{Method: "GET", Pattern: "/admin/orders", Query: q, User: editor})
	}
	h.list = `[{"id":9,"status":"paid"}]`
	code, body := list(map[string][]string{"status": {"paid"}, "event": {"e1"}, "q": {"50%_off"}, "cursor": {"20"}})
	if code != 200 || len(body["orders"].([]any)) != 1 || body["next_cursor"] != "" {
		t.Fatalf("%d %v", code, body)
	}
	st := h.stmt("SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id DESC)")
	if st == nil || !strings.Contains(st.sql, "'type_name', t.name || coalesce(', ' || a.seat_label, '')") {
		t.Errorf("an order's tickets print their seats: %+v", st)
	}
	if st == nil || !same(st.args[0], "paid") || !same(st.args[1], "e1") || !same(st.args[2], `%50\%\_off%`) || !same(st.args[3], 20) || !same(st.args[4], 101) {
		t.Fatalf("args %+v", st)
	}
	// a full page offers a cursor
	rows := make([]string, 101)
	for i := range rows {
		rows[i] = `{"id":` + itoa(int64(500-i)) + `}`
	}
	h.list = "[" + strings.Join(rows, ",") + "]"
	if _, body := list(nil); len(body["orders"].([]any)) != 100 || body["next_cursor"] != "401" {
		t.Errorf("paging %v", body["next_cursor"])
	}
	for _, q := range []map[string][]string{{"status": {"bogus"}}, {"event": {"a b"}}, {"cursor": {"-1"}}, {"q": {strings.Repeat("x", 101)}}} {
		if code, _ := list(q); code != 400 {
			t.Errorf("%v: %d", q, code)
		}
	}
	if code, _ := request(t, sdk.HTTPRequest{Method: "GET", Pattern: "/admin/orders"}); code != 403 {
		t.Errorf("no user: %d", code)
	}
}

// Two refunds of one order must not both reach the provider: the claim comes
// first, and a lost claim never calls the provider.
func TestRefundIsClaimedBeforeTheProviderIsAsked(t *testing.T) {
	h := newOrderHost(t)
	h.claim = `false`
	if code, _ := post(t, refundPattern, "42", owner, `{"amount_cents":100}`); code != 409 || len(h.fetches) != 0 || h.stmt("WITH o AS (UPDATE orders SET refunded_cents") != nil {
		t.Fatalf("lost claim: %d, %d provider calls", code, len(h.fetches))
	}

	h = newOrderHost(t)
	if code, _ := post(t, refundPattern, "42", owner, `{"amount_cents":100}`); code != 200 {
		t.Fatalf("%d", code)
	}
	claim, pay := -1, -1
	for i, c := range h.calls {
		if strings.HasPrefix(c.sql, "WITH o AS (UPDATE orders SET refund_claimed_at = now()") {
			claim = i
		}
		if strings.HasPrefix(c.sql, "WITH o AS (UPDATE orders SET refunded_cents") {
			pay = i
		}
	}
	if claim < 0 || pay < claim || !strings.Contains(h.calls[pay].sql, "refund_claimed_at = NULL") {
		t.Errorf("claim at %d, record at %d", claim, pay)
	}
}

func TestRefundClaimIsReleasedWhenTheProviderFails(t *testing.T) {
	h := newOrderHost(t)
	h.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"500", `{}`}
	if code, _ := post(t, refundPattern, "42", owner, `{"amount_cents":100}`); code != 503 {
		t.Fatalf("%d", code)
	}
	if h.stmt("UPDATE orders SET refund_claimed_at = NULL") == nil {
		t.Error("the claim stays after a failed refund")
	}
}

// Cancelling a paid order moves no money: it must stay refundable, and only a
// cancelled order that was paid (a never-paid one has nothing to give back).
func TestRefundOfACancelledPaidOrder(t *testing.T) {
	const cancelled = `{"status":"cancelled","provider":"stripe","pay_code":"paycode","payment_ref":"pi_1","total_cents":2030,"refunded_cents":0,"currency":"USD","paid":%t}`
	for _, c := range []struct {
		paid bool
		code int
	}{{true, 200}, {false, 409}} {
		h := newOrderHost(t)
		h.order = strings.Replace(cancelled, "%t", jsonText(c.paid), 1)
		if code, _ := post(t, refundPattern, "42", owner, `{"amount_cents":2030}`); code != c.code || (c.code == 409) != (len(h.fetches) == 0) {
			t.Errorf("paid %v: %d, %d provider calls", c.paid, code, len(h.fetches))
		}
		for _, prefix := range []string{"WITH o AS (UPDATE orders SET refund_claimed_at", "WITH o AS (UPDATE orders SET refunded_cents"} {
			if st := h.stmt(prefix); c.paid && (st == nil || !strings.Contains(st.sql, "status = 'cancelled' AND paid_at IS NOT NULL")) {
				t.Errorf("%s: statement lets a cancelled paid order through? %+v", prefix, st)
			}
		}
	}
}
