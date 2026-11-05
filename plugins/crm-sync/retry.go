package main

import (
	"time"

	"hatamoto.dev/sdk"
)

// runQueue drains one kind of due rows: newRows true takes rows never tried
// (the deliver cron), false takes rows that were tried before (the retry
// cron). It stops starting deliveries when the time budget is spent.
func runQueue(newRows bool) {
	start := now()
	c, err := loadCfg()
	if err != nil {
		logf("warn", "settings unreadable")
		return
	}
	if !c.configured() {
		return
	}
	jobs, err := claim(start, newRows)
	if err != nil {
		logf("warn", "could not read the queue")
		return
	}
	budgetEnd = start.Add(cronBudget)
	defer func() { budgetEnd = time.Time{} }()
	m, _ := loadMap(c.Provider, c.object())
	for i, j := range jobs {
		if budgetEnd.Sub(now()) < minCall {
			// The rest were claimed but not reached: free them now instead
			// of letting them wait out the lease.
			for _, rest := range jobs[i:] {
				releaseLease(rest.SubmissionID)
			}
			break
		}
		if j.Provider != c.Provider {
			drop(j, "provider changed")
			continue
		}
		left := budgetEnd.Sub(now())
		settle(j, slowCut(deliverFn(c, j, m), left, cronBudget))
	}
}

// drop removes a row that cannot be delivered and logs why.
func drop(j job, why string) {
	if err := dbExec(`DELETE FROM queue WHERE submission_id = $1`, j.SubmissionID); err != nil {
		logf("warn", "could not clear a skipped submission")
	}
	record(j, "skipped", 0, why)
}

// onDeliver sends rows that have not been tried yet. It never returns an error.
func onDeliver(sdk.CronEvent) error {
	runQueue(true)
	return nil
}

// onRetry drops what is too old, trims the log, then sends rows that failed
// before. It never returns an error.
func onRetry(sdk.CronEvent) error {
	t := now()
	expire(t)
	purgeLog(t)
	runQueue(false)
	return nil
}

// onRefresh renews the provider's access token. It never returns an error.
func onRefresh(sdk.CronEvent) error {
	c, err := loadCfg()
	if err != nil || !c.configured() {
		return nil
	}
	p := providerFor(c.Provider)
	if p == nil {
		return nil
	}
	budgetEnd = now().Add(cronBudget)
	defer func() { budgetEnd = time.Time{} }()
	if r := p.refresh(c, doFetch); r.kind == kindFail {
		logf("warn", "token refresh failed (code %d)", r.status)
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

// expire deletes rows older than 24 h and logs each one, without the email, in
// a single statement so a large backlog cannot outlast the cron run.
func expire(t time.Time) {
	err := dbExec(`WITH d AS (DELETE FROM queue WHERE created_at < $1::timestamptz RETURNING submission_id, form_slug, provider)
INSERT INTO log (created_at, submission_id, form_slug, provider, status, message) SELECT $2::timestamptz, submission_id, form_slug, provider, 'expired', 'too old' FROM d`,
		ts(t.Add(-maxAge)), ts(t))
	if err != nil {
		logf("warn", "could not purge the queue")
	}
}

// purgeLog deletes delivery log rows older than the retention.
func purgeLog(t time.Time) {
	if err := dbExec(`DELETE FROM log WHERE created_at < $1::timestamptz`, ts(t.Add(-logKeep))); err != nil {
		logf("warn", "could not purge the delivery log")
	}
}

// claim leases up to batchSize due rows so that no other run takes them: rows
// never tried when newRows is true, rows tried before otherwise.
func claim(t time.Time, newRows bool) ([]job, error) {
	var jobs []job
	err := dbJSON(`WITH c AS (
  SELECT id FROM queue
  WHERE next_attempt_at <= $1::timestamptz AND lease_until <= $1::timestamptz AND created_at >= $2::timestamptz AND ((attempts = 0) = $5)
  ORDER BY next_attempt_at, id LIMIT $3 FOR UPDATE SKIP LOCKED
), u AS (
  UPDATE queue q SET lease_until = $4::timestamptz FROM c WHERE q.id = c.id
  RETURNING q.id, q.next_attempt_at, q.submission_id, q.form_slug, q.provider, q.email, q.fields, q.source_path, q.submitted_at, q.attempts
)
SELECT coalesce(jsonb_agg(to_jsonb(u) ORDER BY u.next_attempt_at, u.id), '[]'::jsonb) FROM u`,
		&jobs, ts(t), ts(t.Add(-maxAge)), batchSize, ts(t.Add(leaseTime)), newRows)
	return jobs, err
}
