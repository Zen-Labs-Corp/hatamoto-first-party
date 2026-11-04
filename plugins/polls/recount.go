package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const (
	recountPage     = 200 // the most forms.submissions returns
	recountDeadline = 8 * time.Second
	recountReadTime = 6 * time.Second // reading stops here, to leave time to write
	statusSpam      = "spam"
)

// upsertResponses writes one chunk of recounted response rows and marks them
// with the recount's cutoff time.
const upsertResponses = `INSERT INTO poll_response (form, submission_id, score, max_score, choices, recounted)
SELECT $1::text, x.id, x.score, x.mx, x.choices, $2::text::timestamptz
FROM jsonb_to_recordset($3::jsonb) AS x(id text, score int, mx int, choices jsonb)
ON CONFLICT (form, submission_id) DO UPDATE
SET score = excluded.score, max_score = excluded.max_score, choices = excluded.choices, recounted = excluded.recounted`

// finishRecount drops the rows the recount did not confirm and rebuilds the
// tallies from the rows that remain, in one statement. A row is dropped only
// when it is older than the cutoff ($2) and was not rewritten by this recount:
// an answer stored while the recount ran is newer than the cutoff, so it
// survives and still counts. Tallies come from the stored choices, so they
// always agree with the response rows. Rows the tally list no longer has are
// deleted first by key, because a data-modifying CTE that deleted and
// re-inserted the same keys could hit the primary key.
const finishRecount = `WITH keep AS (
  SELECT choices FROM poll_response
  WHERE form = $1::text
    AND NOT (created_at < $2::text::timestamptz AND recounted IS DISTINCT FROM $2::text::timestamptz)),
d1 AS (
  DELETE FROM poll_response
  WHERE form = $1::text
    AND created_at < $2::text::timestamptz AND recounted IS DISTINCT FROM $2::text::timestamptz),
nt AS (
  SELECT c->>'field' AS field, c->>'option' AS option, count(*)::int AS n
  FROM keep, jsonb_array_elements(coalesce(keep.choices, '[]'::jsonb)) AS c
  GROUP BY 1, 2),
d2 AS (
  DELETE FROM poll_tally WHERE form = $1::text AND (field, option) NOT IN (SELECT field, option FROM nt))
INSERT INTO poll_tally (form, field, option, n)
SELECT $1::text, field, option, n FROM nt
ON CONFLICT (form, field, option) DO UPDATE SET n = excluded.n`

// acquireLease takes the form's recount lease ($1 form, $2 run id) when nobody
// holds it or the holder's lease has run out. It returns a row only when the
// lease is now ours. The lease outlives the route budget, so a Recount that was
// cut off frees the form shortly after.
const acquireLease = `INSERT INTO poll_recount_lock (form_slug, run_id, expires_at)
VALUES ($1::text, $2::text, now() + interval '15 seconds')
ON CONFLICT (form_slug) DO UPDATE SET run_id = excluded.run_id, expires_at = excluded.expires_at
WHERE poll_recount_lock.expires_at < now()
RETURNING run_id`

// releaseLease frees the lease, but only when it is still ours.
const releaseLease = `DELETE FROM poll_recount_lock WHERE form_slug = $1::text AND run_id = $2::text`

// dbNow reads the database clock; created_at and the cutoff must come from
// the same clock.
const dbNow = `SELECT to_jsonb(clock_timestamp())`

// recountChunk is the most bytes of response rows one db.exec call carries,
// well under the 1 MiB request limit of the host.
var recountChunk = 512 << 10

type submission struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	Data      map[string]any `json:"data"`
	CreatedAt string         `json:"created_at"`
}

type responseRow struct {
	ID      string     `json:"id"`
	Score   *int       `json:"score"`
	Max     *int       `json:"mx"`
	Choices []tallyRow `json:"choices"`
}

var (
	errRecountIncomplete = errors.New("recount incomplete")
	errRecountBusy       = errors.New("recount already running")
)

// lockRecount takes the form's lease and returns the function that releases it.
func lockRecount(slug string) (func(), error) {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	run := hex.EncodeToString(id[:])
	var res struct {
		Rows [][]json.RawMessage `json:"rows"`
	}
	if err := callHost("db.query", map[string]any{"sql": acquireLease, "args": []any{slug, run}}, &res); err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, errRecountBusy
	}
	return func() {
		if err := dbExec(releaseLease, slug, run); err != nil {
			_ = logf("warn", "polls: recount lease of %q not released: %v", slug, err)
		}
	}, nil
}

// recount rebuilds one form's rows from the stored submissions and returns the
// number of responses counted. It reads every page first and writes after, so a
// failure or a timeout while reading leaves the old rows alone. Spam and
// submissions after closes_at are skipped. Answers stored while it runs are
// kept (see finishRecount).
func recount(cfg formCfg) (int, error) {
	forms, err := listForms()
	if err != nil {
		return 0, err
	}
	form, ok := forms[cfg.Slug]
	if !ok {
		return 0, errors.New("form not found")
	}
	release, err := lockRecount(cfg.Slug)
	if err != nil {
		return 0, err
	}
	defer release()
	fields := choiceFields(form.Fields)
	end, closes := cfg.closes()
	var cutoff string
	if err := dbJSON(dbNow, &cutoff); err != nil {
		return 0, err
	}
	start := now()
	seen := map[string]bool{}
	var responses []responseRow
	for off := 0; ; off += recountPage {
		if now().Sub(start) > recountReadTime {
			return 0, errRecountIncomplete
		}
		var page struct {
			Items []submission `json:"items"`
			Total int          `json:"total"`
		}
		args := map[string]any{"form_slug": cfg.Slug, "limit": recountPage, "offset": off}
		if err := callHost("forms.submissions", args, &page); err != nil {
			return 0, err
		}
		for _, s := range page.Items {
			if s.Status == statusSpam || seen[s.ID] {
				continue
			}
			if at, ok := parseTime(s.CreatedAt); closes && ok && !at.Before(end) {
				continue
			}
			seen[s.ID] = true
			row := responseRow{ID: s.ID, Choices: sortedRows(countFor(fields, s.Data))}
			if cfg.Kind == kindQuiz {
				got, max := score(cfg, form.Fields, s.Data)
				row.Score, row.Max = &got, &max
			}
			responses = append(responses, row)
		}
		if len(page.Items) == 0 || off+recountPage >= page.Total {
			break
		}
	}
	for _, chunk := range chunks(responses, recountChunk) {
		if now().Sub(start) > recountDeadline {
			return 0, errRecountIncomplete
		}
		if err := dbExec(upsertResponses, cfg.Slug, cutoff, chunk); err != nil {
			return 0, err
		}
	}
	if err := dbExec(finishRecount, cfg.Slug, cutoff); err != nil {
		return 0, err
	}
	return len(responses), nil
}

// chunks splits rows so that each part encodes to at most max bytes (a single
// row larger than that gets a part of its own).
func chunks(rows []responseRow, max int) [][]responseRow {
	var out [][]responseRow
	size := 0
	for _, r := range rows {
		b, _ := json.Marshal(r)
		if len(out) == 0 || size+len(b) > max {
			out = append(out, nil)
			size = 0
		}
		out[len(out)-1] = append(out[len(out)-1], r)
		size += len(b)
	}
	return out
}
