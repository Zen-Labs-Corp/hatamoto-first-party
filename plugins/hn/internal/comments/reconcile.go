package comments

import (
	"encoding/json"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// sqlSample picks the entities one run checks; $1 is the current hour, so the
// pick rotates hourly without any stored cursor.
const sqlSample = `SELECT coalesce(jsonb_agg(e), '[]') FROM (SELECT e FROM (SELECT DISTINCT entity_id AS e FROM comment_meta) d ORDER BY md5(e::text || $1) LIMIT 20) s`

// sqlGone lists mirrored comments of an entity that core no longer lists.
// Rows newer than $3 (the start of the listing) are left alone: their event
// may have arrived while the listing ran.
const sqlGone = `SELECT coalesce(jsonb_agg(comment_id), '[]') FROM comment_meta WHERE entity_id = $1::uuid AND created_at < $3 AND comment_id NOT IN (SELECT jsonb_array_elements_text($2::jsonb)::uuid)`

const sqlSync = `UPDATE comment_meta m SET body_html = s.body_html, author_name = s.author_name, live = s.live FROM jsonb_to_recordset($1::jsonb) AS s(id uuid, body_html text, author_name text, live boolean)
 WHERE m.comment_id = s.id AND (m.body_html, m.author_name, m.live) IS DISTINCT FROM (s.body_html, s.author_name, s.live)`

const sqlRepoint = `UPDATE comment_meta m SET points = 1 + coalesce((SELECT sum(value) FROM comment_votes v WHERE v.comment_id = m.comment_id), 0), dirty = true
 WHERE m.entity_id = $1::uuid AND m.points <> 1 + coalesce((SELECT sum(value) FROM comment_votes v WHERE v.comment_id = m.comment_id), 0)`

const sqlEntityIDs = `SELECT coalesce(jsonb_agg(comment_id), '[]') FROM (SELECT comment_id FROM comment_meta WHERE entity_id = $1::uuid LIMIT 1000) x`

const (
	maxThreadPages  = 10
	reconcileBudget = 8 * time.Second
)

// reconcileCron repairs drift between core's comments and the mirror for a
// rotating sample of entities. It stops quietly when core cannot list threads.
func reconcileCron(sdk.CronEvent) error {
	if hostless() {
		return nil
	}
	start := core.Now()
	var entities []string
	if err := core.QueryJSON(sqlSample, &entities, start.UTC().Format("2006-01-02T15")); err != nil {
		return nil
	}
	for _, entity := range entities {
		if core.Now().Sub(start) > reconcileBudget {
			return nil
		}
		listedAt := core.Now()
		items, complete, err := listAll(entity)
		if isUnavailable(err) || isRateLimited(err) {
			return nil
		}
		if err != nil || !complete {
			continue
		}
		reconcileEntity(entity, items, listedAt)
	}
	return nil
}

// listAll pages a whole thread, dead comments included. complete is false when
// the thread was still growing after maxThreadPages pages.
func listAll(entity string) (items []sdk.CommentItem, complete bool, err error) {
	for page := 1; page <= maxThreadPages; page++ {
		var th sdk.CommentThread
		if err := core.Call(sdk.FnCommentsThread, sdk.CommentsThreadArgs{EntityID: entity, Page: page, IncludeDead: true}, &th); err != nil {
			return nil, false, err
		}
		items = append(items, th.Items...)
		if !th.HasMore {
			return items, true, nil
		}
	}
	return items, false, nil
}

// reconcileEntity applies one complete listing. An empty listing changes
// nothing: core also answers an empty thread for an unpublished entity.
func reconcileEntity(entity string, items []sdk.CommentItem, listedAt time.Time) {
	if len(items) == 0 {
		return
	}
	type synced struct {
		ID         string `json:"id"`
		BodyHTML   string `json:"body_html"`
		AuthorName string `json:"author_name"`
		Live       bool   `json:"live"`
	}
	ids := make([]string, 0, len(items))
	rows := make([]synced, 0, len(items))
	for _, it := range items {
		if !validID(it.ID) {
			return // not a listing this code understands: touch nothing
		}
		ids = append(ids, it.ID)
		rows = append(rows, synced{it.ID, it.BodyHTML, it.AuthorName, it.Status != "dead"})
	}
	idList, _ := json.Marshal(ids)
	var gone []string
	if err := core.QueryJSON(sqlGone, &gone, entity, string(idList), listedAt); err != nil {
		return
	}
	_ = removeComments(gone, entity)
	rowList, _ := json.Marshal(rows)
	_, _ = core.Exec(sqlSync, string(rowList))
	_ = recount(entity)
	_, _ = core.Exec(sqlRepoint, entity)
}

// onStoryDeleted drops the mirrored comments of a deleted story.
func onStoryDeleted(ev core.StoryEvent) {
	if hostless() || !validID(ev.EntityID) {
		return
	}
	var ids []string
	if err := core.QueryJSON(sqlEntityIDs, &ids, ev.EntityID); err != nil {
		return
	}
	_ = removeComments(ids, ev.EntityID)
}
