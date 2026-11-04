//go:build wasip1

package main

import "hatamoto.dev/sdk/guest"

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport on_cron_refresh
func onCronRefresh() int32 { return guest.Run(guest.CronExport("refresh")) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
