package main

import (
	"time"

	"hatamoto.dev/sdk"
)

const (
	// maxAge is how long a submission may wait in the queue.
	maxAge = 24 * time.Hour
	// logKeep is how long delivery log rows are kept.
	logKeep = 30 * 24 * time.Hour
	// batchSize is the most rows one run claims.
	batchSize = 50
	// cronBudget is how long one run keeps starting deliveries; the hook's own
	// limit is 10 s (plugin.json).
	cronBudget = 8 * time.Second
)

// onRetry drains the queue: it drops what is too old, then delivers due rows
// until the batch or the time budget is used up. It never returns an error.
func onRetry(sdk.CronEvent) error {
	start := now()
	expire(start)
	if err := dbExec(`DELETE FROM log WHERE created_at < $1::timestamptz`, ts(start.Add(-logKeep))); err != nil {
		logf("warn", "subscriber-sync: could not purge the delivery log")
	}
	c, err := loadCfg()
	if err != nil {
		logf("warn", "subscriber-sync: settings unreadable")
		return nil
	}
	if !c.configured() {
		return nil
	}
	jobs, err := claim(start)
	if err != nil {
		logf("warn", "subscriber-sync: could not read the queue")
		return nil
	}
	budgetEnd = start.Add(cronBudget)
	defer func() { budgetEnd = time.Time{} }()
	for _, j := range jobs {
		if budgetEnd.Sub(now()) < minCall {
			break
		}
		left := budgetEnd.Sub(now())
		settle(j, slowCut(deliverFn(c, j), left, cronBudget))
	}
	return nil
}

// slowCut turns a budget cut into a normal retry when the job started with
// (nearly) the whole budget: it will not fit next time either, so it must be
// counted and backed off instead of heading the queue every run.
func slowCut(r result, left, total time.Duration) result {
	if r.kind == kindBudget && left >= total-minCall {
		return result{kind: kindRetry, reason: "too slow"}
	}
	return r
}

// expire deletes rows older than 24 h and logs each one, without the email,
// in a single statement so a large backlog cannot outlast the cron run.
func expire(t time.Time) {
	err := dbExec(`WITH d AS (DELETE FROM queue WHERE created_at < $1::timestamptz RETURNING form_slug)
INSERT INTO log (created_at, form_slug, status, reason) SELECT $2::timestamptz, form_slug, 'expired', 'too old' FROM d`,
		ts(t.Add(-maxAge)), ts(now()))
	if err != nil {
		logf("warn", "subscriber-sync: could not purge the queue")
	}
}

// claim leases up to batchSize due rows so that no other run takes them.
func claim(t time.Time) ([]job, error) {
	var jobs []job
	err := dbJSON(`WITH c AS (
  SELECT id FROM queue
  WHERE next_attempt_at <= $1::timestamptz AND lease_until <= $1::timestamptz AND created_at >= $2::timestamptz
  ORDER BY next_attempt_at, id LIMIT $3 FOR UPDATE SKIP LOCKED
), u AS (
  UPDATE queue q SET lease_until = $4::timestamptz FROM c WHERE q.id = c.id
  RETURNING q.id, q.next_attempt_at, q.submission_id, q.form_slug, q.email, q.first_name, q.last_name, q.attempts
)
SELECT coalesce(jsonb_agg(to_jsonb(u) ORDER BY u.next_attempt_at, u.id), '[]'::jsonb) FROM u`,
		&jobs, ts(t), ts(t.Add(-maxAge)), batchSize, ts(t.Add(leaseTime)))
	return jobs, err
}
