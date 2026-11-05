package main

import "time"

const (
	// leaseTime is how long a claimed row is left alone by other runs.
	leaseTime = 60 * time.Second
	// maxAge is how long a conversion may wait in the queue.
	maxAge = 24 * time.Hour
	// maxBackoff caps the wait between attempts.
	maxBackoff = time.Hour
	// logKeep is how long delivery log rows are kept.
	logKeep = 30 * 24 * time.Hour
	// clicksKeep is how long posted click IDs are kept.
	clicksKeep = time.Hour
	// maxMessage caps a platform message in the log.
	maxMessage = 300
)

// now is the clock; tests replace it.
var now = time.Now

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// cut shortens s to at most n runes.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// backoff is the wait after the given number of earlier attempts: 1m, 2m,
// 4m, ... up to one hour.
func backoff(attempts int) time.Duration {
	if attempts < 0 || attempts > 6 {
		return maxBackoff
	}
	return min(time.Minute<<attempts, maxBackoff)
}

// job is one queued conversion for one platform. It holds hashes and click
// IDs only, never a raw email, phone or name.
type job struct {
	ID           int64
	SubmissionID string
	Form         string
	Platform     string
	EmHash       string
	EmGoogleHash string
	PhHash       string
	IDs          clickIDs
	PagePath     string
	Consent      bool
	SubmittedAt  time.Time
	Attempts     int
	CreatedAt    time.Time
	IDsJoined    bool
}

// jobRow is a queue row as to_jsonb returns it.
type jobRow struct {
	ID           int64     `json:"id"`
	SubmissionID string    `json:"submission_id"`
	Form         string    `json:"form_slug"`
	Platform     string    `json:"platform"`
	EmHash       *string   `json:"em_hash"`
	EmGoogleHash *string   `json:"em_google_hash"`
	PhHash       *string   `json:"ph_hash"`
	Gclid        *string   `json:"gclid"`
	Gbraid       *string   `json:"gbraid"`
	Wbraid       *string   `json:"wbraid"`
	Ttclid       *string   `json:"ttclid"`
	LiFatID      *string   `json:"li_fat_id"`
	PagePath     string    `json:"page_path"`
	Consent      bool      `json:"consent"`
	SubmittedAt  time.Time `json:"submitted_at"`
	Attempts     int       `json:"attempts"`
	CreatedAt    time.Time `json:"created_at"`
	IDsJoined    bool      `json:"ids_joined"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (r jobRow) job() job {
	ids := clickIDs{}
	for k, p := range map[string]*string{"gclid": r.Gclid, "gbraid": r.Gbraid, "wbraid": r.Wbraid, "ttclid": r.Ttclid, "li_fat_id": r.LiFatID} {
		if v := str(p); v != "" {
			ids[k] = v
		}
	}
	return job{ID: r.ID, SubmissionID: r.SubmissionID, Form: r.Form, Platform: r.Platform,
		EmHash: str(r.EmHash), EmGoogleHash: str(r.EmGoogleHash), PhHash: str(r.PhHash), IDs: ids,
		PagePath: r.PagePath, Consent: r.Consent, SubmittedAt: r.SubmittedAt, Attempts: r.Attempts,
		CreatedAt: r.CreatedAt, IDsJoined: r.IDsJoined}
}

// nullable turns "" into a SQL NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// record writes one delivery log row. It holds no personal data: msg is a
// platform message, cut to 300 characters.
func record(submissionID, form, platform, status, reason, code, msg string) {
	if err := dbExec(`INSERT INTO log (created_at, submission_id, form_slug, platform, status, reason, code, message)
VALUES ($1::timestamptz, $2, $3, $4, $5, $6, $7, $8)`,
		ts(now()), submissionID, form, platform, status, reason, code, cut(msg, maxMessage)); err != nil {
		logf("warn", "ads-conversions: could not write the delivery log")
	}
}

// recordOnce writes a log row unless the submission already has one with the
// same status and reason, so a duplicate delivery does not log twice.
func recordOnce(submissionID, form, platform, status, reason string) {
	if err := dbExec(`INSERT INTO log (created_at, submission_id, form_slug, platform, status, reason)
SELECT $1::timestamptz, $2::text, $3::text, $4::text, $5::text, $6::text
WHERE NOT EXISTS (SELECT 1 FROM log WHERE submission_id = $2::text AND status = $5::text AND reason = $6::text)`,
		ts(now()), submissionID, form, platform, status, reason); err != nil {
		logf("warn", "ads-conversions: could not write the delivery log")
	}
}

// enqueue adds the job. It reports false when this submission was already
// queued for the platform, so a duplicate delivery sends nothing.
func enqueue(j job) (fresh bool, err error) {
	t := now()
	submitted := j.SubmittedAt
	if submitted.IsZero() {
		submitted = t
	}
	n, err := dbExecRows(`INSERT INTO queue (submission_id, form_slug, platform, em_hash, em_google_hash, ph_hash,
  gclid, gbraid, wbraid, ttclid, li_fat_id, page_path, consent, submitted_at, next_attempt_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::timestamptz, $15::timestamptz, $15::timestamptz)
ON CONFLICT (submission_id, platform) DO NOTHING`,
		j.SubmissionID, j.Form, j.Platform, nullable(j.EmHash), nullable(j.EmGoogleHash), nullable(j.PhHash),
		nullable(j.IDs["gclid"]), nullable(j.IDs["gbraid"]), nullable(j.IDs["wbraid"]), nullable(j.IDs["ttclid"]), nullable(j.IDs["li_fat_id"]),
		j.PagePath, j.Consent, ts(submitted), ts(t))
	return n > 0, err
}

const jobColumns = `q.id, q.submission_id, q.form_slug, q.platform, q.em_hash, q.em_google_hash, q.ph_hash,
  q.gclid, q.gbraid, q.wbraid, q.ttclid, q.li_fat_id, q.page_path, q.consent, q.submitted_at, q.attempts, q.created_at, q.ids_joined, q.next_attempt_at`

// claim leases up to limit rows chosen by cond (a WHERE fragment on queue q
// using $1 = now) so that no other run takes them.
func claim(cond string, t time.Time, limit int) ([]job, error) {
	var rows []jobRow
	err := dbJSON(`WITH c AS (
  SELECT q.id FROM queue q
  WHERE `+cond+` AND (q.lease_until IS NULL OR q.lease_until <= $1::timestamptz)
  ORDER BY q.next_attempt_at, q.id LIMIT $2 FOR UPDATE SKIP LOCKED
), u AS (
  UPDATE queue q SET lease_until = $3::timestamptz FROM c WHERE q.id = c.id
  RETURNING `+jobColumns+`
)
SELECT coalesce(jsonb_agg(to_jsonb(u) ORDER BY u.next_attempt_at, u.id), '[]'::jsonb) FROM u`,
		&rows, ts(t), limit, ts(t.Add(leaseTime)))
	jobs := make([]job, 0, len(rows))
	for _, r := range rows {
		jobs = append(jobs, r.job())
	}
	return jobs, err
}

// claimFirst leases due rows that were never tried and are either old enough
// (60 s) for the page script's click IDs to have arrived or already have them.
func claimFirst(t time.Time, limit int) ([]job, error) {
	return claim(`q.attempts = 0 AND q.next_attempt_at <= $1::timestamptz
  AND (q.created_at <= $1::timestamptz - interval '60 seconds' OR EXISTS (SELECT 1 FROM clicks c WHERE c.submission_id = q.submission_id))`, t, limit)
}

// claimRetry leases due rows that were tried before.
func claimRetry(t time.Time, limit int) ([]job, error) {
	return claim(`q.attempts > 0 AND q.next_attempt_at <= $1::timestamptz`, t, limit)
}

// joinClicks merges the click IDs the page script posted over the ones from
// the page address and writes the result onto every queue row of the
// submission, so retries keep them after the clicks row is purged. It runs
// once per row.
func joinClicks(j *job) error {
	if j.IDsJoined {
		return nil
	}
	var posted map[string]any
	if err := dbJSON(`SELECT coalesce((SELECT ids FROM clicks WHERE submission_id = $1), '{}'::jsonb)`, &posted, j.SubmissionID); err != nil {
		return err
	}
	ids := mergeIDs(j.IDs, cleanIDs(posted))
	if err := dbExec(`UPDATE queue SET gclid = $2, gbraid = $3, wbraid = $4, ttclid = $5, li_fat_id = $6, ids_joined = true
WHERE submission_id = $1`, j.SubmissionID, nullable(ids["gclid"]), nullable(ids["gbraid"]), nullable(ids["wbraid"]),
		nullable(ids["ttclid"]), nullable(ids["li_fat_id"])); err != nil {
		return err
	}
	j.IDs, j.IDsJoined = ids, true
	return nil
}

func deleteJob(j job) {
	if err := dbExec(`DELETE FROM queue WHERE id = $1`, j.ID); err != nil {
		logf("warn", "ads-conversions: could not clear a delivered conversion")
	}
}

// settleSent removes the row and logs the success.
func settleSent(j job, code, msg string) {
	deleteJob(j)
	record(j.SubmissionID, j.Form, j.Platform, "sent", "", code, msg)
}

// settleFailed removes the row and logs a permanent failure; it is not retried.
func settleFailed(j job, reason, code, msg string) {
	deleteJob(j)
	record(j.SubmissionID, j.Form, j.Platform, "failed", reason, code, msg)
	logf("warn", "ads-conversions: %s delivery failed (%s, code %s)", j.Platform, reason, cut(code, 20))
}

// settleRetry keeps the row and schedules the next attempt with backoff. Only
// the first retry is logged, so a long outage does not flood the log.
func settleRetry(j job, code, msg string) {
	t := now()
	if err := dbExec(`UPDATE queue SET attempts = attempts + 1, next_attempt_at = $2::timestamptz, lease_until = NULL WHERE id = $1`,
		j.ID, ts(t.Add(backoff(j.Attempts)))); err != nil {
		logf("warn", "ads-conversions: could not reschedule a conversion")
	}
	if j.Attempts == 0 {
		record(j.SubmissionID, j.Form, j.Platform, "retrying", "", code, msg)
	}
}

// release makes a claimed row due again at once, for a run that ran out of
// time before sending it.
func release(j job) {
	if err := dbExec(`UPDATE queue SET lease_until = NULL WHERE id = $1`, j.ID); err != nil {
		logf("warn", "ads-conversions: could not release a conversion")
	}
}

// expireOld deletes rows older than 24 h and logs each one, without any
// value, in a single statement so a large backlog cannot outlast the cron run.
func expireOld(t time.Time) {
	err := dbExec(`WITH d AS (DELETE FROM queue WHERE created_at < $1::timestamptz RETURNING submission_id, form_slug, platform)
INSERT INTO log (created_at, submission_id, form_slug, platform, status, reason)
SELECT $2::timestamptz, submission_id, form_slug, platform, 'failed', 'expired' FROM d`, ts(t.Add(-maxAge)), ts(t))
	if err != nil {
		logf("warn", "ads-conversions: could not purge the queue")
	}
}

// purge deletes log rows older than 30 days and click IDs older than an hour.
func purge(t time.Time) {
	if err := dbExec(`DELETE FROM log WHERE created_at < $1::timestamptz`, ts(t.Add(-logKeep))); err != nil {
		logf("warn", "ads-conversions: could not purge the delivery log")
	}
	if err := dbExec(`DELETE FROM clicks WHERE created_at < $1::timestamptz`, ts(t.Add(-clicksKeep))); err != nil {
		logf("warn", "ads-conversions: could not purge the click IDs")
	}
}
