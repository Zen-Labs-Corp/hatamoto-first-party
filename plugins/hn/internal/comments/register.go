package comments

import "github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"

// Register adds this phase's handlers to r. plugin.go calls it once, in phase order.
func Register(r *core.Registry) {
	defineNotices()
	registerEvents(r)
	r.Cron("comment_rank", rankCron)
	r.Cron("reconcile", reconcileCron)
	r.Subscribe(core.StoryDeleted, onStoryDeleted)
	r.Page("/newcomments", newcommentsPage(r))
	r.Page("/threads", threadsPage(r))
	r.Route("POST /vote/comment/{id}", voteHandler(r))
}
