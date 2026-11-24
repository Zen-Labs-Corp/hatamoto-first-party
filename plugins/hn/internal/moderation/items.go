package moderation

import (
	"errors"
	"regexp"
	"strconv"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	sqlResolveStory   = `SELECT to_jsonb(x) FROM (SELECT s.entity_id AS id, s.item_no, s.author_id, s.dead, COALESCE(k.cause, '') AS cause FROM story_meta s LEFT JOIN kills k ON k.kind = 'story' AND k.target_id = s.entity_id WHERE s.item_no = $1) x`
	sqlResolveStoryID = `SELECT to_jsonb(x) FROM (SELECT s.entity_id AS id, s.item_no, s.author_id, s.dead, COALESCE(k.cause, '') AS cause FROM story_meta s LEFT JOIN kills k ON k.kind = 'story' AND k.target_id = s.entity_id WHERE s.entity_id = $1::uuid) x`
	sqlResolveComment = `SELECT to_jsonb(x) FROM (SELECT c.comment_id AS id, COALESCE(s.item_no, 0) AS item_no, COALESCE(c.author_id, 0) AS author_id, (k.cause IS NOT NULL) AS dead, COALESCE(k.cause, '') AS cause FROM comment_meta c LEFT JOIN story_meta s ON s.entity_id = c.entity_id LEFT JOIN kills k ON k.kind = 'comment' AND k.target_id = c.comment_id WHERE c.comment_id = $1::uuid) x`

	sqlKillOwner   = `WITH c AS (UPDATE comment_meta SET live = false WHERE $1::text = 'comment' AND comment_id = $2::uuid) INSERT INTO kills (kind, target_id, cause, created_at) VALUES ($1, $2::uuid, 'owner', $3::timestamptz) ON CONFLICT (kind, target_id) DO UPDATE SET cause = 'owner'`
	sqlStoryDead   = `UPDATE story_meta SET dead = $2 WHERE entity_id = $1::uuid`
	sqlFlagsDelete = `DELETE FROM flags WHERE kind = $1 AND target_id = $2::uuid`
	// A restored comment is live again only when the host changed it: a held or deleted comment is not touched by the host, so it stays out of search.
	sqlKillsDelete  = `WITH c AS (UPDATE comment_meta SET live = true WHERE $1::text = 'comment' AND $3::int > 0 AND comment_id = $2::uuid) DELETE FROM kills WHERE kind = $1 AND target_id = $2::uuid`
	causeFlags      = "flags"
	causeOwner      = "owner"
	causeBan        = "ban"
	kindStory       = "story"
	kindComment     = "comment"
	maxModBody      = 64 << 10
	uuidPattern     = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
	maxStoryNoDigit = 18
)

var uuidRe = regexp.MustCompile(uuidPattern)

// target is a story or a comment a moderation action is about.
type target struct {
	Kind     string `json:"-"`
	ID       string `json:"id"`
	ItemNo   int64  `json:"item_no"` // a comment: the number of its story, 0 when unknown
	AuthorID int64  `json:"author_id"`
	Dead     bool   `json:"dead"`
	Cause    string `json:"cause"` // why it is dead ("" alive)
}

// parseItemNo reads an item number made of digits only.
func parseItemNo(s string) (int64, bool) {
	if s == "" || len(s) > maxStoryNoDigit {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// resolve finds the item behind a URL value: a story by item number, a comment
// by uuid. It returns nil, nil when there is none (or the value is malformed:
// nothing reaches SQL unchecked).
func resolve(kind, raw string) (*target, error) {
	switch kind {
	case kindStory:
		no, ok := parseItemNo(raw)
		if !ok {
			return nil, nil
		}
		return load(kindStory, sqlResolveStory, no)
	case kindComment:
		return resolveByID(kind, raw)
	}
	return nil, nil
}

// resolveByID finds a story or a comment by its uuid.
func resolveByID(kind, id string) (*target, error) {
	if !uuidRe.MatchString(id) {
		return nil, nil
	}
	switch kind {
	case kindStory:
		return load(kindStory, sqlResolveStoryID, id)
	case kindComment:
		return load(kindComment, sqlResolveComment, id)
	}
	return nil, nil
}

func load(kind, sql string, arg any) (*target, error) {
	var t target
	if err := core.QueryJSON(sql, &t, arg); err != nil {
		if errors.Is(err, core.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	t.Kind = kind
	if t.Dead && t.Cause == "" {
		t.Cause = causeFlags // a dead story without a kills row was killed before kills existed
	}
	return &t, nil
}

// logTarget is the mod_log target of an item.
func (t *target) logTarget() string {
	if t.Kind == kindStory {
		return "story:" + strconv.FormatInt(t.ItemNo, 10)
	}
	return "comment:" + t.ID
}

func writeLog(actor, action, target, detail string) {
	_, _ = core.Exec(sqlLogInsert, actor, action, target, detail, nowArg())
}

// kill makes the item dead and records why. A story only gets story_meta.dead
// (the entry itself is never touched); a comment goes through the host. The
// bool tells whether it is dead now. A failure is logged as "kill-unavailable";
// for the owner it also comes back as a notice, for the automatic causes
// nothing is shown (the flag stays).
func kill(t *target, cause, actor string) (bool, core.Notice) {
	failed := func(n core.Notice) (bool, core.Notice) {
		writeLog(actor, "kill-unavailable", t.logTarget(), "")
		if cause == causeOwner {
			return false, n
		}
		return false, ""
	}
	if t.Kind == kindStory {
		if _, err := core.Exec(sqlStoryDead, t.ID, true); err != nil {
			return failed(nError)
		}
	} else {
		var changed sdk.CommentsChanged
		if err := core.Call(sdk.FnCommentsMarkDead, sdk.CommentIDsArgs{IDs: []string{t.ID}}, &changed); err != nil {
			return failed(nOff)
		}
	}
	sql := sqlKillInsert
	if cause == causeOwner {
		sql = sqlKillOwner
	}
	args := []any{t.Kind, t.ID}
	if cause != causeOwner {
		args = append(args, cause)
	}
	if _, err := core.Exec(sql, append(args, nowArg())...); err != nil {
		return failed(nError)
	}
	writeLog(actor, "kill", t.logTarget(), cause)
	t.Dead, t.Cause = true, cause
	return true, ""
}

// restore makes the item alive again whatever killed it and forgets its flags.
// When the host cannot restore a comment nothing else changes.
func restore(t *target, actor string) core.Notice {
	var changed sdk.CommentsChanged
	if t.Kind == kindStory {
		if _, err := core.Exec(sqlStoryDead, t.ID, false); err != nil {
			return nError
		}
	} else {
		if err := core.Call(sdk.FnCommentsRestore, sdk.CommentIDsArgs{IDs: []string{t.ID}}, &changed); err != nil {
			return nOff
		}
	}
	_, _ = core.Exec(sqlFlagsDelete, t.Kind, t.ID)
	_, _ = core.Exec(sqlKillsDelete, t.Kind, t.ID, changed.Changed)
	writeLog(actor, "restore", t.logTarget(), "")
	t.Dead, t.Cause = false, ""
	return ""
}

// backTo is where a form post goes back to: the form's next, then ?next=, then
// the usual same-origin fallback.
func backTo(q *core.Request) string {
	if n := q.Form().Get("next"); core.SafePath(n) {
		return n
	}
	if v := q.Query["next"]; len(v) > 0 && core.SafePath(v[0]) {
		return v[0]
	}
	return core.SafeNext(q)
}
