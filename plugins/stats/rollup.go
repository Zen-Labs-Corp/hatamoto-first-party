package main

import (
	"errors"
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

// oldestMonthQuery returns the month (first day) of the oldest row of a daily
// table below the cutoff, or null when there is none.
func oldestMonthQuery(table string) string {
	return `SELECT jsonb_build_object('month', (SELECT to_char(date_trunc('month', min(day)), 'YYYY-MM-DD') FROM ` + table + ` WHERE day < $1::date))`
}

// Each daily table is folded by its own statement and tracks its own oldest
// month, so a month too big for one statement in the cron timeout in
// detail_daily or plugins_daily cannot keep installs_daily from folding, that
// month or any later one. Each statement deletes and inserts together, so a
// failed one loses nothing and the next run retries the same month. A monthly row keeps what was last seen that month (version, channel,
// environment, theme, plugin versions); health counts are summed and
// report_days counts the days reported.
const foldInstalls = `WITH moved AS (DELETE FROM installs_daily WHERE day >= $1::date AND day < ($1::date + interval '1 month') RETURNING install, day, version, channel) ` +
	`INSERT INTO installs_monthly (install, month, version, channel) SELECT DISTINCT ON (install) install, $1::date, version, channel FROM moved ORDER BY install, day DESC ON CONFLICT (install, month) DO UPDATE SET version = EXCLUDED.version, channel = EXCLUDED.channel`

const foldDetail = `WITH md AS (DELETE FROM detail_daily WHERE day >= $1::date AND day < ($1::date + interval '1 month') RETURNING *) ` +
	`INSERT INTO detail_monthly (install, month, core_version, os, arch, postgres, age_months, worker, theme_name, theme_version, other_plugins, report_days, panics, http_5xx, render_errors, other_plugin_errors, update_failed, update_rolled_back, migration_failed) ` +
	`SELECT DISTINCT ON (install) install, $1::date, core_version, os, arch, postgres, age_months, worker, theme_name, theme_version, other_plugins, ` +
	`count(*) OVER w, sum(panics) OVER w, sum(http_5xx) OVER w, sum(render_errors) OVER w, sum(other_plugin_errors) OVER w, sum(update_failed) OVER w, sum(update_rolled_back) OVER w, sum(migration_failed) OVER w ` +
	`FROM md WINDOW w AS (PARTITION BY install) ORDER BY install, day DESC ` +
	`ON CONFLICT (install, month) DO UPDATE SET core_version = EXCLUDED.core_version, os = EXCLUDED.os, arch = EXCLUDED.arch, postgres = EXCLUDED.postgres, age_months = EXCLUDED.age_months, worker = EXCLUDED.worker, ` +
	`theme_name = EXCLUDED.theme_name, theme_version = EXCLUDED.theme_version, other_plugins = EXCLUDED.other_plugins, report_days = EXCLUDED.report_days, panics = EXCLUDED.panics, http_5xx = EXCLUDED.http_5xx, ` +
	`render_errors = EXCLUDED.render_errors, other_plugin_errors = EXCLUDED.other_plugin_errors, update_failed = EXCLUDED.update_failed, update_rolled_back = EXCLUDED.update_rolled_back, migration_failed = EXCLUDED.migration_failed`

const foldPlugins = `WITH mp AS (DELETE FROM plugins_daily WHERE day >= $1::date AND day < ($1::date + interval '1 month') RETURNING *) ` +
	`INSERT INTO plugins_monthly (install, month, name, version, enabled, errors) ` +
	`SELECT DISTINCT ON (install, name) install, $1::date, name, version, enabled, sum(errors) OVER (PARTITION BY install, name) FROM mp ORDER BY install, name, day DESC ` +
	`ON CONFLICT (install, month, name) DO UPDATE SET version = EXCLUDED.version, enabled = EXCLUDED.enabled, errors = EXCLUDED.errors`

// foldSteps run in this order, the Basic table first; a failing step does not
// stop the ones after it.
var foldSteps = []struct{ table, fold string }{
	{"installs_daily", foldInstalls},
	{"detail_daily", foldDetail},
	{"plugins_daily", foldPlugins},
}

// rollupOnce folds up to monthsPerRun months per table, oldest first, and
// returns the most months any table folded. Errors of the steps are joined.
func rollupOnce(today time.Time) (int, error) {
	cutoff := cutoffMonth(today)
	most := 0
	var errs []error
	for _, step := range foldSteps {
		n, err := foldTable(step.table, step.fold, cutoff)
		if err != nil {
			errs = append(errs, err)
		}
		most = max(most, n)
	}
	return most, errors.Join(errs...)
}

func foldTable(table, fold, cutoff string) (int, error) {
	for n := 0; n < monthsPerRun; n++ {
		var oldest struct{ Month *string }
		if err := dbJSON(oldestMonthQuery(table), &oldest, cutoff); err != nil {
			return n, err
		}
		if oldest.Month == nil {
			return n, nil
		}
		if err := dbExec(fold, *oldest.Month); err != nil {
			return n, err
		}
	}
	return monthsPerRun, nil
}

func onRollup(sdk.CronEvent) error {
	_, err := rollupOnce(now())
	return err
}
