// Command registry-pages is a Hatamoto plugin for hatamoto.dev only. It fetches
// and verifies the signed registry, fills the /plugins and /themes pages with a
// registry_directory block, and keeps one registry-entry entity per registry
// name in sync so that /plugins/<name> and /themes/<name> are real pages.
//
// The logic is plain Go in this package; the //go:wasmexport lines are in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnFilter(sdk.RenderPage, pageFilter)
	guest.OnCron("sync", syncCron)
}
