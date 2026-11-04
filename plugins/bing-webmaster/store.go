package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// All SQL is unqualified (the host sets the plugin's schema), one statement per
// call, values only as $n arguments. Table and column names come from the
// fixed tables below, never from input.

const (
	dateFmt        = "2006-01-02"
	batchRows      = 200 // rows per INSERT
	retentionStats = 16  // months traffic_daily, query_stats and page_stats are kept
	retentionLogs  = 30  // days import_runs and sitemap_submissions are kept
	maxErrorText   = 500
	maxKeyBytes    = 2000 // longest url, query or page that is stored
)

// statTables maps the two weekly stat tables to their key column.
var statTables = map[string]string{"query_stats": "query", "page_stats": "page"}

// clean drops the NUL byte, which the database refuses in text.
func clean(s string) string { return strings.ReplaceAll(s, "\x00", "") }

// batchInsert runs INSERT head VALUES (...),(...) tail in batches. types gives
// the cast of each column ("" for none).
func batchInsert(head string, types []string, tail string, rows [][]any) error {
	for start := 0; start < len(rows); start += batchRows {
		batch := rows[start:min(start+batchRows, len(rows))]
		var sb strings.Builder
		sb.WriteString(head + " VALUES ")
		args := make([]any, 0, len(batch)*len(types))
		for i, row := range batch {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			for j, v := range row {
				if j > 0 {
					sb.WriteString(", ")
				}
				fmt.Fprintf(&sb, "$%d", len(args)+1)
				if types[j] != "" {
					sb.WriteString("::" + types[j])
				}
				args = append(args, v)
			}
			sb.WriteString(")")
		}
		sb.WriteString(" " + tail)
		if err := dbExec(sb.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

// dedupe keeps the last row per key: one INSERT cannot touch a key twice.
func dedupe[T any](rows []T, key func(T) string) []T {
	at := map[string]int{}
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		k := key(r)
		if i, seen := at[k]; seen {
			out[i] = r
			continue
		}
		at[k] = len(out)
		out = append(out, r)
	}
	return out
}

func storeTraffic(rows []trafficRow) error {
	rows = dedupe(rows, func(r trafficRow) string { return r.Day })
	data := make([][]any, len(rows))
	for i, r := range rows {
		data[i] = []any{r.Day, r.Clicks, r.Impressions}
	}
	return batchInsert("INSERT INTO traffic_daily (day, clicks, impressions)", []string{"date", "", ""},
		"ON CONFLICT (day) DO UPDATE SET clicks = EXCLUDED.clicks, impressions = EXCLUDED.impressions", data)
}

// storeStats upserts weekly rows into query_stats or page_stats.
func storeStats(table string, rows []statRow) error {
	col, ok := statTables[table]
	if !ok {
		return fmt.Errorf("unknown stats table %q", table)
	}
	rows = dedupe(rows, func(r statRow) string { return r.Week + "\x00" + r.Key })
	data := make([][]any, 0, len(rows))
	for _, r := range rows {
		if !tooLong(r.Key) {
			data = append(data, []any{r.Week, clean(r.Key), r.Clicks, r.Impressions, r.AvgClickPos, r.AvgImprPos})
		}
	}
	return batchInsert("INSERT INTO "+table+" (week, "+col+", clicks, impressions, avg_click_position, avg_impression_position)",
		[]string{"date", "", "", "", "", ""},
		"ON CONFLICT (week, "+col+") DO UPDATE SET clicks = EXCLUDED.clicks, impressions = EXCLUDED.impressions, avg_click_position = EXCLUDED.avg_click_position, avg_impression_position = EXCLUDED.avg_impression_position",
		data)
}

// tooLong reports a key over maxKeyBytes: the btree index behind the primary
// keys refuses rows of about 2.7 KB, so one long spam URL would fail a whole
// batch. Such rows are skipped.
func tooLong(s string) bool { return len(s) > maxKeyBytes }

// replaceCrawlIssues swaps the stored snapshot for rows: it upserts each row
// stamped with now, then deletes the rows this run did not see. A failed
// insert therefore keeps the old data. Call it only after a successful,
// complete fetch: an empty rows then means Bing lists no issues.
func replaceCrawlIssues(rows []crawlRow, now time.Time) error {
	rows = dedupe(rows, func(r crawlRow) string { return r.URL })
	data := make([][]any, 0, len(rows))
	for _, r := range rows {
		if !tooLong(r.URL) {
			data = append(data, []any{clean(r.URL), r.HTTPCode, r.Issues, r.InLinks, now.UTC()})
		}
	}
	err := batchInsert("INSERT INTO crawl_issues (url, http_code, issues, in_links, seen_at)",
		[]string{"", "", "", "", "timestamptz"},
		"ON CONFLICT (url) DO UPDATE SET http_code = EXCLUDED.http_code, issues = EXCLUDED.issues, in_links = EXCLUDED.in_links, seen_at = EXCLUDED.seen_at", data)
	if err != nil {
		return err
	}
	return dbExec("DELETE FROM crawl_issues WHERE seen_at < $1::timestamptz", now.UTC())
}

// replaceFeeds swaps the stored sitemap snapshot, as replaceCrawlIssues does.
func replaceFeeds(rows []feedRow, now time.Time) error {
	rows = dedupe(rows, func(r feedRow) string { return r.URL })
	data := make([][]any, 0, len(rows))
	for _, r := range rows {
		if !tooLong(r.URL) {
			data = append(data, []any{clean(r.URL), clean(r.Type), clean(r.Status), r.SubmittedAt, r.LastCrawledAt, r.URLCount, now.UTC()})
		}
	}
	err := batchInsert("INSERT INTO feeds (url, type, status, submitted_at, last_crawled_at, url_count, seen_at)",
		[]string{"", "", "", "timestamptz", "timestamptz", "", "timestamptz"},
		"ON CONFLICT (url) DO UPDATE SET type = EXCLUDED.type, status = EXCLUDED.status, submitted_at = EXCLUDED.submitted_at, last_crawled_at = EXCLUDED.last_crawled_at, url_count = EXCLUDED.url_count, seen_at = EXCLUDED.seen_at", data)
	if err != nil {
		return err
	}
	return dbExec("DELETE FROM feeds WHERE seen_at < $1::timestamptz", now.UTC())
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	if r := []rune(err.Error()); len(r) > maxErrorText {
		return string(r[:maxErrorText])
	}
	return err.Error()
}

func recordRun(started, finished time.Time, runErr error) error {
	return dbExec("INSERT INTO import_runs (started_at, finished_at, ok, error) VALUES ($1::timestamptz, $2::timestamptz, $3, $4)",
		started.UTC(), finished.UTC(), runErr == nil, errorText(runErr))
}

func recordSubmission(feedURL string, at time.Time, subErr error) error {
	return dbExec("INSERT INTO sitemap_submissions (submitted_at, feed_url, error) VALUES ($1::timestamptz, $2, $3)",
		at.UTC(), clean(feedURL), errorText(subErr))
}

// addMonths moves t by n months in UTC and clamps the day to the target
// month's length (31 Mar minus 16 months is 30 Nov, not 1 Dec).
func addMonths(t time.Time, n int) time.Time {
	t = t.UTC()
	first := time.Date(t.Year(), t.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(t.Day(), last), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// pruneBefore deletes what is past its retention, relative to now in UTC. It
// tries every table and returns what failed.
func pruneBefore(now time.Time) error {
	day := addMonths(now, -retentionStats).Format(dateFmt)
	logCut := now.UTC().AddDate(0, 0, -retentionLogs)
	var errs []error
	for _, q := range []string{
		"DELETE FROM traffic_daily WHERE day < $1::date",
		"DELETE FROM query_stats WHERE week < $1::date",
		"DELETE FROM page_stats WHERE week < $1::date",
	} {
		errs = append(errs, dbExec(q, day))
	}
	for _, q := range []string{
		"DELETE FROM import_runs WHERE started_at < $1::timestamptz",
		"DELETE FROM sitemap_submissions WHERE submitted_at < $1::timestamptz",
	} {
		errs = append(errs, dbExec(q, logCut))
	}
	return errors.Join(errs...)
}

// --- overview ----------------------------------------------------------------------------

// topRow is a query or page summed over the last four weeks.
type topRow struct {
	Key         string  `json:"key"`
	Clicks      int     `json:"clicks"`
	Impressions int     `json:"impressions"`
	Position    float64 `json:"position"`
}

type crawlOut struct {
	URL      string `json:"url"`
	HTTPCode int    `json:"http_code"`
	Issues   int    `json:"issues"`
	InLinks  int    `json:"in_links"`
}

type feedOut struct {
	URL           string     `json:"url"`
	Type          string     `json:"type"`
	Status        string     `json:"status"`
	SubmittedAt   *time.Time `json:"submitted_at"`
	LastCrawledAt *time.Time `json:"last_crawled_at"`
	URLCount      int        `json:"url_count"`
}

type runInfo struct {
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
	Error string    `json:"error"`
}

type submissionInfo struct {
	At      time.Time `json:"at"`
	FeedURL string    `json:"feed_url"`
	Error   string    `json:"error"`
}

// stored is everything the overview reads from the database.
type stored struct {
	Traffic        []trafficRow    `json:"traffic"`
	TopQueries     []topRow        `json:"top_queries"`
	TopPages       []topRow        `json:"top_pages"`
	CrawlIssues    []crawlOut      `json:"crawl_issues"`
	Feeds          []feedOut       `json:"feeds"`
	LastRun        *runInfo        `json:"last_import"`
	LastSubmission *submissionInfo `json:"last_submission"`
}

// topSQL builds the top list over the four newest weeks of a stats table.
// Position is weighted by impressions.
func topSQL(table, col string) string {
	return `(SELECT coalesce(jsonb_agg(t ORDER BY t.clicks DESC, t.impressions DESC, t.key), '[]'::jsonb) FROM (` +
		`SELECT ` + col + ` AS key, sum(clicks)::int AS clicks, sum(impressions)::int AS impressions, ` +
		`coalesce(sum(avg_impression_position * impressions) / nullif(sum(impressions), 0), 0) AS position ` +
		`FROM ` + table + ` WHERE week IN (SELECT DISTINCT week FROM ` + table + ` ORDER BY week DESC LIMIT 4) ` +
		`GROUP BY ` + col + ` ORDER BY sum(clicks) DESC, sum(impressions) DESC, ` + col + ` LIMIT 20) t)`
}

var qOverview = `SELECT jsonb_build_object(` +
	`'traffic', (SELECT coalesce(jsonb_agg(jsonb_build_object('day', to_char(day, 'YYYY-MM-DD'), 'clicks', clicks, 'impressions', impressions) ORDER BY day DESC), '[]'::jsonb) ` +
	`FROM (SELECT day, clicks, impressions FROM traffic_daily ORDER BY day DESC LIMIT 28) d), ` +
	`'top_queries', ` + topSQL("query_stats", "query") + `, ` +
	`'top_pages', ` + topSQL("page_stats", "page") + `, ` +
	`'crawl_issues', (SELECT coalesce(jsonb_agg(c ORDER BY c.in_links DESC, c.url), '[]'::jsonb) FROM (SELECT url, http_code, issues, in_links FROM crawl_issues ORDER BY in_links DESC, url LIMIT 100) c), ` +
	`'feeds', (SELECT coalesce(jsonb_agg(f ORDER BY f.url), '[]'::jsonb) FROM (SELECT url, type, status, submitted_at, last_crawled_at, url_count FROM feeds ORDER BY url) f), ` +
	`'last_import', (SELECT jsonb_build_object('at', started_at, 'ok', ok, 'error', error) FROM import_runs ORDER BY started_at DESC, id DESC LIMIT 1), ` +
	`'last_submission', (SELECT jsonb_build_object('at', submitted_at, 'feed_url', feed_url, 'error', error) FROM sitemap_submissions ORDER BY submitted_at DESC, id DESC LIMIT 1))`

func loadStored() (stored, error) {
	var s stored
	if err := dbJSON(qOverview, &s); err != nil {
		return s, err
	}
	if s.Traffic == nil {
		s.Traffic = []trafficRow{}
	}
	if s.TopQueries == nil {
		s.TopQueries = []topRow{}
	}
	if s.TopPages == nil {
		s.TopPages = []topRow{}
	}
	if s.CrawlIssues == nil {
		s.CrawlIssues = []crawlOut{}
	}
	if s.Feeds == nil {
		s.Feeds = []feedOut{}
	}
	return s, nil
}
