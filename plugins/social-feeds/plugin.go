// Command social-feeds is a Hatamoto plugin. It keeps a copy of the latest
// Instagram posts and YouTube videos and serves them to the page builder as
// data sources.
//
// The logic lives in plain Go files and the //go:wasmexport lines in
// exports_wasip1.go, so everything but the exports builds and tests on the
// host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnFilter(sdk.BuilderData, filterData)
	// A source failing is recorded, never returned: the cron run stays green.
	guest.OnCron("refresh", func(sdk.CronEvent) error {
		runRefresh(refreshBudget)
		return nil
	})
	guest.OnCron("token", func(sdk.CronEvent) error {
		runTokenRefresh()
		return nil
	})
}
