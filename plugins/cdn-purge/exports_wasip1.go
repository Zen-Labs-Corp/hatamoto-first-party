//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport on_entity_published
func onEntityPublished() int32 { return guest.Run(sdk.EntityPublished.Export()) }

//go:wasmexport on_entity_updated
func onEntityUpdated() int32 { return guest.Run(sdk.EntityUpdated.Export()) }

//go:wasmexport on_entity_deleted
func onEntityDeleted() int32 { return guest.Run(sdk.EntityDeleted.Export()) }

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
