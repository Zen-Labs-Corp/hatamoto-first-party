//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// //go:wasmexport must sit in package main, one line per handled hook.

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

//go:wasmexport filter_render_page
func filterRenderPage() int32 { return guest.Run(sdk.RenderPage.Export()) }

//go:wasmexport filter_comment_check
func filterCommentCheck() int32 { return guest.Run(sdk.CommentCheckF.Export()) }

//go:wasmexport filter_comment_draft
func filterCommentDraft() int32 { return guest.Run(sdk.CommentDraftF.Export()) }

//go:wasmexport on_comment_created
func onCommentCreated() int32 { return guest.Run(sdk.CommentCreated.Export()) }

//go:wasmexport on_comment_approved
func onCommentApproved() int32 { return guest.Run(sdk.CommentApproved.Export()) }

//go:wasmexport on_comment_spam
func onCommentSpam() int32 { return guest.Run(sdk.CommentSpam.Export()) }

//go:wasmexport on_comment_deleted
func onCommentDeleted() int32 { return guest.Run(sdk.CommentDeleted.Export()) }

//go:wasmexport on_comment_updated
func onCommentUpdated() int32 { return guest.Run(sdk.CommentUpdated.Export()) }

//go:wasmexport on_cron_rank
func onCronRank() int32 { return guest.Run(guest.CronExport("rank")) }

//go:wasmexport on_cron_comment_rank
func onCronCommentRank() int32 { return guest.Run(guest.CronExport("comment_rank")) }

//go:wasmexport on_cron_reconcile
func onCronReconcile() int32 { return guest.Run(guest.CronExport("reconcile")) }

//go:wasmexport on_cron_sweep
func onCronSweep() int32 { return guest.Run(guest.CronExport("sweep")) }

//go:wasmexport on_cron_search
func onCronSearch() int32 { return guest.Run(guest.CronExport("search")) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
