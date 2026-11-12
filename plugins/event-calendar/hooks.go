package main

import (
	"errors"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	reconcileBudget    = 8 * time.Second // the cron call may run 10 s
	reconcileMapBudget = 6 * time.Second // leaves room for geocoding
	geocodeDeadline    = 9 * time.Second
	reconcilePages     = 10 // 2000 events per run
)

func init() {
	guest.OnAction(sdk.EntityCreated, onEntity)
	guest.OnAction(sdk.EntityUpdated, onEntity)
	guest.OnAction(sdk.EntityPublished, onEntity)
	guest.OnAction(sdk.EntityDeleted, onEntityDeleted)
	guest.OnAction(sdk.ImportCompleted, onImport)
	guest.OnCron("reconcile", onReconcile)
}

// The hooks never return an error: a failing index must not fail the save or
// import that triggered them. The hourly reconcile repairs what they missed.

// onEntity indexes a created, updated or published entity, or drops it from
// the index when it is no longer a published event. Entity hooks fire for
// every content type, so other types return before any index work.
func onEntity(ev sdk.EntityEvent) error {
	s := loadSettings()
	if !s.accepts(ev.SchemaSlug) || ev.ID == "" {
		return nil
	}
	_, _ = reindexEntity(ev.ID, s)
	return nil
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
// of events that are gone. It stops at the first failure or when until has
// passed and then deletes nothing. It returns the addresses a map still needs
// looked up. Running it again repeats the same writes.
func reconcileOnce(s settings, until time.Time) (addrs []string, err error) {
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
				if !clock().Before(until) {
					complete = false
					break listing
				}
				_, addr, werr := reindexDTO(raw, s)
				if werr != nil {
					return addrs, errors.Join(err, werr)
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
	for id := range indexed {
		if !seen[id] {
			err = errors.Join(err, deleteEvent(id))
		}
	}
	return addrs, err
}
