//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

//go:wasmexport func_summary
func funcSummaryExport() int32 { return guest.Run(guest.FuncExport("summary")) }

//go:wasmexport func_enabled
func funcEnabledExport() int32 { return guest.Run(guest.FuncExport("enabled")) }

//go:wasmexport on_entity_deleted
func entityDeletedExport() int32 { return guest.Run(sdk.EntityDeleted.Export()) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
