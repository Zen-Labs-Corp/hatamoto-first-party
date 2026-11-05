// Command uptime-monitor is a Hatamoto plugin. Checks your site and other web
// addresses every few minutes, emails you when one goes down or comes back,
// warns before an SSL certificate expires and can ping an outside heartbeat.
//
// The logic lives in plain Go files so everything but the //go:wasmexport
// lines in exports_wasip1.go builds and tests on the host.
package main

import "hatamoto.dev/sdk/guest"

func init() {
	guest.OnCron("check", runCheckTick)
	guest.OnCron("certs", runCertsTick)
	guest.OnCron("rollup", runRollup)
	guest.OnHTTP(handleRoute)
}
