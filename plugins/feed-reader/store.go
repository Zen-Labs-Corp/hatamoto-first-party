package main

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	retentionDays = 90
	// builderItemsMax is the most items one builder query returns.
	builderItemsMax = 48
)

// item is one feed entry after parsing. Published is zero when the feed gave
// no usable date.
type item struct {
	GUID      string
	Title     string
	Link      string
	Excerpt   string
	Published time.Time
}

// feedRow is the stored state of one feed.
type feedRow struct {
	Key          string    `json:"key"`
	URL          string    `json:"url"`
	Name         string    `json:"name"`
	Group        int       `json:"grp"`
	LastAttempt  time.Time `json:"last_attempt"` // zero = never, or due at once
	Status       string    `json:"status"`
	ETag         string    `json:"etag"`
	LastModified string    `json:"last_modified"`
	ItemCount    int       `json:"item_count"`
	SiteURL      string    `json:"site_url"`
}

// itemRow is one item as the builder and the overview read it.
type itemRow struct {
	Title       string    `json:"title"`
	Link        string    `json:"link"`
	Excerpt     string    `json:"excerpt"`
	Published   time.Time `json:"published_at"` // zero = unknown
	FetchedAt   time.Time `json:"fetched_at"`
	FeedName    string    `json:"feed_name"`
	FeedURL     string    `json:"feed_url"`
	FeedSiteURL string    `json:"site_url"`
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func keyList(keys []string) string {
	raw, _ := json.Marshal(keys)
	return string(raw)
}

// syncFeeds makes the stored feeds match the settings: upsert name, url and
// group (state is kept), then delete feeds and items that are no longer
// configured. Upserts come first so a failure half way keeps data.
func syncFeeds(cfg []feedCfg) error {
	keys := make([]string, 0, len(cfg))
	for _, f := range cfg {
		keys = append(keys, f.Key)
		if err := dbExec(`INSERT INTO feeds (key, url, name, grp) VALUES ($1, $2, $3, $4)
ON CONFLICT (key) DO UPDATE SET url = EXCLUDED.url, name = EXCLUDED.name, grp = EXCLUDED.grp`,
			f.Key, f.URL, f.Name, f.Group); err != nil {
			return err
		}
	}
	list := keyList(keys)
	if err := dbExec(`DELETE FROM items WHERE feed_key NOT IN (SELECT jsonb_array_elements_text($1::jsonb))`, list); err != nil {
		return err
	}
	return dbExec(`DELETE FROM feeds WHERE key NOT IN (SELECT jsonb_array_elements_text($1::jsonb))`, list)
}

// feedRows returns the state of every stored feed, ordered by name.
func feedRows() ([]feedRow, error) {
	var rows []feedRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(c)), '[]'::jsonb) FROM (
SELECT key, url, name, grp, last_attempt, status, etag, last_modified, item_count, site_url
FROM feeds ORDER BY name, key) c`, &rows)
	return rows, err
}

// dueFeeds returns the configured feeds whose last attempt is older than
// every (or that were never attempted), oldest attempt first.
func dueFeeds(cfg []feedCfg, rows []feedRow, at time.Time, every time.Duration) []feedCfg {
	last := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		last[r.Key] = r.LastAttempt
	}
	var due []feedCfg
	for _, f := range cfg {
		if t := last[f.Key]; t.IsZero() || !t.Add(every).After(at) {
			due = append(due, f)
		}
	}
	sort.SliceStable(due, func(i, j int) bool { return last[due[i].Key].Before(last[due[j].Key]) })
	return due
}

// saveState records an attempt: its time, a fixed-phrase status, the
// validators for the next conditional GET and the stored item count.
func saveState(key, status, etag, lastMod, siteURL string, count int, attempt time.Time) error {
	return dbExec(`UPDATE feeds SET last_attempt = $2::timestamptz, status = $3, etag = $4, last_modified = $5,
site_url = $6, item_count = $7 WHERE key = $1`,
		key, stamp(attempt), status, etag, lastMod, siteURL, count)
}

// markDue makes feeds due at the next run (NULL sorts first).
func markDue(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	return dbExec(`UPDATE feeds SET last_attempt = NULL WHERE key IN (SELECT jsonb_array_elements_text($1::jsonb))`, keyList(keys))
}

type itemDoc struct {
	GUID      string  `json:"guid"`
	Title     string  `json:"title"`
	Link      string  `json:"link"`
	Excerpt   string  `json:"excerpt"`
	Published *string `json:"published_at"`
}

// upsertItems stores items in one statement. Items repeating a guid inside
// the batch are collapsed (the first wins) because one statement cannot
// touch a row twice. fetched_at keeps its first value.
func upsertItems(key string, items []item) error {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	docs := make([]itemDoc, 0, len(items))
	for _, it := range items {
		if seen[it.GUID] {
			continue
		}
		seen[it.GUID] = true
		d := itemDoc{GUID: it.GUID, Title: it.Title, Link: it.Link, Excerpt: it.Excerpt}
		if !it.Published.IsZero() {
			s := stamp(it.Published)
			d.Published = &s
		}
		docs = append(docs, d)
	}
	raw, _ := json.Marshal(docs)
	return dbExec(`INSERT INTO items (feed_key, guid, title, link, excerpt, published_at)
SELECT $1::text, r.guid, r.title, r.link, r.excerpt, r.published_at
FROM jsonb_to_recordset($2::jsonb) AS r(guid text, title text, link text, excerpt text, published_at timestamptz)
ON CONFLICT (feed_key, guid) DO UPDATE SET title = EXCLUDED.title, link = EXCLUDED.link,
excerpt = EXCLUDED.excerpt, published_at = EXCLUDED.published_at`, key, string(raw))
}

// prune keeps the newest min(maxItems, 200) items of a feed (by published
// date, else fetch time) and deletes items older than 90 days.
func prune(key string, maxItems int, at time.Time) error {
	keep := min(max(maxItems, 1), maxItemsCap)
	if err := dbExec(`DELETE FROM items WHERE feed_key = $1 AND guid NOT IN (
SELECT guid FROM items WHERE feed_key = $1 ORDER BY coalesce(published_at, fetched_at) DESC, guid LIMIT $2::integer)`,
		key, keep); err != nil {
		return err
	}
	cutoff := at.AddDate(0, 0, -retentionDays)
	return dbExec(`DELETE FROM items WHERE feed_key = $1 AND coalesce(published_at, fetched_at) < $2::timestamptz`,
		key, stamp(cutoff))
}

// sourceGroup maps a builder source name to a group: "items" is every feed
// (0), "group-N" is feeds with group N. ok is false for anything else.
func sourceGroup(source string) (group int, ok bool) {
	if source == "items" {
		return 0, true
	}
	n, found := strings.CutPrefix(source, "group-")
	if !found || len(n) != 1 || n[0] < '1' || n[0] > '0'+maxGroup {
		return 0, false
	}
	g, _ := strconv.Atoi(n)
	return g, true
}

// itemsFor returns the newest items of a builder source ("items" or
// "group-N"), at most limit (0 or more than 48 means 48). An unknown source
// has no items.
func itemsFor(source string, limit int) ([]itemRow, error) {
	group, ok := sourceGroup(source)
	if !ok {
		return nil, nil
	}
	if limit < 1 || limit > builderItemsMax {
		limit = builderItemsMax
	}
	var rows []itemRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(c)), '[]'::jsonb) FROM (
SELECT i.title, i.link, i.excerpt, i.published_at, i.fetched_at, f.name AS feed_name, f.url AS feed_url, f.site_url
FROM items i JOIN feeds f ON f.key = i.feed_key
WHERE ($1::integer = 0 OR f.grp = $1::integer)
ORDER BY coalesce(i.published_at, i.fetched_at) DESC, i.guid LIMIT $2::integer) c`, &rows, group, limit)
	return rows, err
}
