// Command pagespeed is a Hatamoto plugin. PageSpeed and Core Web Vitals: tracks field data from the Chrome UX Report and Lighthouse lab scores for your key pages, with daily trends.
//
// The logic is plain Go in this package; the //go:wasmexport lines live in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main

import (
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnHTTP(serve)
	guest.OnCron("measure", func(sdk.CronEvent) error { return runCron() })
}

// serve answers the admin routes.
func serve(sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	return sdk.HTTPResponse{Status: http.StatusNotFound, Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"error":"not found"}`}, nil
}

// runCron measures the tracked pages within the call's time budget.
func runCron() error { return nil }
