package moderation

import (
	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	sqlDeadBan    = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM bans WHERE user_id = $1 AND mode = 'dead') AS dead) x`
	sqlKillInsert = `INSERT INTO kills (kind, target_id, cause, created_at) VALUES ($1, $2::uuid, $3, $4::timestamptz) ON CONFLICT (kind, target_id) DO NOTHING`
	sqlStoryDrop  = `WITH f AS (DELETE FROM flags WHERE kind = 'story' AND target_id = $1::uuid), v AS (DELETE FROM vouches WHERE kind = 'story' AND target_id = $1::uuid) DELETE FROM kills WHERE kind = 'story' AND target_id = $1::uuid`
	sqlLogInsert  = `INSERT INTO mod_log (actor, action, target, detail, created_at) VALUES ($1, $2, $3, $4, $5::timestamptz)`
	// sqlStoryDeadMode kills a just-saved story of a dead-mode author. It only
	// flips story_meta.dead: the entry itself is never touched.
	sqlStoryDeadMode = `WITH s AS (UPDATE story_meta SET dead = true WHERE entity_id = $1::uuid AND NOT dead AND author_id IN (SELECT user_id FROM bans WHERE mode = 'dead') RETURNING entity_id) INSERT INTO kills (kind, target_id, cause, created_at) SELECT 'story', entity_id, 'ban', $2::timestamptz FROM s ON CONFLICT (kind, target_id) DO NOTHING`
)

func registerDeadMode(r *core.Registry) {
	r.Comment(core.OnCreated, deadModeComment)
	r.Subscribe(core.StorySaved, deadModeStory)
	r.Subscribe(core.StoryDeleted, dropStoryModeration)
}

// dropStoryModeration removes the flags, vouches and kills of a deleted story,
// which have no foreign key to it. Failures are ignored; the queue skips
// targets without a meta row anyway.
func dropStoryModeration(ev core.StoryEvent) {
	if ev.EntityID == "" {
		return
	}
	_, _ = core.Exec(sqlStoryDrop, ev.EntityID)
}

// deadModeComment kills the new comment of a member under a dead-mode ban.
// Any failure leaves the comment alone (fail open).
func deadModeComment(ev sdk.CommentEvent) error {
	if ev.ID == "" || ev.CommenterKey == "" {
		return nil
	}
	u, err := core.UserByKey(ev.CommenterKey)
	if err != nil || u == nil {
		return nil
	}
	var res struct{ Dead bool }
	if err := core.QueryJSON(sqlDeadBan, &res, u.ID); err != nil || !res.Dead {
		return nil
	}
	var changed sdk.CommentsChanged
	// unavailable or any other failure: the comment stays alive and nothing is recorded.
	if err := core.Call(sdk.FnCommentsMarkDead, sdk.CommentIDsArgs{IDs: []string{ev.ID}}, &changed); err != nil {
		return nil
	}
	now := nowArg()
	_, _ = core.Exec(sqlKillInsert, "comment", ev.ID, "ban", now)
	_, _ = core.Exec(sqlLogInsert, "ban", "kill", "comment:"+ev.ID, "", now)
	return nil
}

// deadModeStory runs inside the submit, before the answer.
func deadModeStory(ev core.StoryEvent) {
	if ev.EntityID == "" {
		return
	}
	_, _ = core.Exec(sqlStoryDeadMode, ev.EntityID, nowArg())
}
