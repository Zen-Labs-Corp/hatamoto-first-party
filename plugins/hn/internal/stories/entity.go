package stories

import (
	"errors"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// storySchema is the content type the plugin owns (plugin.json entity_schema).
const storySchema = "hn-story"

// sqlDropStory removes the story row of an entry and returns its number and
// title; votes and hidden rows cascade with it.
const sqlDropStory = `SELECT to_jsonb(d) FROM (
WITH gone AS (DELETE FROM story_meta WHERE entity_id = $1::uuid RETURNING item_no, title) SELECT item_no, title FROM gone) d`

// EntityDeleted keeps the story lists in step when a story entry is deleted
// outside the plugin (the owner in the admin, a changeset or import revert):
// it drops the story row and announces StoryDeleted, as the author's own
// delete does. The author's delete already removed the row, so the event it
// causes finds nothing and does nothing.
func EntityDeleted(r *core.Registry) func(sdk.EntityEvent) error {
	return func(ev sdk.EntityEvent) error {
		if ev.SchemaSlug != storySchema || ev.ID == "" {
			return nil
		}
		var row struct {
			ItemNo int64  `json:"item_no"`
			Title  string `json:"title"`
		}
		err := core.QueryJSON(sqlDropStory, &row, ev.ID)
		if errors.Is(err, core.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		dropNumber(row.ItemNo)
		r.Publish(core.StoryEvent{Kind: core.StoryDeleted, EntityID: ev.ID, ItemNo: row.ItemNo, Title: row.Title})
		return nil
	}
}
