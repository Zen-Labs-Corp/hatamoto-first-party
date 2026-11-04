// Command polls is the first-party poll, survey and quiz plugin.
//
// It counts the answers to the choice questions of core forms, scores quizzes,
// and shows the results in the admin, on an optional public page, as a page
// builder source and as CSV. The logic is plain Go; the //go:wasmexport lines
// live in exports_wasip1.go, so everything but the exports builds and tests on
// the host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.FormSubmitted, onSubmitted)
}
