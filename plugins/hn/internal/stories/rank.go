package stories

import (
	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// rankCron recomputes the gravity score of live stories younger than 7 days
// and sweeps reserved item numbers that never became a story. Two statements,
// each bounded by an index on created_at.
func rankCron(sdk.CronEvent) error {
	if core.Call == nil { // no host (wiring test, older hosts): nothing to do
		return nil
	}
	now := nowArg()
	if _, err := core.Exec("UPDATE story_meta SET score = "+scoreSQL("points", "$1")+
		" WHERE created_at > $1::timestamptz - interval '7 days' AND NOT dead AND kind <> 'job'", now); err != nil {
		return err
	}
	_, err := core.Exec(`DELETE FROM item_numbers n WHERE n.created_at < $1::timestamptz - interval '1 hour'
AND NOT EXISTS (SELECT 1 FROM story_meta s WHERE s.item_no = n.item_no)`, now)
	return err
}
