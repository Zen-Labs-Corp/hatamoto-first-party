//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport on_entity_created
func onEntityCreatedExport() int32 { return guest.Run(sdk.EntityCreated.Export()) }

//go:wasmexport on_entity_updated
func onEntityUpdatedExport() int32 { return guest.Run(sdk.EntityUpdated.Export()) }

//go:wasmexport on_entity_published
func onEntityPublishedExport() int32 { return guest.Run(sdk.EntityPublished.Export()) }

//go:wasmexport on_entity_deleted
func onEntityDeletedExport() int32 { return guest.Run(sdk.EntityDeleted.Export()) }

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
