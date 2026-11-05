package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// maxBackoff caps the wait between attempts.
const maxBackoff = 2 * time.Hour

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// jsonString encodes v as a JSON string argument for a ::jsonb cast.
func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// backoff is the wait after the given number of earlier attempts: 5m, 10m,
// 20m, 40m, 80m, then 2h.
func backoff(attempts int) time.Duration {
	if attempts < 0 || attempts > 8 {
		return maxBackoff
	}
	return min(5*time.Minute<<attempts, maxBackoff)
}

// record writes one delivery log row for a queued job. message is a fixed
// phrase or CRM field names, never a value and never provider text.
func record(j job, status string, code int, message string) {
	recordRow(j.SubmissionID, j.FormSlug, j.Provider, status, "", "", code, message)
}

// recordRow writes one delivery log row; code 0 is stored as NULL. It never
// takes an email or a field value.
func recordRow(submissionID, form, provider, status, recordID, recordURL string, code int, message string) {
	var c any
	if code > 0 {
		c = code
	}
	if err := dbExec(`INSERT INTO log (created_at, submission_id, form_slug, provider, status, record_id, record_url, code, message) VALUES ($1::timestamptz, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ts(now()), submissionID, form, provider, status, recordID, recordURL, c, message); err != nil {
		logf("warn", "could not write the delivery log")
	}
}

// enqueue adds the job, due now and leased to nobody. It reports false when
// the submission was queued before.
func enqueue(j job) (bool, error) {
	t := ts(now())
	fields := j.Fields
	if fields == nil {
		fields = map[string]string{}
	}
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost("db.exec", map[string]any{
		"sql": `INSERT INTO queue (submission_id, form_slug, provider, email, fields, source_path, submitted_at, attempts, next_attempt_at, lease_until, created_at)
VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7::timestamptz, 0, $8::timestamptz, $8::timestamptz, $8::timestamptz)
ON CONFLICT (submission_id) DO NOTHING`,
		"args": []any{j.SubmissionID, j.FormSlug, j.Provider, j.Email, jsonString(fields), j.SourcePath, ts(j.SubmittedAt), t},
	}, &res)
	return res.RowsAffected > 0, err
}

// settle applies one delivery outcome: finished jobs leave the queue, a
// retryable one waits with backoff, and every outcome but a budget cut gets a
// log row. A job cut short by the time budget is released and moves behind the
// other due rows.
func settle(j job, r result) {
	switch r.kind {
	case kindBudget:
		releaseLease(j.SubmissionID)
	case kindCreated, kindUpdated, kindFail:
		if err := dbExec(`DELETE FROM queue WHERE submission_id = $1`, j.SubmissionID); err != nil {
			logf("warn", "could not clear a finished submission")
		}
		if r.kind == kindFail {
			recordRow(j.SubmissionID, j.FormSlug, j.Provider, "failed", "", "", r.status, r.reason)
			logf("warn", "delivery failed (%s, code %d)", r.reason, r.status)
			return
		}
		recordRow(j.SubmissionID, j.FormSlug, j.Provider, r.kind, r.recordID, r.recordURL, r.status, r.message)
	default:
		t := now()
		if err := dbExec(`UPDATE queue SET attempts = attempts + 1, next_attempt_at = $2::timestamptz, lease_until = $3::timestamptz WHERE submission_id = $1`,
			j.SubmissionID, ts(t.Add(backoff(j.Attempts))), ts(t)); err != nil {
			logf("warn", "could not reschedule a submission")
		}
		msg := fmt.Sprintf("attempt %d", j.Attempts+1)
		if r.reason != "" {
			msg += ", " + r.reason
		}
		record(j, "retrying", r.status, msg)
		logf("warn", "delivery will be retried (%s, code %d)", msg, r.status)
	}
}

// releaseLease makes a claimed row due again at once, without an attempt.
func releaseLease(submissionID string) {
	if err := dbExec(`UPDATE queue SET lease_until = $2::timestamptz, next_attempt_at = $2::timestamptz WHERE submission_id = $1`, submissionID, ts(now())); err != nil {
		logf("warn", "could not release a submission")
	}
}
