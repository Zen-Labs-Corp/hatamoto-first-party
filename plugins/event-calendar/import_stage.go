package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	maxRefPages      = 5
	maxExistingPages = 25
	bufferPage       = 100
	smallPageSize    = 50
)

const (
	scheduledCreateCap = 100 // standing_approval.create.max_per_hour; cron every 12h, so 200 a day
	manualCreateCap    = 200
	stepUpserts        = 50
	stepBudget         = 7 * time.Second
	pastGrace          = 24 * time.Hour // a scheduled run skips events that ended longer ago
)

// stageCursor is the checkpoint of the transform phase: the next record and
// the counts so far.
type stageCursor struct {
	I   int            `json:"i"`
	Sum map[string]int `json:"sum"`
}

// importTransformStep stages the buffered events a bounded number of upserts
// a step, in start order. A manual run creates what the site does not have (at
// most manualCreateCap) and updates what the feed changed. A scheduled run
// only creates (at most scheduledCreateCap, the rest waits for the next run):
// an update would wait for review and park the whole changeset. It deletes
// nothing.
func importTransformStep(s sdk.RunStep, sched bool) (sdk.RunStepResult, error) {
	started := clock()
	cfg := loadSettings()
	schema := cfg.slugs()[0]

	var cur stageCursor
	if s.Cursor != "" && json.Unmarshal([]byte(s.Cursor), &cur) != nil {
		return failRun("The run cursor could not be read.")
	}
	if cur.Sum == nil {
		failedSources, err := bufferCount(bufError)
		if err != nil {
			return failRun("The run buffer could not be read. Try again.")
		}
		cur.Sum = map[string]int{"created": 0, "updated": 0, "unchanged": 0, "with_links": 0, "failed": 0, "failed_sources": failedSources, "dropped_recurrence": 0, "skipped_over_cap": overCap(),
			"not_updated": 0, "unsafe": 0, "past": 0, "next_run": 0}
	}
	sum := cur.Sum
	createCap := manualCreateCap
	if sched {
		createCap = scheduledCreateCap
	}

	records, err := bufferedRecords()
	if err != nil {
		return failRun("The run buffer could not be read. Try again.")
	}
	if len(records) == 0 || cur.I >= len(records) {
		return finishImport(sum, createCap, sched)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].Ev.Start.Equal(records[j].Ev.Start) {
			return records[i].Ev.Start.Before(records[j].Ev.Start)
		}
		return records[i].Key < records[j].Key
	})

	existing, cut, err := loadImported(cfg)
	if err != nil {
		return failRun("The existing events could not be read. Nothing was staged.")
	}
	if cut && sched {
		// an event the listing did not reach would be staged as new, and core refuses a create for a key it already maps
		return failRun("The site has more imported events than a scheduled run can check. Nothing was staged. Use Run now.")
	}
	venues := namesToIDs("calendar-venue")
	cats := namesToIDs("calendar-category")
	cutoff := now().Add(-pastGrace)
	// a source renamed since the import keeps its events under the old name
	renamed := map[string]bool{}
	for uid, e := range existing {
		var kind string
		_ = json.Unmarshal(e.Attrs["source_kind"], &kind)
		renamed[uidTail(kind, uid)] = true
	}

	upserts := 0
	for ; cur.I < len(records); cur.I++ {
		if upserts >= stepUpserts || clock().Sub(started) >= stepBudget {
			cp, _ := json.Marshal(cur)
			if len(cp) > maxImportCursor || guest.RunCheckpoint(callHost, string(cp)) != nil {
				return failRun("The run could not save its place. Try again.")
			}
			return sdk.RunStepResult{}, nil
		}
		rec := records[cur.I]
		ev := rec.Ev
		if ev.Title == "" {
			ev.Title = "Untitled event"
		}
		old, found := existing[rec.Key]
		if !found && sched {
			found = renamed[uidTail(rec.Kind, rec.Key)]
		}
		switch {
		case found && sched:
			sum["not_updated"]++
			continue
		case !found && sched && endOf(ev).Before(cutoff):
			sum["past"]++
			continue
		case !found && sum["created"] >= createCap:
			sum["next_run"]++
			continue
		}
		sum["dropped_recurrence"] += ev.Dropped
		loc := cfg.location()
		if found {
			loc = storedLocation(old.Attrs, loc)
		}
		want := buildCreateAttrs(ev, loc, rec.Kind, rec.Key, venues[strings.ToLower(ev.Venue)], cats[strings.ToLower(rec.Category)])
		up := sdk.EntityUpsert{SourceKey: rec.Key, Schema: schema, Name: ev.Title}
		if found {
			up.Schema, up.Slug, up.Locale = old.Schema, old.Slug, old.Locale
			if sameImport(old.Attrs, want, loc) && old.Name == ev.Title {
				sum["unchanged"]++
				continue
			}
			attrs := cloneAttrs(old.Attrs)
			for k, v := range want {
				attrs[k] = v
			}
			want = attrs
		}
		if sched {
			// created without review: text and links that cannot park the changeset
			var clean bool
			if want, up.Name, clean = safeCreate(want, ev.Title); !clean {
				sum["unsafe"]++
				continue
			}
			up.Slug = scheduledSlug(up.Name, ev.Start.In(loc), rec.Key)
			up.Status = "published"
		}
		up.Attributes, _ = json.Marshal(want)
		upserts++
		if _, err := guest.EntityUpsert(callHost, up); err != nil {
			sum["failed"]++
			continue
		}
		if found {
			sum["updated"]++
		} else {
			sum["created"]++
		}
		if hasLink(ev) {
			sum["with_links"]++
		}
	}
	return finishImport(sum, createCap, sched)
}

// uidTail is the kind and the hash of the feed's UID that end an import key,
// whatever the source was called.
func uidTail(kind, key string) string {
	if len(key) > 16 {
		key = key[len(key)-16:]
	}
	return kind + "/" + key
}

// endOf is when an event ends: its end, else its start (a whole day for an
// all-day event without an end).
func endOf(ev importEvent) time.Time {
	switch {
	case !ev.End.IsZero():
		return ev.End
	case ev.AllDay:
		return ev.Start.AddDate(0, 0, 1)
	}
	return ev.Start
}

// storedLocation is the zone an event's stored times are read in: its own
// time_zone attribute when that names a zone, else site (as eventFromEntity).
func storedLocation(attrs map[string]json.RawMessage, site *time.Location) *time.Location {
	var tz string
	if json.Unmarshal(attrs["time_zone"], &tz) == nil {
		return zoneOr(tz, site)
	}
	return site
}

// hasLink reports an event that carries a link, which core never publishes on
// its own: its changeset waits for review.
func hasLink(ev importEvent) bool {
	return ev.TicketURL != "" || ev.OnlineURL != "" || ev.Website != "" || strings.Contains(ev.Description, "://")
}

// buildCreateAttrs is the attributes an imported event is created with, or
// overlaid on an existing one. Times are in the editor's form, in loc. It never sets source_url or image.
func buildCreateAttrs(ev importEvent, loc *time.Location, kind, key, venueID, catID string) map[string]json.RawMessage {
	a := map[string]json.RawMessage{}
	set := func(k string, v any) {
		switch x := v.(type) {
		case string:
			if x == "" {
				return
			}
		case nil:
			return
		}
		a[k], _ = json.Marshal(v)
	}
	// an all-day event keeps the feed's calendar date whatever zone it is read in
	stamp := func(t time.Time) string {
		if ev.AllDay {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Format(time.RFC3339)
		}
		return editorTime(t, loc)
	}
	set("start_at", stamp(ev.Start))
	if !ev.End.IsZero() {
		set("end_at", stamp(ev.End))
	}
	if ev.AllDay {
		set("all_day", true)
	}
	if ev.Status == "cancelled" {
		set("event_status", "cancelled")
	}
	set("description", ev.Description)
	set("ticket_url", ev.TicketURL)
	set("online_url", ev.OnlineURL)
	set("website", ev.Website)
	if venueID != "" {
		set("venue", []string{venueID})
	} else {
		set("venue_name", ev.Venue)
		set("venue_address", ev.Address)
		set("venue_city", ev.City)
	}
	if catID != "" {
		set("category", []string{catID})
	}
	for k, v := range ev.Repeat {
		set(k, v)
	}
	set("source_kind", kind)
	set("source_uid", key)
	return a
}

// sameImport reports whether every attribute the import wants is already what
// the event has. Times compare as instants, everything else as JSON values.
func sameImport(existing, want map[string]json.RawMessage, loc *time.Location) bool {
	for k, w := range want {
		e, ok := existing[k]
		if !ok {
			return false
		}
		if k == "start_at" || k == "end_at" {
			var ws, es string
			if json.Unmarshal(w, &ws) != nil || json.Unmarshal(e, &es) != nil {
				return false
			}
			wt, wok := parseTime(ws, loc)
			et, eok := parseTime(es, loc)
			if !wok || !eok || !wt.Equal(et) {
				return false
			}
			continue
		}
		var wv, ev any
		if json.Unmarshal(w, &wv) != nil || json.Unmarshal(e, &ev) != nil || !reflect.DeepEqual(wv, ev) {
			return false
		}
	}
	return true
}

// overCap is how many events the fetch phase left out because of the
// per-run cap; 0 when it left none or its note cannot be read.
func overCap() int {
	raw, found, err := guest.BufferGet(callHost, bufStat)
	var stat struct {
		Over int `json:"over"`
	}
	if err != nil || !found || json.Unmarshal(raw, &stat) != nil || stat.Over < 0 {
		return 0
	}
	return stat.Over
}

// bufferedRecords reads every event the fetch phase buffered, in key order.
func bufferedRecords() ([]importRecord, error) {
	var out []importRecord
	after := ""
	for {
		keys, next, err := guest.BufferList(callHost, bufEvent, after, bufferPage)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			raw, found, err := guest.BufferGet(callHost, k)
			if err != nil {
				return nil, err
			}
			var rec importRecord
			if found && json.Unmarshal(raw, &rec) == nil && rec.Key != "" {
				out = append(out, rec)
			}
		}
		if next == "" || len(keys) == 0 {
			return out, nil
		}
		after = next
	}
}

func bufferCount(prefix string) (int, error) {
	n, after := 0, ""
	for {
		keys, next, err := guest.BufferList(callHost, prefix, after, bufferPage)
		if err != nil {
			return 0, err
		}
		n += len(keys)
		if next == "" || len(keys) == 0 {
			return n, nil
		}
		after = next
	}
}

// importedEvent is an event on the site that an earlier import made.
type importedEvent struct {
	Schema, Slug, Locale, Name string
	Attrs                      map[string]json.RawMessage
}

// loadImported lists the published events whose source_kind is an import kind,
// by source_uid. cut is true when the listing stopped before its last page. A
// run cannot list drafts or deleted events (core: "a run reads published
// entities only"), so a key that is mapped but not published is not seen.
func loadImported(s settings) (out map[string]importedEvent, cut bool, err error) {
	out = map[string]importedEvent{}
	for si, schema := range s.slugs() {
		cursor, limit := "", listPageSize
		for page := 0; page < maxExistingPages; page++ {
			args := map[string]any{"schema_slug": schema, "status": "published", "limit": limit}
			if cursor != "" {
				args["cursor"] = cursor
			}
			var res struct {
				Items      []entityDTO `json:"items"`
				NextCursor string      `json:"next_cursor"`
			}
			err := callHost(sdk.FnEntitiesList, args, &res)
			if isInvalidArgument(err) && limit > smallPageSize {
				// the answer was over the host's size cap: read this page again in smaller pieces
				limit = smallPageSize
				args["limit"] = limit
				err = callHost(sdk.FnEntitiesList, args, &res)
			}
			if err != nil {
				if isNotFound(err) || si > 0 {
					break // the type does not exist, or the older type cannot be read: it has no imports
				}
				return nil, false, err
			}
			for _, raw := range res.Items {
				var attrs map[string]json.RawMessage
				if json.Unmarshal(raw.Attributes, &attrs) != nil {
					continue
				}
				ev := runEvent{Attrs: attrs}
				uid, kind := ev.attrText("source_uid"), ev.attrText("source_kind")
				if uid == "" || !slices.Contains(importKinds, kind) {
					continue
				}
				out[uid] = importedEvent{Schema: raw.SchemaSlug, Slug: raw.Slug, Locale: raw.Locale, Name: raw.Name, Attrs: attrs}
			}
			if res.NextCursor == "" {
				break
			}
			cursor = res.NextCursor
			cut = cut || page == maxExistingPages-1
		}
	}
	return out, cut, nil
}

// namesToIDs maps the lower-case names of the published entities of one type
// to their ids. A type that cannot be read gives an empty map: the event keeps
// the venue as text.
func namesToIDs(schema string) map[string]string {
	out := map[string]string{}
	cursor := ""
	for page := 0; page < maxRefPages; page++ {
		args := map[string]any{"schema_slug": schema, "status": "published", "limit": listPageSize}
		if cursor != "" {
			args["cursor"] = cursor
		}
		var res struct {
			Items      []entityDTO `json:"items"`
			NextCursor string      `json:"next_cursor"`
		}
		if callHost(sdk.FnEntitiesList, args, &res) != nil {
			break
		}
		for _, raw := range res.Items {
			name := strings.ToLower(strings.TrimSpace(raw.Name))
			if _, dup := out[name]; name != "" && !dup {
				out[name] = raw.ID
			}
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return out
}

// finishImport ends the run. Core shows a run's progress message but not its
// summary, so the counts the owner needs are also put in the message.
func finishImport(sum map[string]int, createCap int, sched bool) (sdk.RunStepResult, error) {
	done := sum["created"] + sum["updated"]
	skipped := sum["unchanged"] + sum["not_updated"] + sum["past"] + sum["next_run"] + sum["failed"] + sum["skipped_over_cap"] + sum["unsafe"]
	msg := fmt.Sprintf("Imported %d events, %d skipped", done, skipped)
	if n := sum["next_run"]; n > 0 {
		msg += fmt.Sprintf("; %d left for the next run (at most %d new events a run)", n, createCap)
	}
	if n := sum["failed_sources"]; n > 0 {
		msg += fmt.Sprintf("; %d calendars could not be read", n)
	}
	if n := sum["dropped_recurrence"]; n > 0 {
		msg += fmt.Sprintf("; %d repeating rules left out", n)
	}
	if sched {
		msg += "; an imported event that was deleted or unpublished and is still in a feed holds back publishing until it leaves the feed"
	}
	_ = guest.RunProgress(callHost, done+skipped, done+skipped, msg)
	return sdk.RunStepResult{}, guest.RunFinish(callHost, sum)
}
