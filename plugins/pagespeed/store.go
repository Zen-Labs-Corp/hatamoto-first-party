package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	dateFmt         = "2006-01-02"
	retentionMonths = 13
	progressDays    = 7
	maxLabAttempts  = 2 // the first run and one retry
)

// dbExec runs one statement that changes rows.
func dbExec(sql string, args ...any) error {
	return callHost("db.exec", map[string]any{"sql": sql, "args": args}, nil)
}

// dbJSON runs a query that returns a single jsonb cell and decodes it into out.
func dbJSON(sql string, out any, args ...any) error {
	var res struct {
		Rows [][]json.RawMessage `json:"rows"`
	}
	if err := callHost("db.query", map[string]any{"sql": sql, "args": args}, &res); err != nil {
		return err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return errors.New("query returned no rows")
	}
	cell := res.Rows[0][0]
	// jsonb may arrive as a JSON value or as a JSON string holding JSON.
	var s string
	if json.Unmarshal(cell, &s) == nil && len(s) > 0 && (s[0] == '[' || s[0] == '{') {
		cell = json.RawMessage(s)
	}
	return json.Unmarshal(cell, out)
}

func logf(level, format string, a ...any) {
	_ = callHost("log", map[string]string{"level": level, "message": fmt.Sprintf(format, a...)}, nil)
}

// cats returns the categories of a field result's three metrics.
func (f fieldResult) cats() (lcp, inp, cls string) {
	c := func(metric string, v *float64) string {
		if v == nil {
			return ""
		}
		return category(metric, *v)
	}
	return c("lcp", f.LCP), c("inp", f.INP), c("cls", f.CLS)
}

const qUpsertField = `INSERT INTO metrics (url, day, strategy, lcp_ms, inp_ms, cls, lcp_cat, inp_cat, cls_cat, field_status, measured_at)
	VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8, $9, $10, now())
	ON CONFLICT (url, day, strategy) DO UPDATE SET lcp_ms = EXCLUDED.lcp_ms, inp_ms = EXCLUDED.inp_ms, cls = EXCLUDED.cls,
		lcp_cat = EXCLUDED.lcp_cat, inp_cat = EXCLUDED.inp_cat, cls_cat = EXCLUDED.cls_cat,
		field_status = EXCLUDED.field_status, measured_at = now()`

// upsertField stores the field data of a page and leaves its lab columns alone.
func upsertField(day, url, strategy string, f fieldResult) error {
	lcp, inp, cls := f.cats()
	return dbExec(qUpsertField, url, day, strategy, f.LCP, f.INP, f.CLS, lcp, inp, cls, f.Status)
}

const qUpsertLab = `INSERT INTO metrics (url, day, strategy, lab_score, lab_status, measured_at)
	VALUES ($1, $2::date, $3, $4, $5, now())
	ON CONFLICT (url, day, strategy) DO UPDATE SET lab_score = EXCLUDED.lab_score, lab_status = EXCLUDED.lab_status, measured_at = now()`

// upsertLab stores the lab result of a page; only a finished run has a score.
func upsertLab(day, url, strategy string, l labResult) error {
	var score any
	if l.Status == labOK {
		score = l.Score
	}
	return dbExec(qUpsertLab, url, day, strategy, score, l.Status)
}

const qUpsertOrigin = `INSERT INTO origin_summary (day, strategy, lcp_ms, inp_ms, cls, lcp_cat, inp_cat, cls_cat, status)
	VALUES ($1::date, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (day, strategy) DO UPDATE SET lcp_ms = EXCLUDED.lcp_ms, inp_ms = EXCLUDED.inp_ms, cls = EXCLUDED.cls,
		lcp_cat = EXCLUDED.lcp_cat, inp_cat = EXCLUDED.inp_cat, cls_cat = EXCLUDED.cls_cat, status = EXCLUDED.status`

func upsertOrigin(day, strategy string, f fieldResult) error {
	lcp, inp, cls := f.cats()
	return dbExec(qUpsertOrigin, day, strategy, f.LCP, f.INP, f.CLS, lcp, inp, cls, f.Status)
}

// originDone lists the strategies whose origin summary is stored for day.
func originDone(day string) (map[string]bool, error) {
	var list []string
	if err := dbJSON(`SELECT coalesce(jsonb_agg(strategy), '[]'::jsonb) FROM origin_summary WHERE day = $1::date`, &list, day); err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, s := range list {
		m[s] = true
	}
	return m, nil
}

// Steps of a work item.
const (
	stepCrux = "crux"
	stepLab  = "lab"
)

type workItem struct {
	URL, Strategy, Step string
}

type progressRow struct {
	URL         string `json:"url"`
	Strategy    string `json:"strategy"`
	CruxDone    bool   `json:"crux_done"`
	LabState    string `json:"lab_state"`
	LabAttempts int    `json:"lab_attempts"`
}

func dayProgress(day string) (map[[2]string]progressRow, error) {
	var rows []progressRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(p)), '[]'::jsonb) FROM
		(SELECT url, strategy, crux_done, lab_state, lab_attempts FROM progress WHERE day = $1::date) p`, &rows, day)
	m := map[[2]string]progressRow{}
	for _, r := range rows {
		m[[2]string{r.URL, r.Strategy}] = r
	}
	return m, err
}

// nextWork picks the next step of the day: every field lookup first, then the
// lab runs not tried yet, then the lab runs that timed out once.
func nextWork(day string, targets, strategies []string) (workItem, bool, error) {
	prog, err := dayProgress(day)
	if err != nil {
		return workItem{}, false, err
	}
	for _, u := range targets {
		for _, s := range strategies {
			if !prog[[2]string{u, s}].CruxDone {
				return workItem{u, s, stepCrux}, true, nil
			}
		}
	}
	for _, retry := range []bool{false, true} {
		for _, u := range targets {
			for _, s := range strategies {
				p := prog[[2]string{u, s}]
				switch {
				case !retry && p.LabState == labPending,
					retry && p.LabState == labTimeout && p.LabAttempts < maxLabAttempts:
					return workItem{u, s, stepLab}, true, nil
				}
			}
		}
	}
	return workItem{}, false, nil
}

func markCrux(day, url, strategy string) error {
	return dbExec(`INSERT INTO progress (day, url, strategy, crux_done) VALUES ($1::date, $2, $3, true)
		ON CONFLICT (day, url, strategy) DO UPDATE SET crux_done = true`, day, url, strategy)
}

// markLab records a lab run: its state and one more attempt.
func markLab(day, url, strategy, state string) error {
	return dbExec(`INSERT INTO progress (day, url, strategy, crux_done, lab_state, lab_attempts) VALUES ($1::date, $2, $3, true, $4, 1)
		ON CONFLICT (day, url, strategy) DO UPDATE SET lab_state = EXCLUDED.lab_state, lab_attempts = progress.lab_attempts + 1`,
		day, url, strategy, state)
}

// metricRow is one stored (page, day, device) row.
type metricRow struct {
	URL         string   `json:"url"`
	Day         string   `json:"day"`
	Strategy    string   `json:"strategy"`
	LCP         *float64 `json:"lcp_ms"`
	INP         *float64 `json:"inp_ms"`
	CLS         *float64 `json:"cls"`
	LCPCat      string   `json:"lcp_cat"`
	INPCat      string   `json:"inp_cat"`
	CLSCat      string   `json:"cls_cat"`
	FieldStatus string   `json:"field_status"`
	LabScore    *int     `json:"lab_score"`
	LabStatus   string   `json:"lab_status"`
	MeasuredAt  string   `json:"measured_at"`
}

const rowCols = `url, to_char(day, 'YYYY-MM-DD') AS day, strategy, lcp_ms, inp_ms, cls, lcp_cat, inp_cat, cls_cat,
	field_status, lab_score, lab_status, to_char(measured_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS measured_at`

// latestRows returns the newest stored row of every (page, device).
func latestRows() ([]metricRow, error) {
	var rows []metricRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM
		(SELECT DISTINCT ON (url, strategy) `+rowCols+` FROM metrics ORDER BY url, strategy, day DESC) t`, &rows)
	return rows, err
}

// dayRows returns the rows of one page on one day.
func dayRows(day, url string) ([]metricRow, error) {
	var rows []metricRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.strategy), '[]'::jsonb) FROM
		(SELECT `+rowCols+` FROM metrics WHERE day = $1::date AND url = $2) t`, &rows, day, url)
	return rows, err
}

type seriesPoint struct {
	Day string   `json:"d"`
	LCP *float64 `json:"lcp"`
	INP *float64 `json:"inp"`
	CLS *float64 `json:"cls"`
}

type series struct {
	URL      string        `json:"url"`
	Strategy string        `json:"strategy"`
	Points   []seriesPoint `json:"points"`
}

// seriesFor returns the field values per day since sinceDay for the pages.
func seriesFor(urls []string, sinceDay string) ([]series, error) {
	list, _ := json.Marshal(urls)
	var out []series
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM
		(SELECT url, strategy, jsonb_agg(jsonb_build_object('d', to_char(day, 'YYYY-MM-DD'), 'lcp', lcp_ms, 'inp', inp_ms, 'cls', cls) ORDER BY day) AS points
		   FROM metrics WHERE day >= $1::date AND field_status = 'ok'
		    AND url IN (SELECT jsonb_array_elements_text($2::text::jsonb))
		  GROUP BY url, strategy) s`, &out, sinceDay, string(list))
	return out, err
}

type originRow struct {
	Strategy string   `json:"strategy"`
	LCP      *float64 `json:"lcp_ms"`
	INP      *float64 `json:"inp_ms"`
	CLS      *float64 `json:"cls"`
	LCPCat   string   `json:"lcp_cat"`
	INPCat   string   `json:"inp_cat"`
	CLSCat   string   `json:"cls_cat"`
	Status   string   `json:"status"`
}

// latestOrigin returns the newest origin summary per device.
func latestOrigin() ([]originRow, error) {
	var rows []originRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM
		(SELECT DISTINCT ON (strategy) strategy, lcp_ms, inp_ms, cls, lcp_cat, inp_cat, cls_cat, status
		   FROM origin_summary ORDER BY strategy, day DESC) t`, &rows)
	return rows, err
}

// dayCounts is how many (page, device) pairs are finished today and when the
// last measurement was stored.
func dayCounts(day string) (done int, at string, err error) {
	var r struct {
		Done int    `json:"done"`
		At   string `json:"at"`
	}
	err = dbJSON(`SELECT jsonb_build_object(
		'done', (SELECT count(*) FROM progress WHERE day = $1::date AND crux_done AND (lab_state <> 'pending' AND (lab_state <> 'timeout' OR lab_attempts >= 2))),
		'at', coalesce((SELECT to_char(max(measured_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM metrics WHERE day = $1::date), ''))`, &r, day)
	return r.Done, r.At, err
}

// cutoffDay is the first day of the UTC month 13 months before t's month;
// rows older than it are deleted.
func cutoffDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month()-retentionMonths, 1, 0, 0, 0, 0, time.UTC)
}

// runRetention deletes measurements older than 13 months and checkpoints older
// than a week.
func runRetention(t time.Time) error {
	cutoff := cutoffDay(t).Format(dateFmt)
	for _, table := range []string{`DELETE FROM metrics WHERE day < $1::date`, `DELETE FROM origin_summary WHERE day < $1::date`} {
		if err := dbExec(table, cutoff); err != nil {
			return err
		}
	}
	return dbExec(`DELETE FROM progress WHERE day < $1::date`, t.UTC().AddDate(0, 0, -progressDays).Format(dateFmt))
}
