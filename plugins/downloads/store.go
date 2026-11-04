package main

import "time"

// countSQL adds one download to the (file, day) row in a single statement.
const countSQL = "INSERT INTO downloads_daily (media_id, day, count) VALUES ($1, $2::date, 1) ON CONFLICT (media_id, day) DO UPDATE SET count = downloads_daily.count + 1"

// now is replaced in tests.
var now = time.Now

// utcDay is today's UTC date, computed here because the database time zone is
// not known.
func utcDay() string { return now().UTC().Format("2006-01-02") }

func countDownload(id string) error { return dbExec(countSQL, id, utcDay()) }
