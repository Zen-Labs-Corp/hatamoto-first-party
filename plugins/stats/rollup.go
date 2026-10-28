package main

import (
	"time"

	"hatamoto.dev/sdk"
)

const (
	dateFmt         = "2006-01-02"
	retentionMonths = 13 // daily rows are kept this long, then folded per month
	monthsPerRun    = 3  // bounds one cron run
)

// cutoffMonth is the first day of the UTC month 13 months before the current
// one. Daily rows older than it are folded into monthly rows.
func cutoffMonth(today time.Time) string {
	t := today.UTC()
	return time.Date(t.Year(), t.Month()-retentionMonths, 1, 0, 0, 0, 0, time.UTC).Format(dateFmt)
}

// qOldestMonth returns the month (first day) of the oldest daily row below the
// cutoff, or null when there is none.
const qOldestMonth = `SELECT jsonb_build_object('month', (SELECT to_char(date_trunc('month', min(day)), 'YYYY-MM-DD') FROM installs_daily WHERE day < $1::date))`

// foldMonth moves one whole month of daily rows into installs_monthly in a
// single statement: the rows are deleted and inserted together, so a failed run
// loses nothing and a rerun finds nothing to move. The monthly row keeps the
// version and channel of the last day the install was seen that month.
const foldMonth = `WITH moved AS (DELETE FROM installs_daily WHERE day >= $1::date AND day < ($1::date + interval '1 month') RETURNING install, day, version, channel) INSERT INTO installs_monthly (install, month, version, channel) SELECT DISTINCT ON (install) install, $1::date, version, channel FROM moved ORDER BY install, day DESC ON CONFLICT (install, month) DO UPDATE SET version = EXCLUDED.version, channel = EXCLUDED.channel`

// rollupOnce folds up to monthsPerRun months, oldest first, and returns how
// many it folded.
func rollupOnce(today time.Time) (int, error) {
	cutoff := cutoffMonth(today)
	for n := 0; n < monthsPerRun; n++ {
		var oldest struct{ Month *string }
		if err := dbJSON(qOldestMonth, &oldest, cutoff); err != nil {
			return n, err
		}
		if oldest.Month == nil {
			return n, nil
		}
		if err := dbExec(foldMonth, *oldest.Month); err != nil {
			return n, err
		}
	}
	return monthsPerRun, nil
}

func onRollup(sdk.CronEvent) error {
	_, err := rollupOnce(now())
	return err
}
