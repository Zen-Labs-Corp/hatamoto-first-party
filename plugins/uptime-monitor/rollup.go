package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	rollupKey = "rollup_day" // the last UTC day aggregated, YYYY-MM-DD
	dayLayout = "2006-01-02"

	checkKeepDays   = 30
	historyKeepMths = 13
	// maxRollupDays bounds a catch-up after a long pause: older checks are
	// gone by retention anyway.
	maxRollupDays = checkKeepDays + 1
)

// rollupDay aggregates the checks of one UTC day per monitor. Configuration
// rows (ok IS NULL) are left out. Running it again for a day replaces the row,
// so a day can be aggregated while it is still going on.
const rollupDay = `INSERT INTO daily (monitor_id, day, checks, up_checks, uptime_pct, avg_ms)
	SELECT monitor_id, $1::date, count(*), count(*) FILTER (WHERE ok),
	  round(100.0 * count(*) FILTER (WHERE ok) / count(*), 3), round(avg(ms) FILTER (WHERE ok))::integer
	FROM checks WHERE at >= $2::timestamptz AND at < $3::timestamptz AND ok IS NOT NULL
	GROUP BY monitor_id
	ON CONFLICT (monitor_id, day) DO UPDATE SET checks = EXCLUDED.checks, up_checks = EXCLUDED.up_checks,
	  uptime_pct = EXCLUDED.uptime_pct, avg_ms = EXCLUDED.avg_ms`

// runRollup is the "rollup" cron job. It never returns an error: a failed step
// is logged and the next run repeats it.
func runRollup(sdk.CronEvent) error {
	rollup()
	return nil
}

func rollup() {
	at := now().UTC()
	today := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	first := today.AddDate(0, 0, -1)
	var last string
	if _, err := kvGet(rollupKey, &last); err != nil {
		logf("warn", "uptime-monitor: rollup position unreadable code=%s", hostErrCode(err))
	} else if d, err := time.Parse(dayLayout, last); err == nil {
		// The stored day was aggregated while it was still going on: repeat it.
		first = d
	}
	if floor := today.AddDate(0, 0, -maxRollupDays); first.Before(floor) {
		first = floor
	}
	if first.After(today) {
		first = today
	}
	for d := first; !d.After(today); d = d.AddDate(0, 0, 1) {
		if err := dbExec(rollupDay, d.Format(dayLayout), ts(d), ts(d.AddDate(0, 0, 1))); err != nil {
			logf("warn", "uptime-monitor: rollup failed code=%s", hostErrCode(err))
			return
		}
	}
	if err := kvSet(rollupKey, today.Format(dayLayout)); err != nil {
		logf("warn", "uptime-monitor: rollup position not saved code=%s", hostErrCode(err))
	}
	retain(at)
}

// retain deletes what is past its keeping time and the certificate rows of
// hosts no monitor uses.
func retain(at time.Time) {
	checksCut := at.AddDate(0, 0, -checkKeepDays)
	historyCut := at.AddDate(0, -historyKeepMths, 0)
	steps := []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM checks WHERE at < $1::timestamptz`, []any{ts(checksCut)}},
		{`DELETE FROM incidents WHERE ended_at IS NOT NULL AND ended_at < $1::timestamptz`, []any{ts(historyCut)}},
		{`DELETE FROM daily WHERE day < $1::date`, []any{historyCut.Format(dayLayout)}},
	}
	for _, s := range steps {
		if err := dbExec(s.sql, s.args...); err != nil {
			logf("warn", "uptime-monitor: retention failed code=%s", hostErrCode(err))
			return
		}
	}
	monitors, err := listMonitors()
	if err != nil {
		logf("warn", "uptime-monitor: monitors unavailable code=%s", hostErrCode(err))
		return
	}
	hosts := []string{}
	for _, m := range monitors {
		if u, err := url.Parse(m.URL); err == nil && u.Hostname() != "" {
			hosts = append(hosts, strings.ToLower(u.Hostname()))
		}
	}
	list, _ := json.Marshal(hosts)
	if err := dbExec(`DELETE FROM certs WHERE NOT (host = ANY(ARRAY(SELECT jsonb_array_elements_text($1::jsonb))))`, string(list)); err != nil {
		logf("warn", "uptime-monitor: certificate cleanup failed code=%s", hostErrCode(err))
	}
}
