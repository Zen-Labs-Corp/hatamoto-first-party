package main

import "hatamoto.dev/sdk"

// gcCron deletes expired sessions. One idempotent statement; an error is
// returned so the host logs the failed run, and carries no data.
func gcCron(sdk.CronEvent) error {
	return dbExec(`DELETE FROM sessions WHERE expires_at < now()`)
}
