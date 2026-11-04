// Command meta-capi is a Hatamoto plugin. Meta Conversions API: sends a server-side Lead event to Meta for each form submission, with hashed contact details, to back up the browser pixel.
//
// The logic is plain Go in these files; the //go:wasmexport lines live in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.FormSubmitted, onFormSubmitted)
}
