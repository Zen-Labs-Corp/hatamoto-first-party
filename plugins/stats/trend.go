package main

import (
	"time"
)

const (
	trendLookbackDays = 30
	trendWeekDays     = 7
	trendMonths       = 13
)

func day(t time.Time) string { return t.Format(dateFmt) }

// trendPlan returns the week-ending days of the 13-month trend, ascending: they
// end on today and step back 7 days while the window and the 30-day look-back
// lie inside daily retention (rows before cutoffMonth are folded and no longer
// count per day).
func trendPlan(today time.Time) []string {
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	cutoff, _ := time.Parse(dateFmt, cutoffMonth(today))
	windowStart := today.AddDate(0, -trendMonths, 0)
	var weeks []string
	for w := today; !w.AddDate(0, 0, -trendLookbackDays).Before(cutoff) && w.After(windowStart); w = w.AddDate(0, 0, -trendWeekDays) {
		weeks = append([]string{day(w)}, weeks...)
	}
	return weeks
}

// qWeekly counts, for each week-ending day (from $1, $2 more weeks, every 7
// days), the installs seen in the 30 days ending that day. Weeks with no rows
// count 0. One pass over the daily rows: each row is mapped arithmetically to
// the week-ends whose window holds its day (lo..hi, clipped to 0..$2) as bits
// of a bigint, the bits are OR-ed per install, and a week counts the installs
// with its bit set. So $2 must stay below 62; trendPlan never gives more than 58.
const qWeekly = `SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.date), '[]'::jsonb) FROM (SELECT to_char($1::date + g.k * 7, 'YYYY-MM-DD') AS date, (count(*) FILTER (WHERE m.mask & (1::bigint << g.k) <> 0))::int AS active FROM generate_series(0, $2::int) g(k) LEFT JOIN (SELECT bit_or(CASE WHEN d.lo <= d.hi THEN (1::bigint << (d.hi + 1)) - (1::bigint << d.lo) ELSE 0 END) AS mask FROM (SELECT install, greatest((day - $1::date + 41) / 7 - 5, 0) AS lo, least((day - $1::date + 29) / 7, $2::int) AS hi FROM installs_daily WHERE day >= $1::date - 29 AND day <= $1::date + $2::int * 7) d GROUP BY d.install) m ON true GROUP BY g.k) t`

type trendRow struct {
	Date   string `json:"date"`
	Active int    `json:"active"`
}

// buildTrend returns the weekly points, oldest first.
func buildTrend(today time.Time) ([]point, error) {
	weeks := trendPlan(today)
	out := []point{}
	var rows []trendRow
	if err := dbJSON(qWeekly, &rows, weeks[0], len(weeks)-1); err != nil {
		return nil, err
	}
	for _, r := range rows {
		out = append(out, point{Date: r.Date, Label: r.Date, Active: r.Active, Kind: "week"})
	}
	return out, nil
}
