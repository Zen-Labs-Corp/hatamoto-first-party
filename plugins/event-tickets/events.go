package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // zone names work without a filesystem

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.EntityCreated, onEntity)
	guest.OnAction(sdk.EntityUpdated, onEntity)
	guest.OnAction(sdk.EntityPublished, onEntity)
	guest.OnAction(sdk.EntityDeleted, onEntityDeleted)
}

var eventIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// idOK reports whether id can be an event id: the ones that come from a
// request are checked before they reach a host call.
func idOK(id string) bool { return eventIDRe.MatchString(id) }

// entityDTO is the part of the host's entity JSON this plugin reads.
type entityDTO struct {
	ID         string          `json:"id"`
	SchemaSlug string          `json:"schema_slug"`
	Slug       string          `json:"slug"`
	Locale     string          `json:"locale"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	URL        string          `json:"url"`
	Attributes json.RawMessage `json:"attributes"`
	UpdatedAt  string          `json:"updated_at"`
}

// event is what tickets need to know about an event entity. Start is zero
// when the entity has no readable start.
type event struct {
	ID          string
	SchemaSlug  string
	Name        string
	URL         string
	Status      string // of the entity
	EventStatus string // scheduled, cancelled or postponed
	Mode        string // offline, online or hybrid
	OnlineURL   string
	AllDay      bool
	Start       time.Time
	End         time.Time
	HasEnd      bool
	JoinPublic  bool
}

// readEvent reads one event entity. It reports false, with no error, when the
// entity is gone, is not of an accepted type or is not published.
func readEvent(id string, s settings) (event, bool, error) {
	var raw entityDTO
	if err := callHost(sdk.FnEntitiesGet, map[string]any{"id": id}, &raw); err != nil {
		var he *sdk.HostError
		if errors.As(err, &he) && he.Code == sdk.ErrNotFound {
			return event{}, false, nil
		}
		return event{}, false, err
	}
	if raw.ID == "" || !s.accepts(raw.SchemaSlug) || raw.Status != "published" {
		return event{}, false, nil
	}
	var attrs map[string]any
	if len(raw.Attributes) > 0 && json.Unmarshal(raw.Attributes, &attrs) != nil {
		attrs = nil
	}
	loc := time.UTC
	if tz := attrString(attrs, "time_zone"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	e := event{
		ID: raw.ID, SchemaSlug: raw.SchemaSlug, Name: raw.Name, URL: raw.URL, Status: raw.Status,
		EventStatus: attrString(attrs, "event_status"),
		Mode:        attrString(attrs, "attendance_mode"),
		OnlineURL:   attrString(attrs, "online_url"),
		AllDay:      attrBool(attrs, "all_day"),
		JoinPublic:  joinPublic(attrs),
	}
	if start, ok := parseTime(attrString(attrs, "start_at"), loc); ok {
		e.Start = start
		if end, ok := parseTime(attrString(attrs, "end_at"), loc); ok && !end.Before(start) {
			e.End, e.HasEnd = end, true
		}
	}
	return e, true, nil
}

// parseTime reads the stored time layouts: a date, a local date and time
// (with or without seconds) in loc, or RFC 3339 with an offset.
func parseTime(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// attrString returns a string attribute, empty when missing or not a string.
func attrString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// attrBool reads a boolean attribute, also accepting the string "true".
func attrBool(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// joinPublic reads attributes.join_link_public. Absent means public; only an
// explicit false (also the text "false", "0" or "no") makes the join link
// private to the ticket page and the confirmation email.
func joinPublic(attrs map[string]any) bool {
	switch v := attrs["join_link_public"].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "false", "0", "no":
			return false
		}
	}
	return true
}

// endsAt is when the event is over: its end, else its start; an all-day
// event lasts until the end of its last day.
func (e event) endsAt() time.Time {
	end := e.Start
	if e.HasEnd {
		end = e.End
	}
	if e.AllDay {
		y, m, d := end.Date()
		end = time.Date(y, m, d+1, 0, 0, 0, 0, end.Location())
	}
	return end
}

// open reports whether tickets can still be had at the given time: the event
// is published, not cancelled or postponed, has a start and is not over.
func (e event) open(at time.Time) bool {
	if e.Status != "published" || e.Start.IsZero() {
		return false
	}
	switch e.EventStatus {
	case "cancelled", "postponed":
		return false
	}
	return e.endsAt().After(at)
}

// originOf is the scheme and host mails link to: the one of the event's
// absolute address, else the site_url setting, else "" (path-only links).
func originOf(entityURL, siteURL string) string {
	for _, raw := range []string{entityURL, siteURL} {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

// snapshotEvent records the dates of an event that has ticket types, so the
// hourly job can tell which events ended. The join link is never stored.
func snapshotEvent(e event) error {
	var starts, ends any
	if !e.Start.IsZero() {
		starts = e.Start.UTC().Format(time.RFC3339)
	}
	if e.HasEnd {
		ends = e.End.UTC().Format(time.RFC3339)
	}
	return dbExec(`INSERT INTO events (entity_id, schema_slug, name, starts_at, ends_at, status)
VALUES ($1, $2, $3, $4::timestamptz, $5::timestamptz, $6)
ON CONFLICT (entity_id) DO UPDATE SET schema_slug = EXCLUDED.schema_slug, name = EXCLUDED.name,
    starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, status = EXCLUDED.status, seen_at = now()`,
		e.ID, e.SchemaSlug, e.Name, starts, ends, e.Status)
}

// hasTypes reports whether the event has any ticket type.
func hasTypes(eventID string) (bool, error) {
	var has bool
	err := dbJSON(`SELECT to_jsonb(EXISTS(SELECT 1 FROM ticket_types WHERE event_id = $1))`, &has, eventID)
	return has, err
}

// The hooks never return an error: a failing snapshot must not fail the save
// that triggered them. The hourly job repairs what they missed.

// onEntity refreshes the snapshot of an event that has ticket types. Entity
// hooks fire for every content type, so other types return before any work.
func onEntity(ev sdk.EntityEvent) error {
	s := loadSettings()
	if !s.accepts(ev.SchemaSlug) || ev.ID == "" {
		return nil
	}
	if has, err := hasTypes(ev.ID); err != nil || !has {
		return nil
	}
	e, ok, err := readEvent(ev.ID, s)
	switch {
	case err != nil:
	case ok:
		_ = snapshotEvent(e)
	default:
		_ = dbExec(`UPDATE events SET status = 'unpublished', seen_at = now() WHERE entity_id = $1`, ev.ID)
	}
	return nil
}

// onEntityDeleted closes the sale of a deleted event; the orders stay.
func onEntityDeleted(ev sdk.EntityEvent) error {
	s := loadSettings()
	if !s.accepts(ev.SchemaSlug) || ev.ID == "" {
		return nil
	}
	_ = dbExec(`UPDATE events SET status = 'deleted', seen_at = now() WHERE entity_id = $1`, ev.ID)
	_ = dbExec(`UPDATE ticket_types SET on_sale = false, touched_at = now() WHERE event_id = $1`, ev.ID)
	return nil
}
