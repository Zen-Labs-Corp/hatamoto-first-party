// Command pagespeed is a Hatamoto plugin. PageSpeed and Core Web Vitals: tracks field data from the Chrome UX Report and Lighthouse lab scores for your key pages, with daily trends.
//
// The logic is plain Go in this package; the //go:wasmexport lines live in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnHTTP(serve)
	guest.OnCron("measure", func(sdk.CronEvent) error { return runCron() })
}
