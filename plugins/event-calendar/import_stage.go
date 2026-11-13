package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	maxRefPages      = 5
	maxExistingPages = 25
	bufferPage       = 100
)

// importTransformStep stages the buffered events: a create for an event the
// site does not have, an update for one whose feed data changed, nothing for
// an unchanged one. It deletes nothing.
func importTransformStep() (sdk.RunStepResult, error) {
	s := loadSettings()
	schema := s.slugs()[0]

	records, err := bufferedRecords()
	if err != nil {
		return failRun("The run buffer could not be read. Try again.")
	}
	failedSources, err := bufferCount(bufError)
	if err != nil {
		return failRun("The run buffer could not be read. Try again.")
	}
	sum := map[string]int{"created": 0, "updated": 0, "unchanged": 0, "with_links": 0, "failed": 0, "failed_sources": failedSources, "dropped_recurrence": 0, "skipped_over_cap": overCap()}
	if len(records) == 0 {
		return finishImport(sum)
	}

	existing, err := loadImported(s)
	if err != nil {
		return failRun("The existing events could not be read. Nothing was staged.")
	}
	venues := namesToIDs("calendar-venue")
	cats := namesToIDs("calendar-category")

	for _, rec := range records {
		ev := rec.Ev
		if ev.Title == "" {
			ev.Title = "Untitled event"
		}
		sum["dropped_recurrence"] += ev.Dropped
		old, found := existing[rec.Key]
		loc := s.location()
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
		up.Attributes, _ = json.Marshal(want)
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
	return finishImport(sum)
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
			return t.Format("2006-01-02") + "T00:00"
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
// by source_uid.
func loadImported(s settings) (map[string]importedEvent, error) {
	out := map[string]importedEvent{}
	for si, schema := range s.slugs() {
		cursor := ""
		for page := 0; page < maxExistingPages; page++ {
			args := map[string]any{"schema_slug": schema, "status": "published", "limit": listPageSize}
			if cursor != "" {
				args["cursor"] = cursor
			}
			var res struct {
				Items      []entityDTO `json:"items"`
				NextCursor string      `json:"next_cursor"`
			}
			if err := callHost(sdk.FnEntitiesList, args, &res); err != nil {
				if isNotFound(err) || si > 0 {
					break // the type does not exist, or the older type cannot be read: it has no imports
				}
				return nil, err
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
		}
	}
	return out, nil
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
func finishImport(sum map[string]int) (sdk.RunStepResult, error) {
	read := sum["created"] + sum["updated"] + sum["unchanged"] + sum["failed"]
	msg := fmt.Sprintf("%d events read", read)
	if n := sum["skipped_over_cap"]; n > 0 {
		msg += fmt.Sprintf(", %d left out by the %d-event limit", n, maxImportEvents)
	}
	if n := sum["dropped_recurrence"]; n > 0 {
		msg += fmt.Sprintf(", %d repeating rules left out", n)
	}
	_ = guest.RunProgress(callHost, read, read, msg)
	return sdk.RunStepResult{}, guest.RunFinish(callHost, sum)
}
