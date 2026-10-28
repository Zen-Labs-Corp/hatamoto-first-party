//go:build wasip1

package main

import "hatamoto.dev/sdk/guest"

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

//go:wasmexport on_cron_rollup
func onCronRollup() int32 { return guest.Run(guest.CronExport("rollup")) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
