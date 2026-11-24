package comments

import "github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"

// Register adds this phase's handlers to r. plugin.go calls it once, in phase order.
func Register(r *core.Registry) {
	defineNotices()
	registerEvents(r)
	r.Cron("comment_rank", rankCron)
	r.Route("POST /vote/comment/{id}", voteHandler(r))
}
