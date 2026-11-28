package main

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// Where each import source stands, for the Imports tab. A run cannot use the
// database, so the owner's tab keeps the table import_progress (migration
// 007): it counts what the site holds per source, finds the place the next
// manual run continues from, and counts the feed once an hour at most.

const countFetchMs = 6000 // one fetch of a count must fit the route's time

// importMark is a place in a source's events: events start in order, a tie
// goes by key. A run continues after it.
type importMark struct {
	Start time.Time
	Key   string
}

// later reports whether m comes after o.
func (m importMark) later(o importMark) bool {
	return m.Start.After(o.Start) || m.Start.Equal(o.Start) && m.Key > o.Key
}

// covers reports whether an event at (start, key) is at or before the mark.
func (m importMark) covers(start time.Time, key string) bool {
	return start.Before(m.Start) || start.Equal(m.Start) && key <= m.Key
}

// String is the text a run takes: "<RFC3339 UTC>|<key>", empty for no mark.
func (m importMark) String() string {
	if m.Key == "" {
		return ""
	}
	return m.Start.UTC().Format(time.RFC3339) + "|" + m.Key
}

// sourceStats is what the index and the drafts say about one source.
type sourceStats struct {
	Last        *string    `json:"last"`
	New         *string    `json:"last_new"`
	Today       int        `json:"today"`
	Imported    int        `json:"imported"`
	CursorStart *time.Time `json:"cursor_start"`
	CursorKey   string     `json:"cursor_key"`
}

// mark is the latest imported event of the source: the index's, then
// the drafts' when later.
func (st sourceStats) mark() importMark {
	if st.CursorStart == nil || st.CursorKey == "" {
		return importMark{}
	}
	return importMark{Start: *st.CursorStart, Key: st.CursorKey}
}

// withDrafts adds the drafts of the source (kind and key prefix) to the stats.
func (st *sourceStats) withDrafts(kind, prefix string, drafts []draftRow) {
	m := st.mark()
	for _, d := range drafts {
		if d.SourceKind != kind || !strings.HasPrefix(d.SourceUID, prefix) {
			continue
		}
		st.Imported++
		if dm := (importMark{Start: d.Start, Key: d.SourceUID}); d.HasStart && dm.later(m) {
			m = dm
		}
	}
	if m.Key != "" {
		start := m.Start
		st.CursorStart, st.CursorKey = &start, m.Key
	}
}

// storedCount is the last count of a feed, as import_progress keeps it.
type storedCount struct {
	Found       *int    `json:"found"`
	FoundCapped bool    `json:"found_capped"`
	Remaining   *int    `json:"remaining"`
	CountedAt   *string `json:"counted_at"`
}

const (
	upsertProgressSQL = `INSERT INTO import_progress (source_key, imported, cursor_start, cursor_key, updated_at)
VALUES ($1, $2::int, $3::timestamptz, $4, now())
ON CONFLICT (source_key) DO UPDATE SET imported = EXCLUDED.imported, cursor_start = EXCLUDED.cursor_start,
 cursor_key = EXCLUDED.cursor_key, updated_at = now()`
	upsertCountSQL = `INSERT INTO import_progress (source_key, imported, cursor_start, cursor_key, found, found_capped, remaining, counted_at, updated_at)
VALUES ($1, $2::int, $3::timestamptz, $4, $5::int, $6::boolean, $7::int, now(), now())
ON CONFLICT (source_key) DO UPDATE SET imported = EXCLUDED.imported, cursor_start = EXCLUDED.cursor_start,
 cursor_key = EXCLUDED.cursor_key, found = EXCLUDED.found, found_capped = EXCLUDED.found_capped,
 remaining = EXCLUDED.remaining, counted_at = now(), updated_at = now()`
	readProgressSQL = `SELECT coalesce(jsonb_object_agg(source_key, jsonb_build_object('found', found, 'found_capped', found_capped,
 'remaining', remaining, 'counted_at', counted_at)), '{}'::jsonb) FROM import_progress
WHERE source_key IN (SELECT jsonb_array_elements_text($1::jsonb))`
	pruneProgressSQL = `DELETE FROM import_progress WHERE source_key NOT IN (SELECT jsonb_array_elements_text($1::jsonb))`
)

// startArg is the cursor start as a statement argument: nil for none.
func startArg(st sourceStats) any {
	if st.CursorStart == nil {
		return nil
	}
	return utcText(*st.CursorStart)
}

// storeProgress keeps what the site holds of a source. A failure is ignored:
// the tab still shows the numbers it just read.
func storeProgress(key string, st sourceStats) {
	_ = dbExec(upsertProgressSQL, key, st.Imported, startArg(st), st.CursorKey)
}

// storeCount keeps the count of a feed together with the progress.
func storeCount(key string, st sourceStats, found, remaining int, capped bool) {
	_ = dbExec(upsertCountSQL, key, st.Imported, startArg(st), st.CursorKey, found, capped, remaining)
}

// readCounts reads the stored counts of the given sources by key; none when
// the table cannot be read.
func readCounts(keys []string) map[string]storedCount {
	out := map[string]storedCount{}
	_ = dbJSON(readProgressSQL, &out, jsonText(keys))
	return out
}

// pruneProgress drops the rows of sources no longer in the settings.
func pruneProgress(keys []string) {
	_ = dbExec(pruneProgressSQL, jsonText(keys))
}

// sourceProgress reads the stats of one source from the index and adds the
// drafts. ok is false when the index could not be read.
func sourceProgress(r settingsSource, name string, drafts []draftRow) (sourceStats, bool) {
	var st sourceStats
	prefix := importPrefix(r.Kind, name)
	if err := dbJSON(sourceStatsSQL, &st, prefix, r.Kind); err != nil {
		return st, false
	}
	st.withDrafts(r.Kind, prefix, drafts)
	return st, true
}

// countSource reads the feed of a source once, in one fetch, and counts its
// events and those after the mark. An Eventbrite organization is read for its
// first page only, so capped is true when it has more. why is a short reason
// without the address when the feed could not be read.
func countSource(src importSource, s settings, mark importMark, imported int) (found, remaining int, capped bool, why string) {
	var events []importEvent
	total := -1
	switch {
	case src.Kind == "eventbrite" && src.OrganizationID != "":
		var next string
		events, next, total, why = fetchEventbritePageWithin(src.OrganizationID, "", eventbriteToken(), countFetchMs)
		// The API's own total is the real one; without it the first page is a floor.
		capped = next != "" && total < 0
	case src.URL == "":
		why = "the source has no usable address"
	case !slices.Contains(importKinds, src.Kind):
		why = "the source kind is not known"
	default:
		events, why = fetchEventsWithin(src, s, countFetchMs)
	}
	if why != "" {
		return 0, 0, false, why
	}
	seen := map[string]bool{}
	for _, ev := range events {
		key := importKey(src.Kind, src.Name, ev.UID)
		if seen[key] {
			continue
		}
		seen[key] = true
		if found >= maxReadEvents {
			capped = true
			break
		}
		found++
		if mark.Key == "" || !mark.covers(ev.Start, key) {
			remaining++
		}
	}
	if total >= 0 && !capped {
		found = total
		// Pages after the first were not read: what the site does not hold yet is open.
		if next := total - imported; next > remaining && len(events) < total {
			remaining = next
		}
	}
	return found, remaining, capped, ""
}

// serveCount answers GET /admin/imports?count=<name>: the progress of that
// source with a fresh count of its feed. A feed that cannot be read leaves the
// row as it was, with count_error naming the reason.
func serveCount(name string) sdk.HTTPResponse {
	s := loadSettings()
	hasToken := eventbriteToken() != ""
	for _, r := range loadSources() {
		src := r.source()
		if !r.enabled() || src.Name != name {
			continue
		}
		row := importRowOf(r, src, hasToken)
		var drafts []draftRow
		if rows, _, err := scanDrafts(s, "", clock().Add(draftBudget)); err == nil {
			drafts = rows
		}
		st, ok := sourceProgress(r, src.Name, drafts)
		if !ok {
			return jsonReply(http.StatusServiceUnavailable, map[string]string{"error": "try again"})
		}
		key := importPrefix(r.Kind, src.Name)
		out := countAnswer{}
		found, remaining, capped, why := countSource(src, s, st.mark(), st.Imported)
		if why != "" {
			storeProgress(key, st)
			out.CountError = why
		} else {
			storeCount(key, st, found, remaining, capped)
		}
		fillProgress(&row, st, readCounts([]string{key})[key])
		out.Source = row
		return jsonReply(http.StatusOK, out)
	}
	return jsonReply(http.StatusNotFound, map[string]string{"error": "not found"})
}

type countAnswer struct {
	Source     importRow `json:"source"`
	CountError string    `json:"count_error,omitempty"`
}
