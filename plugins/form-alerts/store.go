package main

import "time"

const (
	// leaseTime is how long a claimed row is left alone by other runs.
	leaseTime = 2 * time.Minute
	// maxBackoff caps the wait between attempts.
	maxBackoff = 2 * time.Hour
	// maxRetryWait caps a wait asked for by a platform's Retry-After.
	maxRetryWait = time.Hour
	// maxAge is how long a message may wait in the queue.
	maxAge = 6 * time.Hour
	// logKeep is how long delivery log rows are kept.
	logKeep = 30 * 24 * time.Hour
	// batchSize is the most rows one run claims.
	batchSize = 50
)

// Outcome kinds of one delivery attempt.
const (
	kindSent   = "sent"
	kindFail   = "fail"
	kindRetry  = "retry"
	kindBudget = "budget"
)

// job is one queued message: one submission on one channel.
type job struct {
	ID           int64     `json:"id"`
	SubmissionID string    `json:"submission_id"`
	FormSlug     string    `json:"form_slug"`
	Channel      string    `json:"channel"`
	Body         string    `json:"body"`
	Attempts     int       `json:"attempts"`
	CreatedAt    time.Time `json:"created_at"`
}

// result is the outcome of one delivery attempt. reason is a fixed word.
type result struct {
	kind       string
	reason     string
	status     int
	retryAfter time.Duration
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// backoff is the wait after the given number of earlier attempts: 5m, 10m, 20m, ... up to 2h.
func backoff(attempts int) time.Duration {
	if attempts < 0 || attempts > 8 {
		return maxBackoff
	}
	return min(5*time.Minute<<attempts, maxBackoff)
}

// delayFor is the wait before the next attempt: the longer of the backoff and
// what the platform asked for, at most an hour.
func delayFor(attempts int, retryAfter time.Duration) time.Duration {
	return min(max(backoff(attempts), retryAfter), maxRetryWait)
}

// record writes one delivery log row. It holds no field values, URL or token.
func record(submissionID, form, channel, status string, code int) {
	if err := dbExec(`INSERT INTO log (created_at, submission_id, form_slug, channel, status, code) VALUES ($1::timestamptz, $2, $3, $4, $5, $6)`,
		ts(now()), submissionID, form, channel, status, code); err != nil {
		logf("warn", "form-alerts: could not write the delivery log")
	}
}

// enqueue adds the message, due now. It reports false when this submission
// was already queued for the channel (the host delivered the event twice).
func enqueue(j job) (bool, error) {
	t := now()
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost("db.exec", map[string]any{
		"sql": `INSERT INTO queue (submission_id, form_slug, channel, body, next_at, lease_until, created_at)
VALUES ($1, $2, $3, $4, $5::timestamptz, $5::timestamptz, $5::timestamptz)
ON CONFLICT (submission_id, channel) DO NOTHING`,
		"args": []any{j.SubmissionID, j.FormSlug, j.Channel, j.Body, ts(t)},
	}, &res)
	return res.RowsAffected > 0, err
}

// claim leases up to batchSize due rows so that no other run takes them.
func claim(t time.Time) ([]job, error) {
	var jobs []job
	err := dbJSON(`WITH c AS (
  SELECT id FROM queue
  WHERE next_at <= $1::timestamptz AND lease_until <= $1::timestamptz AND created_at >= $2::timestamptz
  ORDER BY next_at, id LIMIT $3 FOR UPDATE SKIP LOCKED
), u AS (
  UPDATE queue q SET lease_until = $4::timestamptz FROM c WHERE q.id = c.id
  RETURNING q.id, q.next_at, q.submission_id, q.form_slug, q.channel, q.body, q.attempts, q.created_at
)
SELECT coalesce(jsonb_agg(to_jsonb(u) ORDER BY u.next_at, u.id), '[]'::jsonb) FROM u`,
		&jobs, ts(t), ts(t.Add(-maxAge)), batchSize, ts(t.Add(leaseTime)))
	return jobs, err
}

// settle applies one delivery outcome: finished messages leave the queue, a
// retryable one waits, and every outcome but a budget cut gets a log row. A
// message cut short by the time budget is released and moves behind the other
// due rows; it does not count as an attempt.
func settle(j job, r result) {
	switch r.kind {
	case kindBudget:
		if err := dbExec(`UPDATE queue SET lease_until = $3::timestamptz, next_at = $3::timestamptz WHERE submission_id = $1 AND channel = $2`,
			j.SubmissionID, j.Channel, ts(now())); err != nil {
			logf("warn", "form-alerts: could not release a message")
		}
	case kindSent, kindFail:
		if err := dbExec(`DELETE FROM queue WHERE submission_id = $1 AND channel = $2`, j.SubmissionID, j.Channel); err != nil {
			logf("warn", "form-alerts: could not clear a finished message")
		}
		status := "sent"
		if r.kind == kindFail {
			status = "failed"
			logf("warn", "form-alerts: delivery failed (%s %s, code %d)", j.Channel, r.reason, r.status)
		}
		record(j.SubmissionID, j.FormSlug, j.Channel, status, r.status)
	default:
		t := now()
		if err := dbExec(`UPDATE queue SET attempts = attempts + 1, next_at = $3::timestamptz, lease_until = $4::timestamptz WHERE submission_id = $1 AND channel = $2`,
			j.SubmissionID, j.Channel, ts(t.Add(delayFor(j.Attempts, r.retryAfter))), ts(t)); err != nil {
			logf("warn", "form-alerts: could not reschedule a message")
		}
		record(j.SubmissionID, j.FormSlug, j.Channel, "retrying", r.status)
		logf("warn", "form-alerts: delivery will be retried (%s %s, code %d)", j.Channel, r.reason, r.status)
	}
}

// expire deletes rows older than six hours and logs each one as expired, in a
// single statement so a large backlog cannot outlast the cron run.
func expire(t time.Time) {
	err := dbExec(`WITH d AS (DELETE FROM queue WHERE created_at < $1::timestamptz RETURNING submission_id, form_slug, channel)
INSERT INTO log (created_at, submission_id, form_slug, channel, status) SELECT $2::timestamptz, submission_id, form_slug, channel, 'expired' FROM d`,
		ts(t.Add(-maxAge)), ts(now()))
	if err != nil {
		logf("warn", "form-alerts: could not purge the queue")
	}
}

// pruneLog deletes log rows older than 30 days.
func pruneLog(t time.Time) {
	if err := dbExec(`DELETE FROM log WHERE created_at < $1::timestamptz`, ts(t.Add(-logKeep))); err != nil {
		logf("warn", "form-alerts: could not purge the delivery log")
	}
}
