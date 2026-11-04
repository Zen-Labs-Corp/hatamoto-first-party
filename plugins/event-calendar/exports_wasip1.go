//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport func_when
func funcWhen() int32 { return guest.Run(guest.FuncExport("when")) }

//go:wasmexport filter_builder_data
func filterBuilderData() int32 { return guest.Run(sdk.BuilderData.Export()) }

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
