//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport func_when
func funcWhen() int32 { return guest.Run(guest.FuncExport("when")) }

//go:wasmexport func_addlink
func funcAddlink() int32 { return guest.Run(guest.FuncExport("addlink")) }

//go:wasmexport filter_builder_data
func filterBuilderData() int32 { return guest.Run(sdk.BuilderData.Export()) }

//go:wasmexport filter_jsonld_enrich
func filterJSONLDEnrich() int32 { return guest.Run(sdk.JSONLDEnrich.Export()) }

//go:wasmexport on_entity_created
func onEntityCreatedExport() int32 { return guest.Run(sdk.EntityCreated.Export()) }

//go:wasmexport on_entity_updated
func onEntityUpdatedExport() int32 { return guest.Run(sdk.EntityUpdated.Export()) }

//go:wasmexport on_entity_published
func onEntityPublishedExport() int32 { return guest.Run(sdk.EntityPublished.Export()) }

//go:wasmexport on_entity_deleted
func onEntityDeletedExport() int32 { return guest.Run(sdk.EntityDeleted.Export()) }

//go:wasmexport on_import_completed
func onImportCompletedExport() int32 { return guest.Run(sdk.ImportCompleted.Export()) }

//go:wasmexport on_cron_reconcile
func onCronReconcileExport() int32 { return guest.Run(guest.CronExport("reconcile")) }

//go:wasmexport on_run_step
func onRunStepExport() int32 { return guest.Run(sdk.RunStepExport) }

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
