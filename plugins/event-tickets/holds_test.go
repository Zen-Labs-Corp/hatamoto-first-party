package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// holdHost fakes the database for the hold statements: each statement is
// answered by a canned cell, and every host call is recorded.
type holdHost struct {
	hold     string // answer of holdCart
	release  string
	expire   []string // answers of successive expire batches
	retake   string
	claimed  string
	counts   string
	calls    []call
	expireAt []int // call index of each expire statement
	drainAt  []int
	clock    time.Time
}

func newHoldHost(t *testing.T) *holdHost {
	h := &holdHost{hold: `{"order_id":77,"full":[],"changed":[],"coupon_gone":false}`, release: `true`, retake: `true`, claimed: `null`,
		counts: `{"by_email":0,"hour":0,"waiting":0,"pending_by_email":2}`, clock: time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)}
	now = func() time.Time { return h.clock }
	orig := onHoldsExpired
	t.Cleanup(func() { now = time.Now; onHoldsExpired = orig })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		h.calls = append(h.calls, call{fn, sql, ar})
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{}`, out)
		case strings.Contains(sql, "INSERT INTO orders"):
			return cell(h.hold)
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = $2"):
			return cell(h.release)
		case strings.HasPrefix(sql, "WITH o AS (UPDATE orders SET status = 'expired'"):
			h.expireAt = append(h.expireAt, len(h.calls)-1)
			if len(h.expire) == 0 {
				return cell(`{"released":0,"sessions":[]}`)
			}
			v := h.expire[0]
			h.expire = h.expire[1:]
			return cell(v)
		case strings.Contains(sql, "SET status = 'pending', expires_at"):
			return cell(h.retake)
		case strings.Contains(sql, "FROM mail_queue WHERE next_at"):
			h.drainAt = append(h.drainAt, len(h.calls)-1)
			return cell(h.claimed)
		case strings.Contains(sql, "'by_email'"):
			return cell(h.counts)
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *holdHost) dbCalls() []call {
	var out []call
	for _, c := range h.calls {
		if c.fn == sdk.FnDBQuery || c.fn == sdk.FnDBExec {
			out = append(out, c)
		}
	}
	return out
}

func threeTypeCart() holdIn {
	return holdIn{
		EventID: "e1", Buyer: orderIn{Name: "Ana", Email: "ana@example.com", Answers: map[string]string{"diet": "veg"}},
		Q: quote{
			Lines:    []cartLine{{Type: 5, Name: "GA", Qty: 2, Unit: 1000}, {Type: 6, Name: "VIP", Qty: 1, Unit: 5000}, {Type: 8, Name: "Kids", Qty: 1, Unit: 0}},
			Subtotal: 7000, Discount: 700, Fee: 30, Total: 6330, CouponID: 4, Currency: "USD",
		},
		Codes: []holdCode{{5, "c1"}, {5, "c2"}, {6, "c3"}, {8, "c4"}}, Provider: "stripe", PayCode: "paycode",
	}
}

// A multi-type cart is held by one statement that locks the types in id order,
// re-checks capacity per row, counts the coupon and writes the order: the paid
// path never uses the compensating free path.
func TestHoldCartIsOneStatement(t *testing.T) {
	h := newHoldHost(t)
	res, err := holdCart(threeTypeCart())
	if err != nil || res.OrderID == nil || *res.OrderID != 77 || len(res.Full) != 0 || len(res.Changed) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	calls := h.dbCalls()
	if len(calls) != 1 || calls[0].fn != sdk.FnDBQuery {
		t.Fatalf("%d statements, want exactly one db.query: %+v", len(calls), calls)
	}
	sql := calls[0].sql
	for _, want := range []string{"FOR UPDATE", "ORDER BY t.id", "tt.taken + w.qty <= tt.capacity", "UPDATE coupons", "'pending'",
		"INSERT INTO orders", "INSERT INTO attendees", "lk.price_cents = w.unit", "interval '15 minutes'", "lk.on_sale"} {
		if !strings.Contains(sql, want) {
			t.Errorf("the hold statement lacks %q", want)
		}
	}
	for _, bad := range []string{"reserve", "releaseOrder", "'cancelled'"} {
		if strings.Contains(sql, bad) {
			t.Errorf("the hold statement mentions %q", bad)
		}
	}
	for _, c := range h.calls {
		if strings.Contains(c.sql, "DELETE FROM orders") || strings.Contains(c.sql, "reserve") {
			t.Errorf("the paid path must not use the free path: %s", c.sql)
		}
	}
	// money and ids travel as arguments, the coupon as its id
	if got := jsonText(calls[0].args[7:]); got != `[4,7000,700,30,false,6330,"stripe","paycode"]` {
		t.Errorf("amount args %s", got)
	}
	if got := jsonText(calls[0].args[:2]); !strings.Contains(got, `"e1"`) || !strings.Contains(got, `\"unit\":5000`) {
		t.Errorf("lines are sent with the database prices: %s", got)
	}
	if got := jsonText(calls[0].args[2]); got != `"[{\"type\":5,\"code\":\"c1\"},{\"type\":5,\"code\":\"c2\"},{\"type\":6,\"code\":\"c3\"},{\"type\":8,\"code\":\"c4\"}]"` {
		t.Errorf("codes %s", got)
	}
	for _, c := range calls {
		if strings.Contains(c.sql, "ana@example.com") || strings.Contains(c.sql, "paycode") {
			t.Error("input must travel as arguments only")
		}
	}
}

func TestHoldCartWithoutCouponAndRefused(t *testing.T) {
	h := newHoldHost(t)
	in := threeTypeCart()
	in.Q.CouponID = 0
	h.hold = `{"order_id":null,"full":[6],"changed":null,"coupon_gone":true}`
	res, err := holdCart(in)
	if err != nil || res.OrderID != nil || len(res.Full) != 1 || res.Full[0] != 6 || res.Changed == nil || len(res.Changed) != 0 || !res.CouponGone {
		t.Fatalf("%+v %v", res, err)
	}
	if h.dbCalls()[0].args[7] != nil {
		t.Errorf("no coupon is NULL, got %v", h.dbCalls()[0].args[7])
	}
	h.hold = `{"order_id":null,"full":[],"changed":[5]}`
	if res, _ := holdCart(in); len(res.Changed) != 1 || res.Changed[0] != 5 {
		t.Errorf("a changed price is reported: %+v", res)
	}
}

func TestReleaseHold(t *testing.T) {
	h := newHoldHost(t)
	for _, to := range []string{"expired", "cancelled"} {
		done, err := releaseHold(9, to)
		if err != nil || !done {
			t.Fatalf("%s: %v %v", to, done, err)
		}
	}
	calls := h.dbCalls()
	if len(calls) != 2 || calls[1].args[1] != "cancelled" {
		t.Fatalf("one statement per release: %+v", calls)
	}
	for _, want := range []string{"status = 'pending' RETURNING id, coupon_id", "UPDATE attendees SET status = 'expired'", "RETURNING type_id",
		"taken = t.taken - x.n", "FROM a GROUP BY type_id", "UPDATE coupons SET used = used - 1"} {
		if !strings.Contains(calls[0].sql, want) {
			t.Errorf("the release statement lacks %q", want)
		}
	}
	h.release = `false`
	if done, _ := releaseHold(9, "expired"); done {
		t.Error("an order that is not pending is not released")
	}
	n := len(h.calls)
	if _, err := releaseHold(9, "paid"); err == nil || len(h.calls) != n {
		t.Error("only expired and cancelled are accepted, with no host call")
	}
}

func TestExpireHolds(t *testing.T) {
	h := newHoldHost(t)
	h.expire = []string{`{"released":2,"sessions":["cs_1","cs_2"]}`}
	got, err := expireHolds("e1", 50)
	if err != nil || len(got) != 2 || got[0] != "cs_1" {
		t.Fatalf("%v %v", got, err)
	}
	calls := h.dbCalls()
	if len(calls) != 1 || jsonText(calls[0].args) != `["e1",50]` {
		t.Fatalf("%+v", calls)
	}
	for _, want := range []string{"expires_at < now()", "SKIP LOCKED", "LIMIT $2", "UPDATE attendees SET status = 'expired'", "RETURNING type_id",
		"taken = t.taken - x.n", "provider = 'stripe'", "status = 'pending'"} {
		if !strings.Contains(calls[0].sql, want) {
			t.Errorf("the expiry statement lacks %q", want)
		}
	}
	if got, err := expireHolds("e1", 50); err != nil || got == nil || len(got) != 0 {
		t.Errorf("nothing lapsed: %v %v", got, err)
	}
}

func TestRetakeHold(t *testing.T) {
	h := newHoldHost(t)
	done, err := retakeHold(9)
	if err != nil || !done {
		t.Fatalf("%v %v", done, err)
	}
	calls := h.dbCalls()
	if len(calls) != 1 {
		t.Fatalf("one statement, got %d", len(calls))
	}
	for _, want := range []string{"FOR UPDATE", "ORDER BY t.id", "tt.taken + w.qty <= tt.capacity", "status IN ('expired', 'cancelled')",
		"= (SELECT count(*) FROM want)", "UPDATE attendees SET status = 'pending'", "interval '15 minutes'"} {
		if !strings.Contains(calls[0].sql, want) {
			t.Errorf("the retake statement lacks %q", want)
		}
	}
	h.retake = `false`
	if done, _ := retakeHold(9); done {
		t.Error("no seats, no retake")
	}
}

func TestExpireHoldsStepBatchesAndSeam(t *testing.T) {
	h := newHoldHost(t)
	full := `{"released":50,"sessions":["cs_a"]}`
	h.expire = []string{full, full, `{"released":3,"sessions":[]}`, full}
	var seen []string
	onHoldsExpired = func(ids []string) { seen = append(seen, ids...) }
	expireHoldsStep(2 * time.Second)
	if len(h.expireAt) != 3 {
		t.Errorf("%d batches, want 3 (the third was short)", len(h.expireAt))
	}
	if len(seen) != 2 {
		t.Errorf("Stripe sessions handed on: %v", seen)
	}
	// a full batch each time stops at expireBatches
	h = newHoldHost(t)
	h.expire = []string{full, full, full, full, full, full, full}
	expireHoldsStep(2 * time.Second)
	if len(h.expireAt) != expireBatches {
		t.Errorf("%d batches, want %d", len(h.expireAt), expireBatches)
	}
	// a database error ends the step quietly
	h = newHoldHost(t)
	callHost = func(string, any, any) error { return errors.New("pq: boom") }
	expireHoldsStep(2 * time.Second)
}

func TestMailCronExpiresHoldsBeforeSending(t *testing.T) {
	h := newHoldHost(t)
	in := []byte(`{"name":"mail"}`)
	if _, err := guest.Handle(guest.CronExport("mail"), in); err != nil {
		t.Fatal(err)
	}
	if len(h.expireAt) != 1 || len(h.drainAt) != 1 || h.expireAt[0] > h.drainAt[0] {
		t.Errorf("expiry at %v, drain at %v: lapsed holds go first", h.expireAt, h.drainAt)
	}
	if got := h.calls[h.expireAt[0]].args; jsonText(got) != `["",50]` {
		t.Errorf("the cron expires holds of every event: %s", jsonText(got))
	}
}

func TestCountEventCountsOpenHolds(t *testing.T) {
	h := newHoldHost(t)
	c, err := countEvent("e1", "ana@example.com")
	if err != nil || c.PendingByEmail != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	sql := h.dbCalls()[0].sql
	if !strings.Contains(sql, "'pending_by_email'") || !strings.Contains(sql, "o.status = 'pending'") {
		t.Error("open holds of the address are counted")
	}
}

func TestCountEventIgnoresLapsedHoldsInTheHourlyCap(t *testing.T) {
	h := newHoldHost(t)
	if _, err := countEvent("e1", "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	sql := h.dbCalls()[0].sql
	for _, want := range []string{"'open_holds'", "status = 'pending' AND expires_at > now()", "NOT (status IN ('expired', 'cancelled') AND paid_at IS NULL)"} {
		if !strings.Contains(sql, want) {
			t.Errorf("counts lack %q", want)
		}
	}
}
