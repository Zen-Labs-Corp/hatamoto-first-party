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
// cutoff in any of the daily tables, or null when there is none.
const qOldestMonth = `SELECT jsonb_build_object('month', (SELECT to_char(date_trunc('month', min(d)), 'YYYY-MM-DD') FROM (SELECT min(day) AS d FROM installs_daily WHERE day < $1::date UNION ALL SELECT min(day) FROM detail_daily WHERE day < $1::date UNION ALL SELECT min(day) FROM plugins_daily WHERE day < $1::date) t))`

// foldMonth moves one whole month of daily rows into the monthly tables in a
// single statement: the rows are deleted and inserted together, so a failed run
// loses nothing and a rerun finds nothing to move. A monthly row keeps what was
// last seen that month (version, channel, environment, theme, plugin versions);
// health counts are summed and report_days counts the days reported.
const foldMonth = `WITH moved AS (DELETE FROM installs_daily WHERE day >= $1::date AND day < ($1::date + interval '1 month') RETURNING install, day, version, channel), ` +
	`md AS (DELETE FROM detail_daily WHERE day >= $1::date AND day < ($1::date + interval '1 month') RETURNING *), ` +
	`mp AS (DELETE FROM plugins_daily WHERE day >= $1::date AND day < ($1::date + interval '1 month') RETURNING *), ` +
	`a AS (INSERT INTO installs_monthly (install, month, version, channel) SELECT DISTINCT ON (install) install, $1::date, version, channel FROM moved ORDER BY install, day DESC ON CONFLICT (install, month) DO UPDATE SET version = EXCLUDED.version, channel = EXCLUDED.channel), ` +
	`b AS (INSERT INTO detail_monthly (install, month, core_version, os, arch, postgres, age_months, worker, theme_name, theme_version, other_plugins, report_days, panics, http_5xx, render_errors, other_plugin_errors, update_failed, update_rolled_back, migration_failed) ` +
	`SELECT DISTINCT ON (install) install, $1::date, core_version, os, arch, postgres, age_months, worker, theme_name, theme_version, other_plugins, ` +
	`count(*) OVER w, sum(panics) OVER w, sum(http_5xx) OVER w, sum(render_errors) OVER w, sum(other_plugin_errors) OVER w, sum(update_failed) OVER w, sum(update_rolled_back) OVER w, sum(migration_failed) OVER w ` +
	`FROM md WINDOW w AS (PARTITION BY install) ORDER BY install, day DESC ` +
	`ON CONFLICT (install, month) DO UPDATE SET core_version = EXCLUDED.core_version, os = EXCLUDED.os, arch = EXCLUDED.arch, postgres = EXCLUDED.postgres, age_months = EXCLUDED.age_months, worker = EXCLUDED.worker, ` +
	`theme_name = EXCLUDED.theme_name, theme_version = EXCLUDED.theme_version, other_plugins = EXCLUDED.other_plugins, report_days = EXCLUDED.report_days, panics = EXCLUDED.panics, http_5xx = EXCLUDED.http_5xx, ` +
	`render_errors = EXCLUDED.render_errors, other_plugin_errors = EXCLUDED.other_plugin_errors, update_failed = EXCLUDED.update_failed, update_rolled_back = EXCLUDED.update_rolled_back, migration_failed = EXCLUDED.migration_failed) ` +
	`INSERT INTO plugins_monthly (install, month, name, version, enabled, errors) ` +
	`SELECT DISTINCT ON (install, name) install, $1::date, name, version, enabled, sum(errors) OVER (PARTITION BY install, name) FROM mp ORDER BY install, name, day DESC ` +
	`ON CONFLICT (install, month, name) DO UPDATE SET version = EXCLUDED.version, enabled = EXCLUDED.enabled, errors = EXCLUDED.errors`

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
