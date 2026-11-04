package main

import (
	"fmt"
	"time"
)

const (
	// leaseTime is how long a claimed row is left alone by other runs.
	leaseTime = 2 * time.Minute
	// maxBackoff caps the wait between attempts.
	maxBackoff = 2 * time.Hour
)

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// backoff is the wait after the given number of earlier attempts: 5m, 10m, 20m, ... up to 2h.
func backoff(attempts int) time.Duration {
	if attempts < 0 || attempts > 8 {
		return maxBackoff
	}
	return min(5*time.Minute<<attempts, maxBackoff)
}

// record writes one delivery log row. It holds no personal data.
func record(form, status, reason string, code int) {
	var c any
	if code > 0 {
		c = code
	}
	if err := dbExec(`INSERT INTO log (created_at, form_slug, status, reason, code) VALUES ($1::timestamptz, $2, $3, $4, $5)`,
		ts(now()), form, status, reason, c); err != nil {
		logf("warn", "subscriber-sync: could not write the delivery log")
	}
}

// enqueue adds the job, leased to the caller so the cron leaves it alone while
// the inline attempt runs. It reports false when the submission was queued before.
func enqueue(j job) (bool, error) {
	t := now()
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost("db.exec", map[string]any{
		"sql": `INSERT INTO queue (submission_id, form_slug, email, first_name, last_name, next_attempt_at, lease_until, created_at)
VALUES ($1, $2, $3, $4, $5, $6::timestamptz, $7::timestamptz, $6::timestamptz)
ON CONFLICT (submission_id) DO NOTHING`,
		"args": []any{j.SubmissionID, j.FormSlug, j.Email, j.FirstName, j.LastName, ts(t), ts(t.Add(leaseTime))},
	}, &res)
	return res.RowsAffected > 0, err
}

// settle applies one delivery outcome: finished jobs leave the queue, a
// retryable one waits with backoff, and every outcome gets a log row. A job cut
// short by the time budget is only released for the next run.
func settle(j job, r result) {
	switch r.kind {
	case kindBudget:
		if err := dbExec(`UPDATE queue SET lease_until = $2::timestamptz WHERE submission_id = $1`, j.SubmissionID, ts(now())); err != nil {
			logf("warn", "subscriber-sync: could not release a submission")
		}
	case kindSent, kindUpdated, kindFail:
		if err := dbExec(`DELETE FROM queue WHERE submission_id = $1`, j.SubmissionID); err != nil {
			logf("warn", "subscriber-sync: could not clear a delivered submission")
		}
		status := r.kind
		if r.kind == kindFail {
			status = "failed"
		}
		record(j.FormSlug, status, r.reason, r.status)
		if r.kind == kindFail {
			logf("warn", "subscriber-sync: delivery failed (%s, code %d)", r.reason, r.status)
		}
	default:
		t := now()
		if err := dbExec(`UPDATE queue SET attempts = attempts + 1, next_attempt_at = $2::timestamptz, lease_until = $3::timestamptz WHERE submission_id = $1`,
			j.SubmissionID, ts(t.Add(backoff(j.Attempts))), ts(t)); err != nil {
			logf("warn", "subscriber-sync: could not reschedule a submission")
		}
		reason := fmt.Sprintf("attempt %d", j.Attempts+1)
		if r.reason != "" {
			reason += ", " + r.reason
		}
		record(j.FormSlug, "retrying", reason, r.status)
		logf("warn", "subscriber-sync: delivery will be retried (%s, code %d)", reason, r.status)
	}
}
