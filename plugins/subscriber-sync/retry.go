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
		settle(j, deliverFn(c, j))
	}
	return nil
}

// expire deletes rows older than 24 h and logs each one, without the email.
func expire(t time.Time) {
	var rows []struct {
		FormSlug string `json:"form_slug"`
	}
	err := dbJSON(`WITH d AS (DELETE FROM queue WHERE created_at < $1::timestamptz RETURNING form_slug)
SELECT coalesce(jsonb_agg(to_jsonb(d)), '[]'::jsonb) FROM d`, &rows, ts(t.Add(-maxAge)))
	if err != nil {
		logf("warn", "subscriber-sync: could not purge the queue")
		return
	}
	for _, r := range rows {
		record(r.FormSlug, "expired", "too old", 0)
	}
}

// claim leases up to batchSize due rows so that no other run takes them.
func claim(t time.Time) ([]job, error) {
	var jobs []job
	err := dbJSON(`WITH c AS (
  SELECT id FROM queue
  WHERE next_attempt_at <= $1::timestamptz AND lease_until <= $1::timestamptz AND created_at >= $2::timestamptz
  ORDER BY id LIMIT $3 FOR UPDATE SKIP LOCKED
), u AS (
  UPDATE queue q SET lease_until = $4::timestamptz FROM c WHERE q.id = c.id
  RETURNING q.id, q.submission_id, q.form_slug, q.email, q.first_name, q.last_name, q.attempts
)
SELECT coalesce(jsonb_agg(to_jsonb(u) ORDER BY u.id), '[]'::jsonb) FROM u`,
		&jobs, ts(t), ts(t.Add(-maxAge)), batchSize, ts(t.Add(leaseTime)))
	return jobs, err
}
