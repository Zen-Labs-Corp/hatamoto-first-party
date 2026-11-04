package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	// A failed refresh is already stored and logged; the cron never reports it.
	guest.OnCron("refresh", func(sdk.CronEvent) error {
		_ = refreshAll(false)
		return nil
	})
}
