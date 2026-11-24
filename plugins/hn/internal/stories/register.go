package stories

import (
	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// Register adds this phase's handlers to r. plugin.go calls it once, in phase order.
func Register(r *core.Registry) {
	defineNotices()
	r.Route("POST /vote/story/{id}", func(q *core.Request) (sdk.HTTPResponse, error) { return handleVote(r, q) })
}
