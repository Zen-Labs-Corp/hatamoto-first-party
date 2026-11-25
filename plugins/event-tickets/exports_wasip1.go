//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport filter_render_head
func filterRenderHead() int32 { return guest.Run(sdk.RenderHead.Export()) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
