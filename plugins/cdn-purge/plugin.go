// Command cdn-purge is a Hatamoto plugin. Purges changed pages from Cloudflare, Bunny or Fastly when you publish, update or delete content, so a CDN in front of your site stays fresh.
//
// The hooks live here, the purge logic in purge.go and the provider calls in
// providers.go. Keep the //go:wasmexport lines in exports_wasip1.go, so
// everything but the exports builds and tests on the host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.EntityPublished, func(ev sdk.EntityEvent) error { handle("published", ev); return nil })
	guest.OnAction(sdk.EntityUpdated, func(ev sdk.EntityEvent) error { handle("updated", ev); return nil })
	guest.OnAction(sdk.EntityDeleted, func(ev sdk.EntityEvent) error { handle("deleted", ev); return nil })
}
