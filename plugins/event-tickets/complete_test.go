package main

import (
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// shop fakes the host for payment completion with a one-order state machine:
// the guarded statements behave as their SQL says (a transition happens only
// from the right status), so replays, races and late payments can be played.
type shop struct {
	status   string // of the order
	provider string
	ref      string
	payCode  string
	total    int64
	cur      string
	paid     bool // paid_at is set
	problem  string
	refunded int64
	attend   string // status of the attendees
	seats    bool   // retakeHold finds free seats
	events   map[string]string
	answers  map[string][2]string
	key      []byte
	clock    time.Time
	secret   string        // webhook secret
	tick     time.Duration // the clock moves this much with every provider call (to a URL with tickOn)
	tickOn   string
	live     bool   // the hold has not lapsed
	racing   func() // runs just before retakeHold reads the order: another request moves it

	mails    []map[string]any
	queued   []string // kind of queued mails
	fetches  []fetched
	writes   []call // every statement that changes rows
	dbCalls  int
	released int
}

const (
	shopOrder = 42
	shopCode  = "0123456789abcdef0123456789abcdef"
)

func newShop(t *testing.T) *shop {
	t.Helper()
	w := &shop{
		status: "pending", provider: "stripe", ref: "cs_test_1", payCode: "00112233445566778899aabbccddeeff", total: 2030, cur: "USD", attend: "pending", seats: true, live: true,
		events: map[string]string{}, answers: map[string][2]string{}, key: []byte("0123456789abcdef0123456789abcdef"),
		clock: time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), secret: testStripeSecret,
	}
	w.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"200", `{"id":"re_1"}`}
	w.answers["POST https://api.stripe.com/v1/checkout/sessions/cs_test_1/expire"] = [2]string{"200", `{}`}
	now = func() time.Time { return w.clock }
	origHook := onHoldsExpired
	t.Cleanup(func() { now = time.Now; onHoldsExpired = origHook })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		write := func() { w.writes = append(w.writes, call{fn, sql, ar}) }
		if fn == sdk.FnDBQuery || fn == sdk.FnDBExec {
			w.dbCalls++
		}
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{"stripe_secret_key":"`+testStripeKey+`","stripe_webhook_secret":"`+w.secret+`","paypal_client_id":"`+testPayPalClient+
				`","paypal_secret":"`+testPayPalSecret+`","paypal_webhook_id":"WEBHOOKID123","paypal_sandbox":true}`, out)
		case fn == sdk.FnEntitiesGet:
			return jsonInto(gigEntity, out)
		case fn == sdk.FnMailSend:
			w.mails = append(w.mails, a)
			return nil
		case fn == sdk.FnHTTPFetch:
			f := fetched{method: a["method"].(string), url: a["url"].(string), headers: a["headers"].(map[string]string)}
			f.body, _ = a["body"].(string)
			w.fetches = append(w.fetches, f)
			if strings.Contains(f.url, w.tickOn) {
				w.clock = w.clock.Add(w.tick)
			}
			ans, ok := w.answers[f.method+" "+f.url]
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
			return cell(`"` + hex.EncodeToString(w.key) + `"`)
		case strings.Contains(sql, "FROM payment_events WHERE"):
			_, seen := w.events[ar[1].(string)]
			return cell(jsonText(seen))
		case strings.HasPrefix(sql, "INSERT INTO payment_events"):
			write()
			if _, seen := w.events[ar[1].(string)]; !seen {
				w.events[ar[1].(string)] = ar[3].(string)
			}
			return nil
		case strings.Contains(sql, "WHERE provider = $1 AND provider_ref = $2 AND pay_code = $3"):
			if ar[0] == w.provider && ar[1] == w.ref && ar[2] == w.payCode {
				return cell(`{"id":42,"status":"` + w.status + `"}`)
			}
			return cell(`null`)
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = 'paid', paid_at = now(), payment_ref"):
			write()
			ok := w.status == "pending" && ar[4] == w.provider && ar[2] == w.total && ar[3] == w.cur
			if ok {
				w.status, w.paid, w.attend = "paid", true, "active"
			}
			return cell(jsonText(ok))
		case strings.Contains(sql, "'live', coalesce(expires_at"):
			return cell(`{"status":"` + w.status + `","provider":"` + w.provider + `","provider_ref":"` + w.ref + `","pay_code":"` + w.payCode + `","total_cents":` +
				itoa(w.total) + `,"currency":"` + w.cur + `","paid":` + jsonText(w.paid) + `,"live":` + jsonText(w.live) + `}`)
		case strings.Contains(sql, "'paid', paid_at IS NOT NULL"):
			return cell(`{"status":"` + w.status + `","total_cents":` + itoa(w.total) + `,"currency":"` + w.cur + `","paid":` + jsonText(w.paid) + `}`)
		case strings.Contains(sql, "total_cents = 0 RETURNING"):
			write()
			ok := w.status == "pending" && w.total == 0
			if ok {
				w.status, w.paid, w.attend = "paid", true, "active"
			}
			return cell(jsonText(ok))
		case strings.HasPrefix(sql, "UPDATE orders SET problem = 'amount_mismatch'"):
			write()
			w.problem = "amount_mismatch"
			return nil
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = 'expired', problem = 'refund_failed'"):
			write()
			if w.status == "refunded" && !w.paid {
				w.status, w.refunded, w.attend, w.problem = "expired", 0, "expired", "refund_failed"
			}
			return nil
		case strings.Contains(sql, "SET status = 'pending', expires_at"):
			write()
			if w.racing != nil {
				w.racing()
				w.racing = nil
			}
			ok := w.seats && (w.status == "expired" || w.status == "cancelled") && !w.paid
			if ok {
				w.status, w.attend, w.live = "pending", "pending", true
			}
			return cell(jsonText(ok))
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = 'refunded'"):
			write()
			ok := (w.status == "expired" || w.status == "cancelled") && !w.paid
			if ok {
				w.status, w.refunded, w.attend, w.problem = "refunded", w.total, "refunded", ""
			}
			return cell(jsonText(ok))
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = $2"):
			write()
			ok := w.status == "pending"
			if ok {
				w.status, w.attend = ar[1].(string), "expired"
				w.released++
			}
			return cell(jsonText(ok))
		case strings.HasPrefix(sql, "INSERT INTO mail_queue"):
			write()
			w.queued = append(w.queued, ar[0].(string))
			return nil
		case strings.HasPrefix(sql, "UPDATE orders SET mailed_at"):
			write()
			return nil
		case strings.Contains(sql, "FROM orders o WHERE o.id"):
			return cell(w.orderMail())
		case strings.Contains(sql, "FROM attendees a JOIN ticket_types t ON t.id = a.type_id WHERE a.order_id = $1 AND a.status = 'active'"):
			if w.attend != "active" {
				return cell(`[]`)
			}
			return cell(`[{"code":"` + shopCode + `","type_name":"GA"}]`)
		case strings.Contains(sql, "WHERE provider = 'paypal' AND provider_ref = $1"):
			if w.provider == "paypal" && ar[0] == w.ref {
				return cell(`{"id":42,"status":"` + w.status + `"}`)
			}
			return cell(`null`)
		case strings.Contains(sql, "FROM orders WHERE pay_code = $1"):
			if ar[0] != w.payCode {
				return cell(`null`)
			}
			return cell(`{"id":42,"status":"` + w.status + `","provider":"` + w.provider + `","provider_ref":"` + w.ref + `","event_id":"e1","pay_code":"` + w.payCode + `"}`)
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return w
}

// orderMail is the order as composeConfirm and composeRefund read it.
func (w *shop) orderMail() string {
	tickets, refundedFor := `[]`, `[]`
	if w.attend == "active" {
		tickets = `[{"code":"` + shopCode + `","type_name":"GA"}]`
	}
	if w.attend == "refunded" {
		refundedFor = `["GA"]`
	}
	return `{"event_id":"e1","name":"Ana","email":"ana@example.com","anonymised":false,"tickets":` + tickets + `,"status":"` + w.status + `",
"lines":[{"type":5,"name":"GA","qty":2,"unit":1000}],"subtotal_cents":2000,"discount_cents":0,"fee_cents":30,"total_cents":` + itoa(w.total) + `,
"refunded_cents":` + itoa(w.refunded) + `,"currency":"` + w.cur + `","fee_absorbed":false,"refunded_for":` + refundedFor + `}`
}

func (w *shop) wrote(prefix string) int {
	n := 0
	for _, c := range w.writes {
		if strings.HasPrefix(c.sql, prefix) {
			n++
		}
	}
	return n
}

func (w *shop) refundCalls() []fetched {
	var out []fetched
	for _, f := range w.fetches {
		if strings.HasSuffix(f.url, "/v1/refunds") {
			out = append(out, f)
		}
	}
	return out
}

// leaks reports every secret that shows up in what left the plugin: mails,
// queued mail, written arguments and the given responses. Requests to the
// provider carry the key in a header by design and are not looked at here.
func (w *shop) leaks(responses ...string) []string {
	secrets := []string{testStripeKey, testStripeSecret, testPayPalSecret, testPayPalToken, "whsec_", "sk_test_", "t=" + "1", "v1="}
	var all strings.Builder
	for _, r := range responses {
		all.WriteString(r + "\n")
	}
	for _, m := range w.mails {
		b, _ := json.Marshal(m)
		all.Write(b)
	}
	for _, c := range w.writes {
		b, _ := json.Marshal(c.args)
		all.Write(b)
	}
	for _, q := range w.queued {
		all.WriteString(q)
	}
	var found []string
	for _, s := range secrets {
		if strings.Contains(all.String(), s) {
			found = append(found, s)
		}
	}
	return found
}

func TestCompletePaymentPaysOnceAndMailsOnce(t *testing.T) {
	w := newShop(t)
	out, err := completePayment(shopOrder, "stripe", "pi_1", 2030, "usd")
	if err != nil || out != outcomePaid || w.status != "paid" || w.attend != "active" {
		t.Fatalf("%q %v, order %q attendees %q", out, err, w.status, w.attend)
	}
	if len(w.mails) != 1 || len(w.queued) != 0 {
		t.Fatalf("%d mails, %d queued", len(w.mails), len(w.queued))
	}
	m := w.mails[0]
	html, text := m["html"].(string), m["text"].(string)
	if m["subject"] != "Your tickets for Gig" || !strings.Contains(html, "/qr.png") || !strings.Contains(html, shopCode[:8]) {
		t.Errorf("mail %v", m)
	}
	for _, body := range []string{html, text} {
		for _, want := range []string{"GA x 2", "20.00 USD", "0.30 USD", "20.30 USD"} {
			if !strings.Contains(body, want) {
				t.Errorf("receipt lacks %q: %s", want, body)
			}
		}
	}
	if w.wrote("UPDATE orders SET mailed_at") != 1 {
		t.Error("mailed_at not set")
	}

	// the same payment again: nothing changes, nothing is sent
	before := len(w.writes)
	out, err = completePayment(shopOrder, "stripe", "pi_1", 2030, "USD")
	if err != nil || out != outcomeDuplicate || len(w.mails) != 1 || len(w.fetches) != 0 {
		t.Fatalf("replay: %q %v, %d mails", out, err, len(w.mails))
	}
	for _, c := range w.writes[before:] {
		if !strings.HasPrefix(c.sql, "WITH o AS (UPDATE orders SET status = 'paid'") {
			t.Errorf("replay wrote %q", c.sql)
		}
	}
}

func TestCompletePaymentAmountAndProviderMustMatch(t *testing.T) {
	cases := map[string]struct {
		provider string
		amount   int64
		cur      string
	}{
		"less":           {"stripe", 2029, "USD"},
		"more":           {"stripe", 2031, "USD"},
		"other currency": {"stripe", 2030, "EUR"},
		"other provider": {"paypal", 2030, "USD"},
	}
	for name, c := range cases {
		w := newShop(t)
		out, err := completePayment(shopOrder, c.provider, "pi_1", c.amount, c.cur)
		if err != nil || out != outcomeMismatch || w.status != "pending" || w.attend != "pending" || w.problem != "amount_mismatch" {
			t.Errorf("%s: %q %v, order %q attendees %q problem %q", name, out, err, w.status, w.attend, w.problem)
		}
		if len(w.mails) != 0 || len(w.queued) != 0 {
			t.Errorf("%s: a mail went out", name)
		}
	}
}

func TestCompletePaymentOnAnExpiredHoldTakesTheSeatsBack(t *testing.T) {
	for _, status := range []string{"expired", "cancelled"} {
		w := newShop(t)
		w.status, w.attend = status, "expired"
		out, err := completePayment(shopOrder, "stripe", "pi_1", 2030, "USD")
		if err != nil || out != outcomePaid || w.status != "paid" || w.attend != "active" || len(w.mails) != 1 || len(w.fetches) != 0 {
			t.Fatalf("%s: %q %v, order %q, %d mails, %d provider calls", status, out, err, w.status, len(w.mails), len(w.fetches))
		}
	}
}

func TestCompletePaymentWithNoSeatsLeftPaysBack(t *testing.T) {
	w := newShop(t)
	w.status, w.attend, w.seats = "expired", "expired", false
	out, err := completePayment(shopOrder, "stripe", "pi_1", 2030, "USD")
	if err != nil || out != outcomeRefunded || w.status != "refunded" || w.refunded != 2030 || w.attend != "refunded" {
		t.Fatalf("%q %v, order %q refunded %d", out, err, w.status, w.refunded)
	}
	refunds := w.refundCalls()
	if len(refunds) != 1 {
		t.Fatalf("%d refund calls", len(refunds))
	}
	form, _ := url.ParseQuery(refunds[0].body)
	if refunds[0].headers["Idempotency-Key"] != "et-42-late-refund" || form.Get("payment_intent") != "pi_1" || form.Get("amount") != "2030" {
		t.Errorf("refund %+v %v", refunds[0].headers, form)
	}
	if len(w.mails) != 1 || w.mails[0]["subject"] != "Your refund for Gig" || !strings.Contains(w.mails[0]["text"].(string), "20.30 USD") ||
		!strings.Contains(w.mails[0]["text"].(string), "GA") {
		t.Errorf("mails %v", w.mails)
	}
	if strings.Contains(w.mails[0]["html"].(string), "/qr.png") {
		t.Error("a refund mail has a ticket QR")
	}
	// the refunded order is not taken again by a replay
	if out, _ = completePayment(shopOrder, "stripe", "pi_1", 2030, "USD"); out != outcomeDuplicate || len(w.mails) != 1 || len(w.refundCalls()) != 1 {
		t.Errorf("replay: %q, %d mails, %d refunds", out, len(w.mails), len(w.refundCalls()))
	}
}

func TestCompletePaymentRefundFailureIsRetried(t *testing.T) {
	w := newShop(t)
	w.status, w.attend, w.seats = "expired", "expired", false
	w.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"500", `{"error":{"message":"` + testStripeKey + `"}}`}
	out, err := completePayment(shopOrder, "stripe", "pi_1", 2030, "USD")
	if err == nil || out != "" || err.Error() != errRefundFailed.Error() || strings.Contains(err.Error(), "sk_") {
		t.Fatalf("%q %v", out, err)
	}
	if w.status != "expired" || w.problem != "refund_failed" || len(w.mails) != 0 || len(w.queued) != 0 {
		t.Fatalf("order %q problem %q, %d mails", w.status, w.problem, len(w.mails))
	}
	// Stripe retries the webhook; the refund now works and uses the same key
	w.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"200", `{"id":"re_1"}`}
	if out, err = completePayment(shopOrder, "stripe", "pi_1", 2030, "USD"); err != nil || out != outcomeRefunded || w.status != "refunded" || w.problem != "" {
		t.Fatalf("retry: %q %v, order %q problem %q", out, err, w.status, w.problem)
	}
	refunds := w.refundCalls()
	if len(refunds) != 2 || refunds[0].headers["Idempotency-Key"] != refunds[1].headers["Idempotency-Key"] {
		t.Errorf("refund calls %+v", refunds)
	}
	if len(w.mails) != 1 {
		t.Errorf("%d mails", len(w.mails))
	}
}

func TestCompletePaymentOnAPaidOrderOrNoOrder(t *testing.T) {
	w := newShop(t)
	w.status, w.paid = "cancelled", true // an owner cancelled a paid order
	if out, err := completePayment(shopOrder, "stripe", "pi_1", 2030, "USD"); err != nil || out != outcomeDuplicate || len(w.mails) != 0 || w.released != 0 {
		t.Errorf("cancelled after paid: %q %v", out, err)
	}
	w = newShop(t)
	w.status = "confirmed" // a free order
	if out, _ := completePayment(shopOrder, "stripe", "pi_1", 0, "USD"); out != outcomeDuplicate || len(w.mails) != 0 {
		t.Errorf("free order: %q", out)
	}
}

func TestCompleteFree(t *testing.T) {
	w := newShop(t)
	w.total = 0
	if done, err := completeFree(shopOrder); !done || err != nil || w.status != "paid" || w.attend != "active" {
		t.Fatalf("%v %v, order %q", done, err, w.status)
	}
	if done, _ := completeFree(shopOrder); done {
		t.Fatal("the second call changed the order again")
	}
	w = newShop(t) // an order that costs money is not completed without payment
	if done, _ := completeFree(shopOrder); done || w.status != "pending" {
		t.Fatalf("a priced order was completed: %v %q", done, w.status)
	}
}

func TestPayHooksCloseStripeSessionsWithinABudget(t *testing.T) {
	w := newShop(t)
	w.tick = time.Second
	onHoldsExpired([]string{"cs_test_1", "cs_test_2", "cs_test_3", "cs_test_4"})
	if len(w.fetches) != 1 || !strings.HasSuffix(w.fetches[0].url, "/v1/checkout/sessions/cs_test_1/expire") {
		t.Fatalf("%+v", w.fetches)
	}
}

// A late payment that races another request for the same order has one winner:
// the loser refunds nothing and does not change the seats.
func TestLatePaymentRaceHasOneWinner(t *testing.T) {
	cases := []struct {
		name    string
		racing  func(w *shop)
		out     string
		status  string
		mails   int
		refunds int
	}{
		{"other request already paid it", func(w *shop) { w.status, w.paid, w.attend = "paid", true, "active" }, outcomeDuplicate, "paid", 0, 0},
		{"other request holds it again", func(w *shop) { w.status, w.attend = "pending", "pending" }, outcomePaid, "paid", 1, 0},
		{"other request refunded it", func(w *shop) { w.status, w.refunded, w.attend = "refunded", w.total, "refunded" }, outcomeDuplicate, "refunded", 0, 0},
	}
	for _, c := range cases {
		w := newShop(t)
		w.status, w.attend = "expired", "expired"
		w.racing = func() { c.racing(w) }
		out, err := completePayment(shopOrder, "stripe", "pi_1", 2030, "USD")
		if err != nil || out != c.out || w.status != c.status || len(w.mails) != c.mails || len(w.refundCalls()) != c.refunds {
			t.Errorf("%s: %q %v, order %q, %d mails, %d refunds", c.name, out, err, w.status, len(w.mails), len(w.refundCalls()))
		}
	}
}
