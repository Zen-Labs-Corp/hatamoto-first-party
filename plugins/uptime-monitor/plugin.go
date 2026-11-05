// Command uptime-monitor is a Hatamoto plugin. Checks your site and other web
// addresses every few minutes, emails you when one goes down or comes back,
// warns before an SSL certificate expires and can ping an outside heartbeat.
//
// The logic lives in plain Go files so everything but the //go:wasmexport
// lines in exports_wasip1.go builds and tests on the host.
package main

import (
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnCron("check", runCheckTick)
	guest.OnCron("certs", runCertsTick)
	guest.OnCron("rollup", runRollup)
	guest.OnHTTP(handleRoute)
}

// Stub, replaced by the real routes.
func handleRoute(sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	return sdk.HTTPResponse{
		Status:  http.StatusNotImplemented,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"error":"not implemented"}`,
	}, nil
}
