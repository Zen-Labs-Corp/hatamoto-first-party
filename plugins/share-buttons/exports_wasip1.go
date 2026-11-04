//go:build wasip1

package main

import "hatamoto.dev/sdk/guest"

// //go:wasmexport must sit in package main, one line per template func.

//go:wasmexport func_network
func funcNetwork() int32 { return guest.Run(guest.FuncExport("network")) }

//go:wasmexport func_style
func funcStyle() int32 { return guest.Run(guest.FuncExport("style")) }

//go:wasmexport func_page_url
func funcPageURL() int32 { return guest.Run(guest.FuncExport("page_url")) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
