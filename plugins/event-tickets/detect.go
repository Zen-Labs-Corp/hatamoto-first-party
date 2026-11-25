package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// eventSig is what the plugin compares between two sightings of an event.
// Venue is nil for a snapshot written before venues were recorded.
type eventSig struct {
	Starts, Ends time.Time
	Venue        *string
	Status       string
	Seen         string // when the stored snapshot was written; empty for a new signature
}

// sigOf is the signature snapshotEvent is about to store for e.
func sigOf(e event) eventSig {
	s := eventSig{Venue: &e.Venue, Status: e.Status}
	if !e.Start.IsZero() {
		s.Starts, s.Ends = e.Start.UTC(), e.endsAt().UTC()
	}
	if e.EventStatus == "cancelled" || e.EventStatus == "postponed" {
		s.Status = e.EventStatus
	}
	return s
}

// readSnapshot returns the stored signature of an event, nil when it was
// never seen.
func readSnapshot(id string) (*eventSig, error) {
	var raw *struct {
		Starts *string `json:"starts"`
		Ends   *string `json:"ends"`
		Venue  *string `json:"venue"`
		Status string  `json:"status"`
		Seen   string  `json:"seen"`
	}
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('starts', starts_at, 'ends', ends_at, 'venue', venue_key, 'status', status, 'seen', seen_at)
FROM events WHERE entity_id = $1), 'null'::jsonb)`, &raw, id)
	if err != nil || raw == nil {
		return nil, err
	}
	s := &eventSig{Venue: raw.Venue, Status: raw.Status, Seen: raw.Seen}
	s.Starts = parseStamp(raw.Starts)
	s.Ends = parseStamp(raw.Ends)
	return s, nil
}

func parseStamp(s *string) time.Time {
	if s == nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func liveStatus(s string) bool { return s == "published" || s == "cancelled" || s == "postponed" }

// diffSig lists what a guest would care about between two sightings: "date",
// "venue" and "status". It is empty for a first sighting, for a snapshot from
// before venues were recorded and for an event that has already started.
func diffSig(old *eventSig, nw eventSig, at time.Time) []string {
	if old == nil || old.Venue == nil || !liveStatus(old.Status) {
		return nil // an event coming back from unpublished or deleted is not a change
	}
	if !old.Starts.After(at) && !nw.Starts.After(at) {
		return nil
	}
	var what []string
	if !old.Starts.IsZero() && (!old.Starts.Equal(nw.Starts) || !old.Ends.Equal(nw.Ends)) {
		what = append(what, "date")
	}
	if nw.Venue != nil && *old.Venue != *nw.Venue {
		what = append(what, "venue")
	}
	if liveStatus(nw.Status) && old.Status != nw.Status {
		what = append(what, "status")
	}
	return what
}

func (s eventSig) text() string {
	venue := "-"
	if s.Venue != nil {
		venue = *s.Venue
	}
	return strings.Join([]string{s.Starts.Format(time.RFC3339), s.Ends.Format(time.RFC3339), venue, s.Status}, "|")
}

// changeKey identifies one transition, so the same change seen by the hook
// and by the hourly refresh is recorded once. The time the old snapshot was
// written tells a repeat of an earlier transition (postponed, restored,
// postponed again) from a second sighting of the same one: both observers of
// one transition read the same old row.
func changeKey(old *eventSig, nw eventSig) string {
	sum := sha256.Sum256([]byte(old.text() + "\n" + nw.text() + "\n" + old.Seen))
	return hex.EncodeToString(sum[:])[:32]
}

// observeEvent records a marker when the event differs from its snapshot, then
// refreshes the snapshot. It never sends mail: the emails job does that.
func observeEvent(e event) error {
	nw := sigOf(e)
	if old, err := readSnapshot(e.ID); err == nil {
		if what := diffSig(old, nw, now()); len(what) > 0 {
			_ = dbExec(`INSERT INTO event_changes (event_id, change_key, what) VALUES ($1, $2, $3)
ON CONFLICT (event_id, change_key) DO NOTHING`, e.ID, changeKey(old, nw), strings.Join(what, ","))
		}
	}
	return snapshotEvent(e)
}
