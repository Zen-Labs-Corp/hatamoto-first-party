package moderation

import (
	"errors"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	sqlSweepNetBans = `DELETE FROM net_bans WHERE expires_at IS NOT NULL AND expires_at < $1::timestamptz`
	sqlSweepRate    = `DELETE FROM rate_events WHERE id IN (SELECT id FROM rate_events WHERE created_at < $1::timestamptz - interval '24 hours' ORDER BY id LIMIT 10000)`
	sqlSweepModLog  = `DELETE FROM mod_log WHERE id IN (SELECT id FROM mod_log WHERE created_at < $1::timestamptz - interval '180 days' ORDER BY id LIMIT 10000)`

	sweepBatch  = 10000
	sweepBudget = 20 * time.Second
)

// sweep drops expired network bans, rate events older than a day and moderation
// log rows older than 180 days. Each delete is batched: it repeats until a batch
// comes back short or the time budget is spent, and runs even when another
// failed.
func sweep(sdk.CronEvent) error {
	if core.Call == nil {
		return nil
	}
	now := nowArg()
	var errs []error
	deadline := time.Now().Add(sweepBudget)
	for _, q := range []string{sqlSweepNetBans, sqlSweepRate, sqlSweepModLog} {
		for {
			n, err := core.Exec(q, now)
			if err != nil {
				errs = append(errs, err)
				break
			}
			if n < sweepBatch || time.Now().After(deadline) {
				break
			}
		}
	}
	return errors.Join(errs...)
}
