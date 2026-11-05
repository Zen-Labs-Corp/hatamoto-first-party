//go:build wasip1

package main

import "hatamoto.dev/sdk/guest"

// //go:wasmexport must sit in package main, one line per template func.

//go:wasmexport func_embed_url
func funcEmbedURL() int32 { return guest.Run(guest.FuncExport("embed_url")) }

//go:wasmexport func_booking_url
func funcBookingURL() int32 { return guest.Run(guest.FuncExport("booking_url")) }

//go:wasmexport func_button_label
func funcButtonLabel() int32 { return guest.Run(guest.FuncExport("button_label")) }

//go:wasmexport func_height
func funcHeight() int32 { return guest.Run(guest.FuncExport("height")) }

//go:wasmexport func_provider_name
func funcProviderName() int32 { return guest.Run(guest.FuncExport("provider_name")) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
