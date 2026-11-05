//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport on_form_submitted
func onFormSubmittedExport() int32 { return guest.Run(sdk.FormSubmitted.Export()) }

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

//go:wasmexport on_cron_deliver
func onCronDeliver() int32 { return guest.Run(guest.CronExport("deliver")) }

//go:wasmexport on_cron_retry
func onCronRetry() int32 { return guest.Run(guest.CronExport("retry")) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
