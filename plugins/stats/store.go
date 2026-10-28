package main

import "time"

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
