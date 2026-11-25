// Command hn is a Hatamoto plugin. A Hacker News style community: stories, votes, karma, ranked threads, flags and moderation, built on core comments.
//
// Each phase lives in internal/<phase> and registers its handlers into one
// core.Registry; this file installs exactly one guest handler per export,
// because the guest package refuses a second handler for the same export.
// The //go:wasmexport lines are in exports_wasip1.go, so everything but the
// exports builds and tests on the host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/accounts"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/comments"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/moderation"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/parity"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/polish"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/stories"
)

var registry = build()

// build registers the phases in their fixed order.
func build() *core.Registry {
	r := core.NewRegistry()
	r.Register("accounts", accounts.Register)
	r.Register("stories", stories.Register)
	r.Register("comments", comments.Register)
	r.Register("moderation", moderation.Register)
	r.Register("parity", parity.Register)
	r.Register("polish", polish.Register)
	return r
}

func init() {
	r := registry
	guest.OnHTTP(r.ServeHTTP)
	guest.OnFilter(sdk.RenderPage, r.RenderPage)
	guest.OnFilter(sdk.CommentCheckF, r.CheckComment) // never errors: the host holds a comment on any error
	guest.OnFilter(sdk.CommentDraftF, r.DraftComment) // the one deliberate rejecter
	guest.OnAction(sdk.CommentCreated, r.CommentAction(core.OnCreated))
	guest.OnAction(sdk.CommentApproved, r.CommentAction(core.OnApproved))
	guest.OnAction(sdk.CommentSpam, r.CommentAction(core.OnSpam))
	guest.OnAction(sdk.CommentDeleted, r.CommentAction(core.OnDeleted))
	guest.OnAction(sdk.CommentUpdated, r.CommentAction(core.OnUpdated))
	for _, n := range core.CronNames {
		guest.OnCron(n, r.CronHandler(n))
	}
}
