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
// are deleted, the rest are upserted. $21 is the plugin list as JSON text and
// $22 the optional site address (NULL when the report has none; a same-day
// report without one keeps the host an earlier report stored). $23..$30 are the
// feature flags in order comments_on, static_mode, sso_on, locales_bucket,
// forms_in_use, offsite_backups_on, csp_enforced, wp_imported: NULL when not
// sent, and a same-day report without one keeps the value stored earlier.
const upsertDetailed = "WITH i AS (" +
	"INSERT INTO installs_daily (install, day, version, channel) VALUES ($1, $2::date, $3, $4) " +
	"ON CONFLICT (install, day) DO UPDATE SET version = EXCLUDED.version, channel = EXCLUDED.channel), " +
	"d AS (" +
	"INSERT INTO detail_daily (install, day, core_version, os, arch, postgres, age_months, worker, theme_name, theme_version, " +
	"other_plugins, since_hours, panics, http_5xx, render_errors, other_plugin_errors, update_failed, update_rolled_back, migration_failed, host, comments_on, static_mode, sso_on, locales_bucket, forms_in_use, offsite_backups_on, csp_enforced, wp_imported) " +
	"VALUES ($1, $2::date, $3, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $22, $23, $24, $25, $26, $27, $28, $29, $30) " +
	"ON CONFLICT (install, day) DO UPDATE SET core_version = EXCLUDED.core_version, os = EXCLUDED.os, arch = EXCLUDED.arch, " +
	"postgres = EXCLUDED.postgres, age_months = EXCLUDED.age_months, worker = EXCLUDED.worker, theme_name = EXCLUDED.theme_name, " +
	"theme_version = EXCLUDED.theme_version, other_plugins = EXCLUDED.other_plugins, since_hours = EXCLUDED.since_hours, " +
	"panics = EXCLUDED.panics, http_5xx = EXCLUDED.http_5xx, render_errors = EXCLUDED.render_errors, " +
	"other_plugin_errors = EXCLUDED.other_plugin_errors, update_failed = EXCLUDED.update_failed, " +
	"update_rolled_back = EXCLUDED.update_rolled_back, migration_failed = EXCLUDED.migration_failed, " +
	"host = COALESCE(EXCLUDED.host, detail_daily.host), " +
	"comments_on = COALESCE(EXCLUDED.comments_on, detail_daily.comments_on), " +
	"static_mode = COALESCE(EXCLUDED.static_mode, detail_daily.static_mode), " +
	"sso_on = COALESCE(EXCLUDED.sso_on, detail_daily.sso_on), " +
	"locales_bucket = COALESCE(EXCLUDED.locales_bucket, detail_daily.locales_bucket), " +
	"forms_in_use = COALESCE(EXCLUDED.forms_in_use, detail_daily.forms_in_use), " +
	"offsite_backups_on = COALESCE(EXCLUDED.offsite_backups_on, detail_daily.offsite_backups_on), " +
	"csp_enforced = COALESCE(EXCLUDED.csp_enforced, detail_daily.csp_enforced), " +
	"wp_imported = COALESCE(EXCLUDED.wp_imported, detail_daily.wp_imported)), " +
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
	f := p.Flags
	var host any // nil stores NULL, never ""
	if p.HostSet {
		host = p.Host
	}
	return dbExec(upsertDetailed, p.Install, day, p.Version, p.Channel, p.OS, p.Arch, p.Postgres, p.AgeMonths, p.Worker,
		p.ThemeName, p.ThemeVersion, p.OtherPlugins, h.SinceHours, h.Panics, h.HTTP5xx, h.RenderErrors, h.OtherErrors,
		h.UpdateFailed, h.UpdateRolledBack, h.MigrationFailed, string(list), host,
		optBool(f.CommentsOn), optBool(f.StaticMode), optBool(f.SSOOn), optString(f.LocalesBucket), optBool(f.FormsInUse),
		optBool(f.OffsiteBackupsOn), optBool(f.CSPEnforced), optBool(f.WPImported))
}

// optBool and optString turn an unsent flag into an untyped nil (NULL, never
// false or "").
func optBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func optString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
