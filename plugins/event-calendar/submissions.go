package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// Submissions come from the public form and wait in the plugin's own table
// (migrations/004_submissions.sql). Only routes, hooks and cron read or write
// it; a run never does, so the submitter's email never reaches a changeset.

const (
	submissionRetention = 90 * 24 * time.Hour // approved and published
	rejectedRetention   = 30 * 24 * time.Hour
	secretKey           = "time-token"
	maxSubmissionRows   = 100
)

var errDuplicate = errors.New("duplicate submission")

// submission mirrors a row of the submissions table.
type submission struct {
	ID          int64  `json:"id"`
	Status      string `json:"status"`
	Title       string `json:"title"`
	Start       string `json:"start_at"`
	End         string `json:"end_at"`
	Venue       string `json:"venue"`
	Description string `json:"description"`
	Email       string `json:"email"`
	CreatedAt   string `json:"created_at"`
	Notified    bool   `json:"notified"`
}

const submissionCols = `id, status, title, start_at, end_at, venue, description, email, created_at, notified`

// hmacSecret is the signing key of the time token. It is generated on first
// use; two first requests at once both insert, one wins, both read the winner.
func hmacSecret() ([]byte, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	if err := dbExec(`INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, secretKey, hex.EncodeToString(buf[:])); err != nil {
		return nil, err
	}
	var value string
	if err := dbJSON(`SELECT to_jsonb(value) FROM meta WHERE key = $1`, &value, secretKey); err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(value)
	if err != nil || len(key) != len(buf) {
		return nil, errors.New("unusable signing key")
	}
	return key, nil
}

// insertSubmission stores a pending submission; errDuplicate when the same
// email, title and start already wait.
func insertSubmission(s submission) error {
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost(sdk.FnDBExec, map[string]any{"sql": `INSERT INTO submissions (title, start_at, end_at, venue, description, email)
VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`, "args": []any{s.Title, s.Start, s.End, s.Venue, s.Description, s.Email}}, &res)
	if err != nil {
		return err
	}
	if res.RowsAffected == 0 {
		return errDuplicate
	}
	return nil
}

func pendingCount() (int, error) {
	var n int
	err := dbJSON(`SELECT to_jsonb(count(*)) FROM submissions WHERE status = 'pending'`, &n)
	return n, err
}

// submissionCounts is the number of submissions in each status.
func submissionCounts() (map[string]int, error) {
	counts := map[string]int{}
	err := dbJSON(`SELECT coalesce(jsonb_object_agg(status, n), '{}'::jsonb) FROM (SELECT status, count(*) AS n FROM submissions GROUP BY status) c`, &counts)
	return counts, err
}

// listSubmissions reads up to maxSubmissionRows, oldest first; status "" or
// "all" lists every status.
func listSubmissions(status string) ([]submission, error) {
	var out []submission
	if status == "" || status == "all" {
		err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM (SELECT `+submissionCols+` FROM submissions ORDER BY created_at, id LIMIT $1) s`, &out, maxSubmissionRows)
		return out, err
	}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM (SELECT `+submissionCols+` FROM submissions WHERE status = $1 ORDER BY created_at, id LIMIT $2) s`, &out, status, maxSubmissionRows)
	return out, err
}

// openSubmissions lists the pending and approved rows, oldest first.
func openSubmissions() ([]submission, error) {
	var out []submission
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM (SELECT `+submissionCols+` FROM submissions WHERE status IN ('pending', 'approved') ORDER BY id LIMIT $1) s`, &out, maxSubmissionRows)
	return out, err
}

// getSubmission reads one row; false when there is none.
func getSubmission(id int64) (submission, bool, error) {
	var out []submission
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM (SELECT `+submissionCols+` FROM submissions WHERE id = $1) s`, &out, id)
	if err != nil || len(out) == 0 {
		return submission{}, false, err
	}
	return out[0], true, nil
}

// setSubmissionStatus moves a submission to a new status; with from, only one
// that is in one of those statuses. It reports whether a row changed, and is
// false on any error (callers are hooks and routes that must not fail on it).
func setSubmissionStatus(id int64, to string, from ...string) bool {
	sql := `UPDATE submissions SET status = $2, updated_at = now() WHERE id = $1`
	args := []any{id, to}
	if len(from) > 0 {
		marks := make([]string, len(from))
		for i, f := range from {
			marks[i] = "$" + strconv.Itoa(i+3)
			args = append(args, f)
		}
		sql += ` AND status IN (` + strings.Join(marks, ", ") + `)`
	}
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	return callHost(sdk.FnDBExec, map[string]any{"sql": sql, "args": args}, &res) == nil && res.RowsAffected > 0
}

// rejectAllPending rejects every pending submission and returns how many.
func rejectAllPending() (int, error) {
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost(sdk.FnDBExec, map[string]any{"sql": `UPDATE submissions SET status = 'rejected', updated_at = now() WHERE status = 'pending'`, "args": []any{}}, &res)
	return res.RowsAffected, err
}

func deleteSubmission(id int64) (bool, error) {
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost(sdk.FnDBExec, map[string]any{"sql": `DELETE FROM submissions WHERE id = $1`, "args": []any{id}}, &res)
	return res.RowsAffected > 0, err
}

// pruneSubmissions removes approved and published rows after 90 days and
// rejected rows after 30; pending ones stay until someone acts on them.
func pruneSubmissions(now time.Time) error {
	return dbExec(`DELETE FROM submissions WHERE (status IN ('approved', 'published') AND updated_at < $1::timestamptz)
 OR (status = 'rejected' AND updated_at < $2::timestamptz)`,
		utcText(now.Add(-submissionRetention)), utcText(now.Add(-rejectedRetention)))
}

// unnotified lists the pending submissions the owner was not mailed about.
func unnotified() ([]submission, error) {
	var out []submission
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM (SELECT `+submissionCols+` FROM submissions WHERE status = 'pending' AND NOT notified ORDER BY created_at, id LIMIT $1) s`, &out, maxSubmissionRows)
	return out, err
}

// markNotified records that the owner was told about these submissions.
func markNotified(ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	return dbExec(`UPDATE submissions SET notified = true WHERE id IN (SELECT jsonb_array_elements_text($1::jsonb)::bigint)`, jsonText(ids))
}
