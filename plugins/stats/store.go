package main

import (
	"encoding/json"
	"time"
)

// ping is what core sends, and all the plugin ever stores besides the day.
type ping struct {
	Install, Version, Channel string
}

// upsertDaily keeps one row per (install, day); a later ping the same day wins.
const upsertDaily = "INSERT INTO installs_daily (install, day, version, channel) VALUES ($1, $2::date, $3, $4) ON CONFLICT (install, day) DO UPDATE SET version = EXCLUDED.version, channel = EXCLUDED.channel"

// now is replaced in tests.
var now = time.Now

// utcDay is today's UTC date, computed here because the database time zone is
// not known.
func utcDay() string { return now().UTC().Format("2006-01-02") }

func recordPing(p ping, day string) error {
	return dbExec(upsertDaily, p.Install, day, p.Version, p.Channel)
}

// upsertDetailed writes one Detailed report in a single statement: the same
// installs_daily row a Basic ping writes (so Active, Versions and Trend count
// it), the install's detail_daily row, and its plugins_daily rows. A later
// report the same day replaces that day's plugin rows: names no longer listed
// are deleted, the rest are upserted. $21 is the plugin list as JSON text.
const upsertDetailed = "WITH i AS (" +
	"INSERT INTO installs_daily (install, day, version, channel) VALUES ($1, $2::date, $3, $4) " +
	"ON CONFLICT (install, day) DO UPDATE SET version = EXCLUDED.version, channel = EXCLUDED.channel), " +
	"d AS (" +
	"INSERT INTO detail_daily (install, day, core_version, os, arch, postgres, age_months, worker, theme_name, theme_version, " +
	"other_plugins, since_hours, panics, http_5xx, render_errors, other_plugin_errors, update_failed, update_rolled_back, migration_failed) " +
	"VALUES ($1, $2::date, $3, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20) " +
	"ON CONFLICT (install, day) DO UPDATE SET core_version = EXCLUDED.core_version, os = EXCLUDED.os, arch = EXCLUDED.arch, " +
	"postgres = EXCLUDED.postgres, age_months = EXCLUDED.age_months, worker = EXCLUDED.worker, theme_name = EXCLUDED.theme_name, " +
	"theme_version = EXCLUDED.theme_version, other_plugins = EXCLUDED.other_plugins, since_hours = EXCLUDED.since_hours, " +
	"panics = EXCLUDED.panics, http_5xx = EXCLUDED.http_5xx, render_errors = EXCLUDED.render_errors, " +
	"other_plugin_errors = EXCLUDED.other_plugin_errors, update_failed = EXCLUDED.update_failed, " +
	"update_rolled_back = EXCLUDED.update_rolled_back, migration_failed = EXCLUDED.migration_failed), " +
	"x AS (" +
	"DELETE FROM plugins_daily WHERE install = $1 AND day = $2::date " +
	"AND name NOT IN (SELECT name FROM jsonb_to_recordset($21::jsonb) AS r(name text))) " +
	"INSERT INTO plugins_daily (install, day, name, version, enabled, errors) " +
	"SELECT $1::text, $2::date, name, version, enabled, errors " +
	"FROM jsonb_to_recordset($21::jsonb) AS r(name text, version text, enabled boolean, errors int) " +
	"ON CONFLICT (install, day, name) DO UPDATE SET version = EXCLUDED.version, enabled = EXCLUDED.enabled, errors = EXCLUDED.errors"

// recordDetailed stores a Detailed report: one db.exec, atomic.
func recordDetailed(p detailedPing, day string) error {
	plugins := p.Plugins
	if plugins == nil {
		plugins = []pluginRow{} // marshal as [], never null
	}
	list, err := json.Marshal(plugins)
	if err != nil {
		return err
	}
	h := p.Health
	return dbExec(upsertDetailed, p.Install, day, p.Version, p.Channel, p.OS, p.Arch, p.Postgres, p.AgeMonths, p.Worker,
		p.ThemeName, p.ThemeVersion, p.OtherPlugins, h.SinceHours, h.Panics, h.HTTP5xx, h.RenderErrors, h.OtherErrors,
		h.UpdateFailed, h.UpdateRolledBack, h.MigrationFailed, string(list))
}
