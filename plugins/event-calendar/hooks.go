package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	reconcileBudget    = 8 * time.Second // the cron call may run 10 s
	reconcileMapBudget = 6 * time.Second // leaves room for geocoding
	geocodeDeadline    = 9 * time.Second
	hookDeadline       = 3500 * time.Millisecond // from hook start; the entity hooks may run 5 s
	reconcilePages     = 10                      // 2000 events per run
)

func init() {
	guest.OnAction(sdk.EntityCreated, onEntity)
	guest.OnAction(sdk.EntityUpdated, onEntity)
	guest.OnAction(sdk.EntityPublished, onEntity)
	guest.OnAction(sdk.EntityDeleted, onEntityDeleted)
	guest.OnAction(sdk.ImportCompleted, onImport)
	guest.OnCron("reconcile", onReconcile)
	guest.OnCron("prune", onPrune)
}

// The hooks never return an error: a failing index must not fail the save or
// import that triggered them. The hourly reconcile repairs what they missed.

// onEntity indexes a created, updated or published entity, or drops it from
// the index when it is no longer a published event. Entity hooks fire for
// every content type, so other types return before any index work.
func onEntity(ev sdk.EntityEvent) error {
	started := clock()
	s := loadSettings()
	if !s.accepts(ev.SchemaSlug) || ev.ID == "" {
		return nil
	}
	raw, _, addr, _ := reindexEntityRaw(ev.ID, s)
	markSubmissionPublished(raw)
	if addr != "" {
		geocodeOne(s, addr, started.Add(hookDeadline))
	}
	// A new site has no completed reconcile yet and the JSON route stays
	// empty until one has: run it now instead of waiting for the hourly one,
	// but at most once per 10 minutes, so a burst of saves on a site
	// that never completes does not repeat it.
	if !indexReady() && claimReconcile() {
		_, _ = reconcileOnce(s, started.Add(hookDeadline))
	}
	return nil
}

var submissionUIDRe = regexp.MustCompile(`^sub-([0-9]{1,18})$`)

// markSubmissionPublished records that the event staged from a submission is
// live. Core says nothing about which hook a changeset's entity fires, so
// every created, updated or published event is looked at; only a published
// event with source_kind submission and source_uid sub-<id> counts. A failure
// (no db grant, no table yet) is ignored.
func markSubmissionPublished(raw entityDTO) {
	if raw.Status != "published" || len(raw.Attributes) == 0 {
		return
	}
	var a map[string]any
	if json.Unmarshal(raw.Attributes, &a) != nil || attrString(a, "source_kind") != "submission" {
		return
	}
	m := submissionUIDRe.FindStringSubmatch(attrString(a, "source_uid"))
	if m == nil {
		return
	}
	if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
		setSubmissionStatus(id, "published", "pending", "approved")
	}
}

// maxDigestTitles is how many titles the digest mail lists.
const maxDigestTitles = 20

// onPrune is the hourly clean-up of the submissions table and, when the owner
// set notify_email, one mail about the submissions the per-submission mails
// did not cover (over the hourly cap or failed). It lists titles only. A mail
// that fails leaves the rows for the next run.
func onPrune(sdk.CronEvent) error {
	_ = pruneSubmissions(now())
	to := notifyEmail()
	if to == "" {
		return nil
	}
	rows, err := unnotified()
	if err != nil || len(rows) == 0 {
		return nil
	}
	lines := []string{strconv.Itoa(len(rows)) + " new event submissions are waiting:", ""}
	ids := make([]int64, 0, len(rows))
	for i, r := range rows {
		ids = append(ids, r.ID)
		if i < maxDigestTitles {
			lines = append(lines, "- "+r.Title)
		}
	}
	if len(rows) > maxDigestTitles {
		lines = append(lines, "- and "+strconv.Itoa(len(rows)-maxDigestTitles)+" more")
	}
	lines = append(lines, "", "Review them in the Event calendar plugin page, Submissions tab.")
	args := map[string]any{"to": []string{to}, "subject": "New event submissions", "text": strings.Join(lines, "\n")}
	if callHost(sdk.FnMailSend, args, nil) == nil {
		_ = markNotified(ids...)
	}
	return nil
}

// reconcileKey is the geocode_cache row that holds the time of the last
// reconcile a hook started, shared by every instance of the plugin.
const reconcileKey = "!last-hook-reconcile"

// claimReconcile reserves the right to run a reconcile from a hook now: true
// when the last one started at least 10 minutes ago. It is atomic in
// the database.
func claimReconcile() bool {
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost(sdk.FnDBExec, map[string]any{"sql": `INSERT INTO geocode_cache (address, status, fetched_at)
VALUES ($1, 'failed', now())
ON CONFLICT (address) DO UPDATE SET fetched_at = now()
 WHERE geocode_cache.fetched_at <= now() - interval '10 minutes'`, "args": []any{reconcileKey}}, &res)
	return err == nil && res.RowsAffected > 0
}

// onEntityDeleted drops a deleted event; the entity may no longer be readable.
func onEntityDeleted(ev sdk.EntityEvent) error {
	if s := loadSettings(); s.accepts(ev.SchemaSlug) && ev.ID != "" {
		_ = deleteEvent(ev.ID)
	}
	return nil
}

func onImport(sdk.ImportEvent) error {
	_, _ = reconcileOnce(loadSettings(), clock().Add(reconcileBudget))
	return nil
}

func onReconcile(sdk.CronEvent) error {
	started := clock()
	s := loadSettings()
	budget := reconcileBudget
	if s.ShowMap {
		budget = reconcileMapBudget
	}
	addrs, _ := reconcileOnce(s, started.Add(budget))
	geocodeAddresses(s, addrs, started.Add(geocodeDeadline))
	return nil
}

// reconcileOnce lists the published events of every accepted type, writes
// each to the index and, when the whole listing was read, removes index rows
// of events that are gone. A row that fails to write is skipped; after that,
// or when the listing fails or until has passed, it deletes nothing. It
// returns the addresses a map still needs looked up. Running it again repeats
// the same writes.
func reconcileOnce(s settings, until time.Time) (addrs []string, err error) {
	started := clock()
	seen := map[string]bool{}
	pages := 0
	complete := true
listing:
	for _, slug := range s.slugs() {
		cursor := ""
		for {
			if pages >= reconcilePages || !clock().Before(until) {
				complete = false
				break listing
			}
			pages++
			args := map[string]any{"schema_slug": slug, "status": "published", "limit": listPageSize}
			if cursor != "" {
				args["cursor"] = cursor
			}
			var out struct {
				Items      []entityDTO `json:"items"`
				NextCursor string      `json:"next_cursor"`
			}
			if lerr := callHost(sdk.FnEntitiesList, args, &out); lerr != nil {
				if isNotFound(lerr) {
					break // the type does not exist: it has no events
				}
				return addrs, errors.Join(err, lerr)
			}
			for _, raw := range out.Items {
				if !s.accepts(raw.SchemaSlug) || raw.Status != "published" {
					continue
				}
				seen[raw.ID] = true
				// a changeset fires no per-entity hook: catch up here
				markSubmissionPublished(raw)
				if !clock().Before(until) {
					complete = false
					break listing
				}
				_, addr, werr := reindexDTO(raw, s, &started)
				if werr != nil {
					// one bad row must not stop the rest; delete nothing after it
					err = errors.Join(err, werr)
					complete = false
					continue
				}
				if addr != "" {
					addrs = append(addrs, addr)
				}
			}
			if out.NextCursor == "" {
				break
			}
			cursor = out.NextCursor
		}
	}
	if !complete {
		return addrs, err
	}
	indexed, ierr := indexedIDs()
	if ierr != nil {
		return addrs, ierr
	}
	for id, updated := range indexed {
		// a row written after this run began was not in the listing
		if !seen[id] && !writtenSince(updated, started) {
			err = errors.Join(err, deleteEvent(id))
		}
	}
	if err == nil {
		err = markReconciled()
	}
	return addrs, err
}

// writtenSince reports whether an updated_at from the index is at or after t.
// An unreadable value counts as old, as before this check existed.
func writtenSince(updated string, t time.Time) bool {
	u, err := time.Parse(time.RFC3339Nano, updated)
	return err == nil && !u.Before(t)
}
