//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

//go:wasmexport on_cron_refresh
func onCronRefresh() int32 { return guest.Run(guest.CronExport("refresh")) }

//go:wasmexport filter_builder_data
func filterBuilderData() int32 { return guest.Run(sdk.BuilderData.Export()) }

//go:wasmexport func_summary
func funcSummaryExport() int32 { return guest.Run(guest.FuncExport("summary")) }

//go:wasmexport func_count
func funcCountExport() int32 { return guest.Run(guest.FuncExport("count")) }

//go:wasmexport func_field
func funcFieldExport() int32 { return guest.Run(guest.FuncExport("field")) }

//go:wasmexport func_profile
func funcProfileExport() int32 { return guest.Run(guest.FuncExport("profile")) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
