package moderation

import "github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"

// Register adds this phase's handlers to r. plugin.go calls it once, in phase order.
func Register(r *core.Registry) {
	defineNotices()
	registerGates(r)
	r.Check(checkComment)
	registerDeadMode(r)
	r.Cron("sweep", sweep)
}
