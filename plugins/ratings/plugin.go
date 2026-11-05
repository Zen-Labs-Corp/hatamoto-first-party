// Command ratings is a Hatamoto plugin. Star ratings for posts and pages: visitors rate with a plain form, the average shows on the page and you see totals per page.
//
// The logic lives in plain Go files; the //go:wasmexport lines are in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main

import (
	"encoding/json"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnHTTP(serve)
	guest.OnFunc("summary", funcSummary)
	guest.OnFunc("enabled", funcEnabled)
	guest.OnAction(sdk.EntityDeleted, onEntityDeleted)
}

// Placeholders, replaced by the func files.

func funcSummary([]json.RawMessage) (string, error) { return "", nil }

func funcEnabled([]json.RawMessage) (string, error) { return "", nil }
