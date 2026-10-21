package main

import (
	"encoding/json"
	"time"
)

// connection is the stored Google connection; the tokens are sealed.
type connection struct {
	RefreshToken    string `json:"refresh_token"`
	AccessToken     string `json:"access_token"`
	AccessExpiresAt string `json:"access_expires_at"`
	Scope           string `json:"scope"`
	SiteURL         string `json:"site_url"`
	ConnectedAt     string `json:"connected_at"`
}

func loadConnection() (*connection, error) {
	var rows []connection
	if err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(c)), '[]'::jsonb) FROM connection c`, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// storeConnection saves a fresh connection from the code exchange; the site
// choice survives a reconnect.
func storeConnection(key []byte, t tokenResponse) error {
	refresh, err := seal(key, t.RefreshToken)
	if err != nil {
		return err
	}
	access, err := seal(key, t.AccessToken)
	if err != nil {
		return err
	}
	return dbExec(`INSERT INTO connection (id, refresh_token, access_token, access_expires_at, scope)
		VALUES (1, $1, $2, $3::timestamptz, $4)
		ON CONFLICT (id) DO UPDATE SET refresh_token = EXCLUDED.refresh_token, access_token = EXCLUDED.access_token,
		  access_expires_at = EXCLUDED.access_expires_at, scope = EXCLUDED.scope, connected_at = now()`,
		refresh, access, expiry(t), t.Scope)
}

// saveAccessToken stores a refreshed access token (Google may rotate the
// refresh token too).
func saveAccessToken(key []byte, t tokenResponse) error {
	access, err := seal(key, t.AccessToken)
	if err != nil {
		return err
	}
	if t.RefreshToken != "" {
		refresh, err := seal(key, t.RefreshToken)
		if err != nil {
			return err
		}
		return dbExec(`UPDATE connection SET access_token = $1, access_expires_at = $2::timestamptz, refresh_token = $3 WHERE id = 1`, access, expiry(t), refresh)
	}
	return dbExec(`UPDATE connection SET access_token = $1, access_expires_at = $2::timestamptz WHERE id = 1`, access, expiry(t))
}

func expiry(t tokenResponse) string {
	return time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second).Format(time.RFC3339)
}

func chooseSite(site string) error {
	return dbExec(`UPDATE connection SET site_url = $1 WHERE id = 1`, site)
}

func disconnect() error { return dbExec(`DELETE FROM connection`) }

// importedDays lists the days already stored (YYYY-MM-DD) from since on.
func importedDays(since string) (map[string]bool, error) {
	var days []string
	if err := dbJSON(`SELECT coalesce(jsonb_agg(to_char(day, 'YYYY-MM-DD')), '[]'::jsonb) FROM imported_days WHERE day >= $1::date`, &days, since); err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, d := range days {
		m[d] = true
	}
	return m, nil
}

// storeRows upserts rows of Search Analytics (idempotent: a page stored twice
// after an interrupted run changes nothing).
func storeRows(rows []perfRow) error {
	const chunk = 500
	for i := 0; i < len(rows); i += chunk {
		part := rows[i:min(i+chunk, len(rows))]
		b, _ := json.Marshal(part)
		if err := dbExec(`INSERT INTO perf_daily (day, query, page, clicks, impressions, ctr, avg_position)
			SELECT x.day, x.query, x.page, x.clicks, x.impressions, x.ctr, x.avg_position
			  FROM jsonb_to_recordset($1::jsonb) AS x(day date, query text, page text, clicks integer, impressions integer, ctr double precision, avg_position double precision)
			ON CONFLICT (day, query, page) DO UPDATE SET clicks = EXCLUDED.clicks, impressions = EXCLUDED.impressions,
			  ctr = EXCLUDED.ctr, avg_position = EXCLUDED.avg_position`, string(b)); err != nil {
			return err
		}
	}
	return nil
}

// markDay records a day as fully imported (even when it had no rows) and
// forgets its paging progress.
func markDay(site, day string, total int) error {
	if err := dbExec(`INSERT INTO imported_days (day, site_url, row_count) VALUES ($1::date, $2, $3)
		ON CONFLICT (day) DO UPDATE SET site_url = EXCLUDED.site_url, row_count = EXCLUDED.row_count, imported_at = now()`, day, site, total); err != nil {
		return err
	}
	return dbExec(`DELETE FROM import_progress WHERE day = $1::date`, day)
}

// dayProgress is where an interrupted import of one day stopped.
type dayProgress struct {
	NextRow int `json:"next_row"`
	Stored  int `json:"stored"`
}

// loadProgress returns the progress of day for site (zero when it has none or
// was started for another site).
func loadProgress(site, day string) (dayProgress, error) {
	var p []dayProgress
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(p)), '[]'::jsonb) FROM
		(SELECT next_row, stored FROM import_progress WHERE day = $1::date AND site_url = $2) p`, &p, day, site)
	if err != nil || len(p) == 0 {
		return dayProgress{}, err
	}
	return p[0], nil
}

func saveProgress(site, day string, p dayProgress) error {
	return dbExec(`INSERT INTO import_progress (day, site_url, next_row, stored) VALUES ($1::date, $2, $3, $4)
		ON CONFLICT (day) DO UPDATE SET site_url = EXCLUDED.site_url, next_row = EXCLUDED.next_row, stored = EXCLUDED.stored, updated_at = now()`,
		day, site, p.NextRow, p.Stored)
}

func recordRun(days, rows int, runErr error) {
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
	}
	if err := dbExec(`INSERT INTO import_runs (days, row_count, error) VALUES ($1, $2, $3)`, days, rows, msg); err != nil {
		logf("warn", "recording the import run: %v", err)
	}
}

// topRow is one line of the "top queries" or "top pages" table.
type topRow struct {
	Key         string  `json:"key"`
	Clicks      int     `json:"clicks"`
	Impressions int     `json:"impressions"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"pos"`
}

// topBy returns the best rows by clicks for the last days days, grouped by
// column (query or page), with impression-weighted position.
func topBy(column string, days, limit int) ([]topRow, error) {
	var rows []topRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.clicks DESC, t.impressions DESC), '[]'::jsonb) FROM
		(SELECT `+column+` AS key, sum(clicks)::integer AS clicks, sum(impressions)::integer AS impressions,
		        CASE WHEN sum(impressions) = 0 THEN 0 ELSE sum(clicks)::float8 / sum(impressions) END AS ctr,
		        CASE WHEN sum(impressions) = 0 THEN 0 ELSE sum(avg_position * impressions) / sum(impressions) END AS pos
		   FROM perf_daily WHERE day >= (now() - make_interval(days => $1))::date
		  GROUP BY `+column+` ORDER BY sum(clicks) DESC, sum(impressions) DESC LIMIT $2) t`, &rows, days, limit)
	if rows == nil {
		rows = []topRow{}
	}
	return rows, err
}

type runInfo struct {
	RanAt string `json:"ran_at"`
	Days  int    `json:"days"`
	Rows  int    `json:"row_count"`
	Error string `json:"error"`
}

func lastRun() (*runInfo, error) {
	var runs []runInfo
	if err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(r)), '[]'::jsonb) FROM
		(SELECT ran_at, days, row_count, error FROM import_runs ORDER BY id DESC LIMIT 1) r`, &runs); err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

func lastImportedDay() (string, error) {
	var d []string
	if err := dbJSON(`SELECT coalesce(jsonb_agg(m.d), '[]'::jsonb) FROM (SELECT to_char(max(day), 'YYYY-MM-DD') AS d FROM imported_days HAVING max(day) IS NOT NULL) m`, &d); err != nil || len(d) == 0 {
		return "", err
	}
	return d[0], nil
}

func lastSitemap() (map[string]string, error) {
	var rows []map[string]string
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM
		(SELECT submitted_at, sitemap_url, error FROM sitemap_submissions ORDER BY id DESC LIMIT 1) s`, &rows)
	if len(rows) == 0 {
		return nil, err
	}
	return rows[0], err
}
