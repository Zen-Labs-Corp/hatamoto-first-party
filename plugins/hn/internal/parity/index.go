package parity

import (
	"html"
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// The search index is search_docs: one row per live story and per approved
// comment, filled by events and repaired by the search cron. Text arguments
// are cast to text so that || and to_tsvector resolve.
const (
	maxDocBody   = 8 << 10 // bytes of comment text kept per document
	batch        = 500     // rows a cron statement touches
	commentBatch = 25      // comments per cron round, so a result and the upsert stay under 1 MiB
	maxRounds    = 20      // comment rounds per cron run
	cronBudget   = 8 * time.Second
)

const (
	sqlUpsertStory = "INSERT INTO search_docs (kind, target_id, entity_id, title, body, tsv, created_at) VALUES ('story', $1::uuid, $1::uuid, $2::text, $3::text, to_tsvector('simple', $2::text || ' ' || $3::text), coalesce((SELECT s.created_at FROM story_meta s WHERE s.entity_id = $1::uuid), now())) ON CONFLICT (kind, target_id) DO UPDATE SET title = EXCLUDED.title, body = EXCLUDED.body, tsv = EXCLUDED.tsv"
	// favorites has no foreign key on target_id, so a deleted story's rows go here.
	sqlDropStoryDocs = "DELETE FROM search_docs WHERE entity_id = $1::uuid"
	sqlDropStoryFavs = "DELETE FROM favorites WHERE kind = 'story' AND target_id = $1::uuid"
	sqlUpsertComment = "INSERT INTO search_docs (kind, target_id, entity_id, title, body, tsv, created_at) VALUES ('comment', $1::uuid, $2::uuid, '', $3::text, to_tsvector('simple', $3::text), $4::timestamptz) ON CONFLICT (kind, target_id) DO UPDATE SET body = EXCLUDED.body, tsv = EXCLUDED.tsv"
	sqlDropComment   = "DELETE FROM search_docs WHERE kind = 'comment' AND target_id = $1::uuid"
)

// The cron statements: each one is bounded by batch and repairs one kind of drift.
const (
	sqlAddStories     = "INSERT INTO search_docs (kind, target_id, entity_id, title, body, tsv, created_at) SELECT 'story', s.entity_id, s.entity_id, s.title, s.body, to_tsvector('simple', s.title || ' ' || s.body), s.created_at FROM story_meta s WHERE NOT s.dead AND NOT EXISTS (SELECT 1 FROM search_docs d WHERE d.kind = 'story' AND d.target_id = s.entity_id) ORDER BY s.created_at DESC LIMIT 500 ON CONFLICT (kind, target_id) DO NOTHING"
	sqlFixStories     = "UPDATE search_docs d SET title = s.title, body = s.body, tsv = to_tsvector('simple', s.title || ' ' || s.body) FROM story_meta s WHERE d.kind = 'story' AND d.target_id = s.entity_id AND d.target_id IN (SELECT d2.target_id FROM search_docs d2 JOIN story_meta s2 ON s2.entity_id = d2.target_id WHERE d2.kind = 'story' AND (d2.title <> s2.title OR d2.body <> s2.body) LIMIT 500)"
	sqlPruneStories   = "DELETE FROM search_docs WHERE kind = 'story' AND target_id IN (SELECT d.target_id FROM search_docs d LEFT JOIN story_meta s ON s.entity_id = d.target_id WHERE d.kind = 'story' AND (s.entity_id IS NULL OR s.dead) LIMIT 500)"
	sqlPruneComments  = "DELETE FROM search_docs WHERE kind = 'comment' AND target_id IN (SELECT d.target_id FROM search_docs d LEFT JOIN comment_meta c ON c.comment_id = d.target_id WHERE d.kind = 'comment' AND (c.comment_id IS NULL OR NOT c.live) LIMIT 500)"
	sqlMissingComment = "SELECT coalesce(jsonb_agg(to_jsonb(x)), '[]'::jsonb) FROM (SELECT c.comment_id AS id, c.entity_id, left(c.body_html, 32768) AS body_html, c.created_at FROM comment_meta c WHERE c.live AND NOT EXISTS (SELECT 1 FROM search_docs d WHERE d.kind = 'comment' AND d.target_id = c.comment_id) ORDER BY c.created_at DESC LIMIT 25) x"
	sqlAddComments    = "INSERT INTO search_docs (kind, target_id, entity_id, title, body, tsv, created_at) SELECT 'comment', x.id::uuid, x.entity_id::uuid, '', x.body, to_tsvector('simple', x.body), x.created_at::timestamptz FROM jsonb_to_recordset($1::jsonb) AS x(id text, entity_id text, body text, created_at text) ON CONFLICT (kind, target_id) DO UPDATE SET body = EXCLUDED.body, tsv = EXCLUDED.tsv"
)

// plain is the searchable text of a comment's HTML: tags become spaces,
// entities are decoded, whitespace is collapsed and the result is cut to
// maxDocBody bytes on a rune boundary. An unterminated tag (a cut body) is dropped.
func plain(htmlText string) string {
	var b strings.Builder
	inTag := false
	for _, c := range htmlText {
		switch {
		case c == '<':
			inTag = true
			b.WriteByte(' ')
		case c == '>' && inTag:
			inTag = false
		case !inTag:
			b.WriteRune(c)
		}
	}
	s := strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
	if len(s) > maxDocBody {
		n := maxDocBody
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		s = strings.TrimSpace(s[:n])
	}
	return s
}

// indexStory stores a saved story; title and body are plain text already.
func indexStory(ev core.StoryEvent) {
	if !isUUID(ev.EntityID) {
		return
	}
	_, _ = core.Exec(sqlUpsertStory, ev.EntityID, ev.Title, ev.Body)
}

// dropStory removes a deleted story's documents, its comments' and its favorites.
func dropStory(ev core.StoryEvent) {
	if !isUUID(ev.EntityID) {
		return
	}
	_, _ = core.Exec(sqlDropStoryDocs, ev.EntityID)
	_, _ = core.Exec(sqlDropStoryFavs, ev.EntityID)
}

// dropComment removes a comment's document (spam or deleted, whatever the
// status field of the event says).
func dropComment(ev sdk.CommentEvent) error {
	if isUUID(ev.ID) {
		_, _ = core.Exec(sqlDropComment, ev.ID)
	}
	return nil
}

// indexComment keeps one comment's document in step: approved comments are
// indexed, every other status removes it. It never fails the comment action.
func indexComment(ev sdk.CommentEvent) error {
	if !isUUID(ev.ID) {
		return nil
	}
	if ev.Status != "approved" || !isUUID(ev.EntityID) {
		return dropComment(ev)
	}
	created := nowArg()
	if !ev.CreatedAt.IsZero() {
		created = ev.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	_, _ = core.Exec(sqlUpsertComment, ev.ID, ev.EntityID, plain(ev.BodyHTML), created)
	return nil
}

type missingComment struct {
	ID       string `json:"id"`
	EntityID string `json:"entity_id"`
	Body     string `json:"body"`
	Created  string `json:"created_at"`
}

// searchCron repairs the index from story_meta and comment_meta in bounded
// steps and stops at the first host error. It never returns an error.
func searchCron(sdk.CronEvent) error {
	if core.Call == nil {
		return nil
	}
	for _, sql := range []string{sqlAddStories, sqlFixStories, sqlPruneStories, sqlPruneComments} {
		if _, err := core.Exec(sql); err != nil {
			return nil
		}
	}
	start := core.Now()
	for range maxRounds {
		var rows []struct {
			ID        string    `json:"id"`
			EntityID  string    `json:"entity_id"`
			BodyHTML  string    `json:"body_html"`
			CreatedAt time.Time `json:"created_at"`
		}
		if err := core.QueryJSON(sqlMissingComment, &rows); err != nil || len(rows) == 0 {
			return nil
		}
		docs := make([]missingComment, 0, len(rows))
		for _, row := range rows {
			if isUUID(row.ID) && isUUID(row.EntityID) {
				docs = append(docs, missingComment{ID: row.ID, EntityID: row.EntityID, Body: plain(row.BodyHTML), Created: row.CreatedAt.UTC().Format(time.RFC3339Nano)})
			}
		}
		if len(docs) == 0 {
			return nil
		}
		// The list travels as one argument that SQL expands with jsonb_to_recordset.
		if _, err := core.Exec(sqlAddComments, docs); err != nil || len(rows) < commentBatch || core.Now().Sub(start) >= cronBudget {
			return nil
		}
	}
	return nil
}
