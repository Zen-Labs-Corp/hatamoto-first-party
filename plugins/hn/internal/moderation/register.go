package moderation

import (
	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// Register adds this phase's handlers to r. plugin.go calls it once, in phase order.
func Register(r *core.Registry) {
	defineNotices()
	registerGates(r)
	r.Check(checkComment)
	registerDeadMode(r)
	r.Route("POST /flag/{kind}/{id}", func(q *core.Request) (sdk.HTTPResponse, error) { return handleFlag(r, q) })
	r.Route("POST /vouch/{kind}/{id}", handleVouch)
	r.RowActions(rowActions)
	r.Cron("sweep", sweep)
}
