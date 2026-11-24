package comments

import (
	"encoding/json"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const sqlDirty = `SELECT coalesce(jsonb_agg(x ORDER BY x.entity_id, x.comment_id), '[]') FROM (SELECT comment_id, entity_id, points FROM comment_meta WHERE dirty ORDER BY entity_id, comment_id LIMIT 1000) x`

// sqlClean clears dirty for the rows that were sent, unless a vote moved their points meanwhile.
const sqlClean = `UPDATE comment_meta m SET dirty = false FROM jsonb_to_recordset($1::jsonb) AS s(id uuid, points int) WHERE m.comment_id = s.id AND m.points = s.points AND m.dirty`

const (
	// rankBudget keeps a run under the 10 s hook limit.
	rankBudget = 8 * time.Second
	// maxRankCalls stays well inside the shared write limiter (60/min, burst 20).
	maxRankCalls = 15
)

type dirtyRow struct {
	ID     string `json:"comment_id"`
	Entity string `json:"entity_id"`
	Points int    `json:"points"`
}

type sentRank struct {
	ID     string `json:"id"`
	Points int    `json:"points"`
}

// rankCron feeds the rank (the points) of dirty comments to core, one entity
// per batch of at most sdk.MaxCommentRankItems. Quiet when the host function
// is unavailable or rate limited: the rows stay dirty for the next run.
func rankCron(sdk.CronEvent) error {
	if hostless() {
		return nil
	}
	start := core.Now()
	var rows []dirtyRow
	if err := core.QueryJSON(sqlDirty, &rows); err != nil {
		return nil
	}
	calls := 0
	for len(rows) > 0 {
		n := 1
		for n < len(rows) && rows[n].Entity == rows[0].Entity {
			n++
		}
		group := rows[:n]
		rows = rows[n:]
		for len(group) > 0 {
			if core.Now().Sub(start) > rankBudget || calls == maxRankCalls {
				return nil
			}
			chunk := group[:min(len(group), sdk.MaxCommentRankItems)]
			group = group[len(chunk):]

			items := make([]sdk.CommentRankItem, len(chunk))
			sent := make([]sentRank, len(chunk))
			for i, r := range chunk {
				items[i] = sdk.CommentRankItem{ID: r.ID, Rank: float64(r.Points)}
				sent[i] = sentRank{r.ID, r.Points}
			}
			var out sdk.CommentsChanged
			err := core.Call(sdk.FnCommentsSetRank, sdk.CommentRankArgs{Items: items}, &out)
			calls++
			if isUnavailable(err) || isRateLimited(err) {
				return nil
			}
			if err != nil {
				break // leave this entity dirty (reconcile removes stale ids), go on with the next
			}
			list, _ := json.Marshal(sent)
			_, _ = core.Exec(sqlClean, string(list))
		}
	}
	return nil
}
