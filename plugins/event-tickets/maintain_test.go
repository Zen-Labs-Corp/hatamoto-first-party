package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// maintainHost fakes the host for the hourly job: it answers each step's
// statement from its fields, advances a clock on every call and records them.
type maintainHost struct {
	settings string
	expired  string // types the expiry statement frees, as a jsonb cell
	expiries int    // expiry batches that still find something
	closed   string // waitlist entries the closed-event statement ends
	stale    string // event ids with an old snapshot
	entities map[string]string
	orders   []int64 // rows_affected of each orders batch in turn
	other    int64   // rows_affected of the other retention deletes
	dbErr    error
	tick     time.Duration // the clock moves this much on every call
	clock    time.Time
	calls    []call
	freed    []int64
}

func newMaintainHost(t *testing.T) *maintainHost {
	h := &maintainHost{settings: `{}`, expired: `[]`, closed: `0`, stale: `[]`, entities: map[string]string{},
		clock: time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)}
	now = func() time.Time { return h.clock }
	origFreed := onSeatsFreed
	onSeatsFreed = func(id int64) { h.freed = append(h.freed, id) }
	t.Cleanup(func() { now = time.Now; onSeatsFreed = origFreed })
	callHost = func(fn string, args, out any) error {
		h.clock = h.clock.Add(h.tick)
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		h.calls = append(h.calls, call{fn, sql, ar})
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		rows := func(n int64) error { return jsonInto(`{"rows_affected":`+jsonText(n)+`}`, out) }
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(h.settings, out)
		case fn == sdk.FnEntitiesGet:
			if h.dbErr != nil {
				return h.dbErr
			}
			e, ok := h.entities[a["id"].(string)]
			if !ok {
				return &sdk.HostError{Code: sdk.ErrNotFound}
			}
			return jsonInto(e, out)
		case h.dbErr != nil:
			return h.dbErr
		case strings.HasPrefix(sql, "WITH e AS (UPDATE waitlist SET status = 'expired'"):
			if h.expiries == 0 {
				return cell(`[]`)
			}
			h.expiries--
			return cell(h.expired)
		case strings.HasPrefix(sql, "WITH c AS (UPDATE waitlist SET status = 'expired'"):
			return cell(h.closed)
		case strings.Contains(sql, "SELECT DISTINCT event_id FROM ticket_types"):
			return cell(h.stale)
		case strings.HasPrefix(sql, "UPDATE orders SET name"):
			n := int64(0)
			if len(h.orders) > 0 {
				n, h.orders = h.orders[0], h.orders[1:]
			}
			return rows(n)
		case strings.HasPrefix(sql, "DELETE FROM"):
			return rows(h.other)
		case strings.HasPrefix(sql, "UPDATE "), strings.HasPrefix(sql, "INSERT INTO events"):
			return nil
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *maintainHost) count(prefix string) int {
	n := 0
	for _, c := range h.calls {
		if strings.HasPrefix(c.sql, prefix) {
			n++
		}
	}
	return n
}

func (h *maintainHost) order(prefix string) int {
	for i, c := range h.calls {
		if strings.HasPrefix(c.sql, prefix) {
			return i
		}
	}
	return -1
}

func (h *maintainHost) first(prefix string) *call {
	for i, c := range h.calls {
		if strings.HasPrefix(c.sql, prefix) {
			return &h.calls[i]
		}
	}
	return nil
}

func TestMaintainExpiryPromotesEachType(t *testing.T) {
	h := newMaintainHost(t)
	h.expired, h.expiries = `[5,6]`, 1
	maintain(maintainBudget)
	if len(h.freed) != 2 || h.freed[0] != 5 || h.freed[1] != 6 {
		t.Errorf("seats freed for %v, want 5 and 6", h.freed)
	}
	c := h.first("WITH e AS (UPDATE waitlist SET status = 'expired'")
	for _, want := range []string{"status = 'offered' AND expires_at < now()", "LIMIT $1", "taken = taken - x.n"} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("expiry statement lacks %q", want)
		}
	}
	if c.args[0] != expireBatch {
		t.Errorf("batch = %v", c.args)
	}
	// an offer claimed meanwhile is not expired: the outer WHERE repeats the status
	if !strings.Contains(c.sql, "WHERE status = 'offered' AND id IN") {
		t.Error("the update re-checks the status")
	}
}

func TestMaintainExpiryStopsAtFiveBatches(t *testing.T) {
	h := newMaintainHost(t)
	h.expired, h.expiries = `[5]`, 100
	maintain(maintainBudget)
	if n := h.count("WITH e AS (UPDATE waitlist"); n != expireBatches {
		t.Errorf("%d expiry batches, want %d", n, expireBatches)
	}
}

func TestMaintainSnapshots(t *testing.T) {
	h := newMaintainHost(t)
	h.stale = `["e1","e2","e3"]`
	h.entities["e1"] = gigEntity
	h.entities["e3"] = strings.NewReplacer(`"status":"published"`, `"status":"draft"`, `"id":"e1"`, `"id":"e3"`).Replace(gigEntity)
	maintain(maintainBudget)
	snap := h.first("INSERT INTO events")
	if snap == nil || snap.args[0] != "e1" {
		t.Fatalf("e1 is snapshotted: %+v", snap)
	}
	closed := h.first("UPDATE ticket_types SET on_sale = false")
	if closed == nil || closed.args[0] != "e2" {
		t.Errorf("the sale of the missing e2 closes: %+v", closed)
	}
	if del := h.first("UPDATE events SET status = 'deleted'"); del == nil || del.args[0] != "e2" {
		t.Errorf("e2 is marked deleted: %+v", del)
	}
	// a draft is not deleted: its sale stays as it is
	if un := h.first("UPDATE events SET status = 'unpublished'"); un == nil || un.args[0] != "e3" {
		t.Errorf("e3 is marked unpublished: %+v", un)
	}
	if h.count("UPDATE ticket_types SET on_sale = false") != 1 {
		t.Error("only the missing event closes sales")
	}
	q := h.first("SELECT coalesce(jsonb_agg(x.event_id)")
	if !strings.Contains(q.sql, "interval '6 hours'") || !strings.Contains(q.sql, "seen_at NULLS FIRST") || q.args[0] != snapshotBatch {
		t.Errorf("snapshot query = %s %v", q.sql, q.args)
	}
}

func TestMaintainSnapshotHostErrorStops(t *testing.T) {
	h := newMaintainHost(t)
	h.stale = `["e1","e2"]`
	callHost0 := callHost
	callHost = func(fn string, args, out any) error {
		if fn == sdk.FnEntitiesGet {
			return errors.New("host down")
		}
		return callHost0(fn, args, out)
	}
	maintain(maintainBudget)
	if h.count("UPDATE events") != 0 || h.count("INSERT INTO events") != 0 {
		t.Error("a host error changes nothing")
	}
}

func TestMaintainDriftGuardsOnTouchedAt(t *testing.T) {
	h := newMaintainHost(t)
	maintain(maintainBudget)
	c := h.first("UPDATE ticket_types t SET taken = x.n")
	if c == nil {
		t.Fatal("no drift repair")
	}
	if n := strings.Count(c.sql, "touched_at < now() - interval '10 minutes'"); n != 2 {
		t.Errorf("the touched_at guard appears %d times, want 2", n)
	}
	for _, want := range []string{"a.status IN ('active', 'pending')", "w.status = 'offered'", "t.taken <> x.n"} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("drift statement lacks %q", want)
		}
	}
}

func TestMaintainRetentionMonths(t *testing.T) {
	h := newMaintainHost(t)
	maintain(maintainBudget)
	c := h.first("UPDATE orders SET name")
	if c == nil || c.args[0] != 24 || c.args[1] != retainBatch {
		t.Fatalf("default retention = %+v", c)
	}
	for _, want := range []string{"answers = '{}'::jsonb", "anonymised_at = now()", "o.anonymised_at IS NULL", "coalesce(e.ends_at, e.starts_at, o.created_at)", "make_interval(months => $1)", "LIMIT $2"} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("anonymise statement lacks %q", want)
		}
	}
	h = newMaintainHost(t)
	h.settings = `{"retention_months":6}`
	maintain(maintainBudget)
	if c := h.first("UPDATE orders SET name"); c.args[0] != 6 {
		t.Errorf("retention_months 6 => %v", c.args)
	}
	if strings.Contains(h.first("UPDATE orders SET name").sql, "DELETE FROM attendees") || h.count("DELETE FROM attendees") != 0 {
		t.Error("attendee rows are never deleted")
	}
}

func TestMaintainRetentionBatches(t *testing.T) {
	h := newMaintainHost(t)
	h.orders = []int64{500, 500, 500, 500, 500, 500, 500}
	maintain(maintainBudget)
	if n := h.count("UPDATE orders SET name"); n != retainBatches {
		t.Errorf("%d order batches, want %d", n, retainBatches)
	}
	h = newMaintainHost(t)
	h.orders = []int64{500, 120, 500}
	maintain(maintainBudget)
	if n := h.count("UPDATE orders SET name"); n != 2 {
		t.Errorf("a short batch ends the loop: %d batches", n)
	}
	// the waitlist and queue clean-ups run in bounded batches too
	h = newMaintainHost(t)
	h.other = 500
	maintain(maintainBudget)
	if h.count("DELETE FROM waitlist") != retainBatches || h.count("DELETE FROM mail_queue") != retainBatches {
		t.Errorf("deletes: waitlist %d, queue %d", h.count("DELETE FROM waitlist"), h.count("DELETE FROM mail_queue"))
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c.sql, "DELETE FROM") && !strings.Contains(c.sql, "LIMIT $2") {
			t.Errorf("unbounded delete: %s", c.sql)
		}
	}
}

func TestMaintainOverBudgetSkipsLaterSteps(t *testing.T) {
	h := newMaintainHost(t)
	h.tick = 5 * time.Second // the second call already passes the 8 s budget
	h.expired, h.expiries = `[5]`, 100
	h.stale = `["e1"]`
	maintain(maintainBudget)
	if h.count("UPDATE ticket_types t SET taken = x.n") != 0 || h.count("UPDATE orders SET name") != 0 || h.count("SELECT coalesce(jsonb_agg(x.event_id)") != 0 {
		t.Errorf("steps ran past the budget: %d calls", len(h.calls))
	}
	if len(h.calls) > 4 {
		t.Errorf("too many calls over budget: %d", len(h.calls))
	}
}

func TestMaintainCronSwallowsEveryError(t *testing.T) {
	h := newMaintainHost(t)
	h.dbErr = errors.New("pq: boom")
	h.expired, h.expiries = `[5]`, 1
	in := []byte(`{"name":"maintain"}`)
	if _, err := guest.Handle(guest.CronExport("maintain"), in); err != nil {
		t.Errorf("the cron returned %v", err)
	}
	if len(h.freed) != 0 {
		t.Error("nothing is promoted after a failed expiry")
	}
	if h.count("UPDATE orders SET name") != 1 {
		t.Errorf("a failing step does not stop the next one: %d", h.count("UPDATE orders SET name"))
	}
}

func TestMaintainClosesWaitlistsOfClosedEventsWithoutMail(t *testing.T) {
	h := newMaintainHost(t)
	maintain(maintainBudget)
	c := h.first("WITH c AS (UPDATE waitlist SET status = 'expired'")
	if c == nil {
		t.Fatal("no statement ends the waitlists of closed events")
	}
	for _, want := range []string{"status IN ('waiting', 'offered')", "ev.status IN ('cancelled', 'deleted')", "coalesce(ev.ends_at, ev.starts_at) <= now()",
		"offered_at IS NOT NULL AS held", "taken = taken - x.n"} {
		if !strings.Contains(c.sql, want) {
			t.Errorf("close statement lacks %q", want)
		}
	}
	for _, bad := range []string{"unpublished", "postponed", "ev.status = 'published'"} {
		if strings.Contains(c.sql, bad) {
			t.Errorf("a paused event keeps its waitlist, but the close statement names %q", bad)
		}
	}
	if h.count("INSERT INTO mail_queue") != 0 || len(h.freed) != 0 {
		t.Error("closing a waitlist mails nobody and offers no seat")
	}
	if i, j := h.order("WITH c AS"), h.order("WITH e AS"); i < 0 || j < 0 || i > j {
		t.Error("closed events are cleared before offers expire and promote")
	}
}

// Abandoned checkouts, handled payment events and queued refund mail are cleaned
// up in bounded batches, and only unpaid orders are deleted.
func TestMaintainRetentionOfPaidState(t *testing.T) {
	h := newMaintainHost(t)
	maintain(maintainBudget)
	o := h.first("DELETE FROM orders")
	if o == nil {
		t.Fatal("abandoned checkouts are never deleted")
	}
	for _, want := range []string{"status IN ('expired', 'cancelled')", "paid_at IS NULL", "provider IS NOT NULL", "payment_ref IS NULL", "problem IS NULL", "refunded_cents = 0", "interval '7 days'", "LIMIT $2", "$1::int"} {
		if !strings.Contains(o.sql, want) {
			t.Errorf("abandoned-order delete must keep orders that hold money, it lacks %q", want)
		}
	}
	p := h.first("DELETE FROM payment_events")
	if p == nil || !strings.Contains(p.sql, "interval '90 days'") || !strings.Contains(p.sql, "LIMIT $2") {
		t.Errorf("payment events are deleted after 90 days: %+v", p)
	}
	q := h.first("DELETE FROM mail_queue")
	if !strings.Contains(q.sql, "q.kind IN ('confirm', 'refund')") || !strings.Contains(q.sql, "q.kind NOT IN ('confirm', 'refund')") {
		t.Errorf("a refund mail refers to an order, not a waitlist entry: %s", q.sql)
	}
	for _, c := range h.calls {
		if len(c.args) == 2 && strings.HasPrefix(c.sql, "DELETE FROM") && (c.args[0] != 24 || c.args[1] != retainBatch) {
			t.Errorf("every retention step is called as (months, batch): %v", c.args)
		}
	}
}
