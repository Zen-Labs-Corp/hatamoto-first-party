package main

import (
	"errors"
	"time"
)

const (
	recountPage     = 100
	recountDeadline = 8 * time.Second
	statusSpam      = "spam"
)

// replaceForm replaces one form's rows with the recounted ones in a single
// statement. It upserts the new rows and deletes only the rows that are not in
// the new set: a data-modifying CTE that deleted and re-inserted the same keys
// could run its insert first and hit the primary key.
const replaceForm = `WITH nr AS (
  SELECT x.id, x.score, x.mx FROM jsonb_to_recordset($2::jsonb) AS x(id text, score int, mx int)),
nt AS (
  SELECT t.field, t.option, t.n FROM jsonb_to_recordset($3::jsonb) AS t(field text, option text, n int)),
d1 AS (
  DELETE FROM poll_response WHERE form = $1::text AND submission_id NOT IN (SELECT id FROM nr)),
d2 AS (
  DELETE FROM poll_tally WHERE form = $1::text AND (field, option) NOT IN (SELECT field, option FROM nt)),
r AS (
  INSERT INTO poll_response (form, submission_id, score, max_score)
  SELECT $1::text, id, score, mx FROM nr
  ON CONFLICT (form, submission_id) DO UPDATE SET score = excluded.score, max_score = excluded.max_score)
INSERT INTO poll_tally (form, field, option, n)
SELECT $1::text, field, option, n FROM nt
ON CONFLICT (form, field, option) DO UPDATE SET n = excluded.n`

type submission struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	Data      map[string]any `json:"data"`
	CreatedAt string         `json:"created_at"`
}

type responseRow struct {
	ID    string `json:"id"`
	Score *int   `json:"score"`
	Max   *int   `json:"mx"`
}

type countRow struct {
	Field  string `json:"field"`
	Option string `json:"option"`
	N      int    `json:"n"`
}

var errRecountIncomplete = errors.New("recount incomplete")

// recount rebuilds one form's rows from the stored submissions and returns the
// number of responses counted. It reads every page first and writes once, so a
// failure or a timeout leaves the old rows alone. Spam and submissions after
// closes_at are skipped.
func recount(cfg formCfg) (int, error) {
	forms, err := listForms()
	if err != nil {
		return 0, err
	}
	form, ok := forms[cfg.Slug]
	if !ok {
		return 0, errors.New("form not found")
	}
	fields := choiceFields(form.Fields)
	end, closes := cfg.closes()
	start := now()
	seen := map[string]bool{}
	tallies := map[[2]string]int{}
	var responses []responseRow
	for off := 0; ; off += recountPage {
		if now().Sub(start) > recountDeadline {
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
			row := responseRow{ID: s.ID}
			if cfg.Kind == kindQuiz {
				got, max := score(cfg, form.Fields, s.Data)
				row.Score, row.Max = &got, &max
			}
			responses = append(responses, row)
			for k, n := range countFor(fields, s.Data) {
				tallies[k] += n
			}
		}
		if len(page.Items) == 0 || off+recountPage >= page.Total {
			break
		}
	}
	rows := make([]countRow, 0, len(tallies))
	for _, r := range sortedRows(tallies) {
		rows = append(rows, countRow{r.Field, r.Option, tallies[[2]string{r.Field, r.Option}]})
	}
	if responses == nil {
		responses = []responseRow{}
	}
	if err := dbExec(replaceForm, cfg.Slug, responses, rows); err != nil {
		return 0, err
	}
	return len(responses), nil
}
