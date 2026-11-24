package comments

import (
	"encoding/json"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// Only comments of HN stories are mirrored (core sends events for every entity
// of the site). The commenter key only ever travels as the SQL argument $4 of the upserts;
// it is never logged or stored beside the comment.
const sqlInsertComment = `INSERT INTO comment_meta (comment_id, entity_id, parent_id, author_id, author_name, body_html, live, created_at, dirty)
 SELECT $1::uuid, $2::uuid, NULLIF($3,'')::uuid, (SELECT id FROM users WHERE commenter_key = NULLIF($4,'')), $5, $6, $7, $8, true
 WHERE EXISTS (SELECT 1 FROM story_meta WHERE entity_id = $2::uuid)
 ON CONFLICT (comment_id) DO UPDATE SET `

const sqlKeepFields = `author_id = COALESCE(comment_meta.author_id, EXCLUDED.author_id),
   author_name = CASE WHEN EXCLUDED.author_name <> '' THEN EXCLUDED.author_name ELSE comment_meta.author_name END,
   body_html = CASE WHEN EXCLUDED.body_html <> '' THEN EXCLUDED.body_html ELSE comment_meta.body_html END`

// created never lowers live: a late "pending" delivery cannot hide an approved comment.
const sqlUpsertCreated = sqlInsertComment + `live = comment_meta.live OR EXCLUDED.live,
   ` + sqlKeepFields

// approved and updated state the live flag outright.
const sqlUpsertState = sqlInsertComment + `live = EXCLUDED.live,
   ` + sqlKeepFields

// sqlRecount sets the live comment count of a story. A count is a recount, so
// repeated deliveries cannot drift it; a missing story_meta row is a no-op.
const sqlRecount = `UPDATE story_meta SET comment_count = c.n FROM (SELECT count(*)::int AS n FROM comment_meta WHERE entity_id = $1::uuid AND live) c
 WHERE entity_id = $1::uuid AND comment_count IS DISTINCT FROM c.n`

const sqlRemovalInfo = `SELECT coalesce(jsonb_build_object('entities', (SELECT coalesce(jsonb_agg(DISTINCT entity_id),'[]') FROM comment_meta WHERE comment_id IN (SELECT jsonb_array_elements_text($1::jsonb)::uuid)),
 'karma', (SELECT coalesce(jsonb_agg(x),'[]') FROM (SELECT m.author_id, sum(v.value)::int AS net FROM comment_meta m JOIN comment_votes v ON v.comment_id = m.comment_id
   WHERE m.comment_id IN (SELECT jsonb_array_elements_text($1::jsonb)::uuid) AND m.author_id IS NOT NULL GROUP BY m.author_id) x)), '{}')`

const sqlDeleteMeta = `DELETE FROM comment_meta WHERE comment_id IN (SELECT jsonb_array_elements_text($1::jsonb)::uuid)`

func approved(ev sdk.CommentEvent) bool { return ev.Status == "approved" }
func always(sdk.CommentEvent) bool      { return true }

// onUpsert mirrors a created, approved or updated comment and recounts its story.
func onUpsert(sqlText string, live func(sdk.CommentEvent) bool) func(sdk.CommentEvent) error {
	return func(ev sdk.CommentEvent) error {
		if hostless() || !validID(ev.ID) || !validID(ev.EntityID) {
			return nil
		}
		key := ev.CommenterKey
		if ev.Staff {
			key = ""
		}
		created := ev.CreatedAt
		if created.IsZero() {
			created = core.Now()
		}
		if _, err := core.Exec(sqlText, ev.ID, ev.EntityID, ev.ParentID, key, ev.AuthorName, ev.BodyHTML, live(ev), created); err != nil {
			return err
		}
		return recount(ev.EntityID)
	}
}

// onRemove drops a spam or deleted comment.
func onRemove(ev sdk.CommentEvent) error {
	if hostless() || !validID(ev.ID) {
		return nil
	}
	return removeComments([]string{ev.ID}, ev.EntityID)
}

func recount(entity string) error {
	if !validID(entity) {
		return nil
	}
	_, err := core.Exec(sqlRecount, entity)
	return err
}

// removeComments deletes comment rows (their votes cascade), takes the votes
// back from the authors' karma and recounts the affected stories (and
// extraEntity when set). Set-based and idempotent: a second call finds nothing.
func removeComments(ids []string, extraEntity string) error {
	if hostless() || len(ids) == 0 {
		return nil
	}
	list, _ := json.Marshal(ids)
	var info struct {
		Entities []string `json:"entities"`
		Karma    []struct {
			AuthorID int64 `json:"author_id"`
			Net      int   `json:"net"`
		} `json:"karma"`
	}
	if err := core.QueryJSON(sqlRemovalInfo, &info, string(list)); err != nil {
		return err
	}
	rows, err := core.Exec(sqlDeleteMeta, string(list))
	if err != nil {
		return err
	}
	if rows > 0 {
		for _, k := range info.Karma {
			if k.Net != 0 {
				_ = core.AddKarma(k.AuthorID, -k.Net) // fail open: a karma miss must not stop the recount
			}
		}
	}
	entities := info.Entities
	if extraEntity != "" {
		entities = append(entities, extraEntity)
	}
	seen := map[string]bool{}
	var first error
	for _, e := range entities {
		if seen[e] {
			continue
		}
		seen[e] = true
		if err := recount(e); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func registerEvents(r *core.Registry) {
	r.Comment(core.OnCreated, onUpsert(sqlUpsertCreated, approved))
	r.Comment(core.OnApproved, onUpsert(sqlUpsertState, always))
	r.Comment(core.OnUpdated, onUpsert(sqlUpsertState, approved))
	r.Comment(core.OnSpam, onRemove)
	r.Comment(core.OnDeleted, onRemove)
}
