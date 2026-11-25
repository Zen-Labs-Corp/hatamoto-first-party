package main

import (
	"errors"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	maintainBudget = 8 * time.Second // the cron call may run 10 s
	expireBatch    = 50
	expireBatches  = 5
	snapshotBatch  = 50
	retainBatch    = 500
	retainBatches  = 5
)

func init() {
	guest.OnCron("maintain", func(sdk.CronEvent) error {
		maintain(maintainBudget)
		return nil
	})
}

// maintain is the hourly upkeep. Each step stops when the budget is used and
// swallows its own errors: the next run starts again where this one stopped,
// and the cron never fails.
func maintain(budget time.Duration) {
	deadline := now().Add(budget)
	over := func() bool { return !now().Before(deadline) }
	s := loadSettings()
	closeWaitlists(over)
	expireOffers(over)
	if !over() {
		refreshSnapshots(s, over)
	}
	if !over() {
		repairDrift()
	}
	if !over() {
		anonymise(s.RetentionMonths, over)
	}
}

// closeWaitlists ends the waiting entries and offers of events that are over,
// cancelled or deleted, without mail. Unpublished and postponed events only
// pause their waitlists; the entries stay. An offer holds a seat, which
// goes back in the same statement; offered_at tells an offer because RETURNING
// sees the new status.
func closeWaitlists(over func() bool) {
	for range expireBatches {
		if over() {
			return
		}
		var n int
		err := dbJSON(`WITH c AS (UPDATE waitlist SET status = 'expired'
    WHERE status IN ('waiting', 'offered') AND id IN (SELECT id FROM waitlist WHERE status IN ('waiting', 'offered')
        AND `+eventOverSQL("waitlist.event_id")+` ORDER BY id LIMIT $1)
    RETURNING type_id, offered_at IS NOT NULL AS held),
 t AS (UPDATE ticket_types t SET taken = taken - x.n, touched_at = now()
    FROM (SELECT type_id, count(*) n FROM c WHERE held GROUP BY type_id) x WHERE t.id = x.type_id RETURNING t.id)
SELECT to_jsonb((SELECT count(*) FROM c))`, &n, expireBatch)
		if err != nil || n < expireBatch {
			return
		}
	}
}

// expireOffers gives the seat of every offer past its window back and offers
// it to the next person waiting. The outer WHERE repeats the status so an
// offer claimed in the meantime is left alone.
func expireOffers(over func() bool) {
	for range expireBatches {
		if over() {
			return
		}
		var types []int64
		err := dbJSON(`WITH e AS (UPDATE waitlist SET status = 'expired'
    WHERE status = 'offered' AND id IN (SELECT id FROM waitlist WHERE status = 'offered' AND expires_at < now() ORDER BY id LIMIT $1)
    RETURNING type_id),
 t AS (UPDATE ticket_types t SET taken = taken - x.n, touched_at = now()
    FROM (SELECT type_id, count(*) n FROM e GROUP BY type_id) x WHERE t.id = x.type_id RETURNING t.id)
SELECT coalesce(jsonb_agg(id), '[]'::jsonb) FROM t`, &types, expireBatch)
		if err != nil || len(types) == 0 {
			return
		}
		for _, id := range types {
			if over() {
				return
			}
			onSeatsFreed(id)
		}
	}
}

// refreshSnapshots re-reads the events whose snapshot is oldest, so the dates
// stay right when an event is edited while the plugin is not looking. An event
// the host no longer has closes its sale.
func refreshSnapshots(s settings, over func() bool) {
	var ids []string
	err := dbJSON(`SELECT coalesce(jsonb_agg(x.event_id), '[]'::jsonb) FROM (
SELECT t.event_id FROM (SELECT DISTINCT event_id FROM ticket_types) t
LEFT JOIN events e ON e.entity_id = t.event_id
WHERE coalesce(e.status, '') <> 'deleted' AND (e.seen_at IS NULL OR e.seen_at < now() - interval '6 hours')
ORDER BY e.seen_at NULLS FIRST, t.event_id LIMIT $1) x`, &ids, snapshotBatch)
	if err != nil {
		return
	}
	for _, id := range ids {
		if over() {
			return
		}
		e, ok, err := readEvent(id, s)
		switch {
		case err != nil:
			return // the host is struggling: try again next hour
		case ok:
			_ = snapshotEvent(e)
		case entityGone(id):
			_ = dbExec(`UPDATE events SET status = 'deleted', seen_at = now() WHERE entity_id = $1`, id)
			_ = dbExec(`UPDATE ticket_types SET on_sale = false, touched_at = now() WHERE event_id = $1`, id)
		default:
			_ = dbExec(`UPDATE events SET status = 'unpublished', seen_at = now() WHERE entity_id = $1`, id)
		}
	}
}

// entityGone reports whether the host has no entity with this id at all, as
// opposed to one that is a draft or of another type.
func entityGone(id string) bool {
	var raw entityDTO
	err := callHost(sdk.FnEntitiesGet, map[string]any{"id": id}, &raw)
	var he *sdk.HostError
	return errors.As(err, &he) && he.Code == sdk.ErrNotFound
}

// repairDrift sets taken back to what the tickets and held offers add up to.
// Pending attendees count, all of them, lapsed or not: a lapsed hold still sits
// in taken until expireHolds flips it and gives the seat back in one statement.
// Leaving it out here would make this statement give the seat back first and
// expireHolds again, and an undercount sells a seat twice.
// Only rows untouched for 10 minutes are looked at, in the subquery and again
// in the update, so a reservation that races this statement is never undone.
func repairDrift() {
	_ = dbExec(`UPDATE ticket_types t SET taken = x.n FROM (
SELECT tt.id, (SELECT count(*) FROM attendees a WHERE a.type_id = tt.id AND a.status IN ('active', 'pending'))
  + (SELECT count(*) FROM waitlist w WHERE w.type_id = tt.id AND w.status = 'offered') AS n
FROM ticket_types tt WHERE tt.touched_at < now() - interval '10 minutes') x
WHERE t.id = x.id AND t.taken <> x.n AND t.touched_at < now() - interval '10 minutes'`)
}

// anonymise clears the personal data of orders past retention, deletes the
// waitlist entries of those events and the queued mail that points at nothing
// any more. The attendee rows stay: counts and ticket codes are not personal.
// Paid orders are anonymised like free ones and keep their amounts and provider
// ids. Abandoned checkouts (a hold that lapsed or was cancelled, never paid) are
// deleted after 7 days, only when nothing shows that money moved: no payment or
// capture id, no problem flag (refund_failed, amount_mismatch), nothing refunded, and handled payment events after 90 days. The two
// deletes name $1 only to keep the call shape of the other steps.
func anonymise(months int, over func() bool) {
	steps := []string{
		`UPDATE orders SET name = '', email = '', answers = '{}'::jsonb, anonymised_at = now()
WHERE id IN (SELECT o.id FROM orders o LEFT JOIN events e ON e.entity_id = o.event_id
  WHERE o.anonymised_at IS NULL AND coalesce(e.ends_at, e.starts_at, o.created_at) < now() - make_interval(months => $1) LIMIT $2)`,
		`DELETE FROM waitlist WHERE id IN (SELECT w.id FROM waitlist w LEFT JOIN events e ON e.entity_id = w.event_id
  WHERE coalesce(e.ends_at, e.starts_at, w.created_at) < now() - make_interval(months => $1) LIMIT $2)`,
		`DELETE FROM mail_queue WHERE id IN (SELECT q.id FROM mail_queue q
  WHERE (q.kind IN ('confirm', 'refund') AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.id = q.ref_id AND o.anonymised_at IS NULL))
     OR (q.kind NOT IN ('confirm', 'refund') AND NOT EXISTS (SELECT 1 FROM waitlist w WHERE w.id = q.ref_id)) LIMIT $2)`,
		`DELETE FROM orders WHERE id IN (SELECT id FROM orders
  WHERE status IN ('expired', 'cancelled') AND paid_at IS NULL AND provider IS NOT NULL
  AND payment_ref IS NULL AND problem IS NULL AND refunded_cents = 0
  AND created_at < now() - interval '7 days' AND $1::int > 0 LIMIT $2)`,
		`DELETE FROM payment_events WHERE (provider, event_id) IN (SELECT provider, event_id FROM payment_events
  WHERE received_at < now() - interval '90 days' AND $1::int > 0 LIMIT $2)`,
	}
	for _, sql := range steps {
		for range retainBatches {
			if over() {
				return
			}
			n, err := dbExecN(sql, months, retainBatch)
			if err != nil || n < retainBatch {
				break
			}
		}
	}
}
