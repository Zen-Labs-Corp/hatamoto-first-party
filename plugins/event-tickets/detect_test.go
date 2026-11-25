package main

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// gigEntity starts 2027-03-01T19:00Z and ends 21:00Z, with no venue.
const (
	snapSame    = `{"starts":"2027-03-01T19:00:00+00:00","ends":"2027-03-01T21:00:00+00:00","venue":"","status":"published"}`
	snapOldDate = `{"starts":"2027-02-28T19:00:00+00:00","ends":"2027-02-28T21:00:00+00:00","venue":"","status":"published"}`
	snapNoVenue = `{"starts":"2027-02-28T19:00:00+00:00","ends":"2027-02-28T21:00:00+00:00","venue":null,"status":"published"}`
	snapVenue   = `{"starts":"2027-03-01T19:00:00+00:00","ends":"2027-03-01T21:00:00+00:00","venue":"text:Old Hall||Paris","status":"published"}`
)

func fixedNow(t *testing.T, at time.Time) {
	t.Helper()
	now = func() time.Time { return at }
	t.Cleanup(func() { now = time.Now })
}

func hookWith(t *testing.T, snapshot string) *eventHost {
	t.Helper()
	fixedNow(t, time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC))
	h := &eventHost{entity: gigEntity, hasTypes: true, snapshot: snapshot}
	h.install(t)
	fire(t, sdk.EntityUpdated, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	return h
}

func TestDetectDateChangeInsertsOneMarker(t *testing.T) {
	h := hookWith(t, snapOldDate)
	n := 0
	for _, c := range h.calls {
		if strings.HasPrefix(c.sql, "INSERT INTO event_changes") {
			n++
			if !strings.Contains(c.sql, "ON CONFLICT (event_id, change_key) DO NOTHING") || c.args[2] != "date" || c.args[0] != "e1" {
				t.Errorf("marker %s %v", c.sql, c.args)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d markers, want 1", n)
	}
	if h.used("INSERT INTO events") == nil {
		t.Error("the snapshot is still refreshed")
	}
	// the hook fires again before the snapshot caught up: the same key, so DO NOTHING dedupes
	h2 := hookWith(t, snapOldDate)
	if k1, k2 := h.used("INSERT INTO event_changes").args[1], h2.used("INSERT INTO event_changes").args[1]; k1 != k2 || len(k1.(string)) != 32 {
		t.Errorf("change keys %v and %v differ or are not 32 chars", k1, k2)
	}
}

func TestDetectNoMarker(t *testing.T) {
	for name, snap := range map[string]string{
		"first sighting":      "",
		"no change":           snapSame,
		"pre-venue snapshot":  snapNoVenue,
		"unpublished before":  `{"starts":"2027-02-28T19:00:00+00:00","ends":"2027-02-28T21:00:00+00:00","venue":"","status":"unpublished"}`,
		"deleted before":      `{"starts":"2027-02-28T19:00:00+00:00","ends":"2027-02-28T21:00:00+00:00","venue":"","status":"deleted"}`,
		"unpublished + moved": `{"starts":"2027-03-02T19:00:00+00:00","ends":"2027-03-02T21:00:00+00:00","venue":"","status":"unpublished"}`,
	} {
		h := hookWith(t, snap)
		if h.used("INSERT INTO event_changes") != nil {
			t.Errorf("%s: marker inserted", name)
		}
	}
}

func TestDetectStartedEventRecordsNothing(t *testing.T) {
	fixedNow(t, time.Date(2027, 3, 1, 20, 0, 0, 0, time.UTC)) // after the new start
	h := &eventHost{entity: gigEntity, hasTypes: true, snapshot: snapOldDate}
	h.install(t)
	fire(t, sdk.EntityUpdated, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	if h.used("INSERT INTO event_changes") != nil {
		t.Error("an event that has started must not record a change")
	}
}

func TestDetectStatusAndVenue(t *testing.T) {
	fixedNow(t, time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC))
	cancelled := strings.Replace(gigEntity, `"attendance_mode"`, `"event_status":"cancelled","attendance_mode"`, 1)
	h := &eventHost{entity: cancelled, hasTypes: true, snapshot: snapSame}
	h.install(t)
	fire(t, sdk.EntityUpdated, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	if c := h.used("INSERT INTO event_changes"); c == nil || c.args[2] != "status" {
		t.Errorf("cancelled: %v", c)
	}

	typed := strings.Replace(gigEntity, `"attendance_mode"`, `"venue_name":"New Hall","venue_city":"Paris","attendance_mode"`, 1)
	h = &eventHost{entity: typed, hasTypes: true, snapshot: snapVenue}
	h.install(t)
	fire(t, sdk.EntityUpdated, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	if c := h.used("INSERT INTO event_changes"); c == nil || c.args[2] != "venue" {
		t.Errorf("venue: %v", c)
	}
	if c := h.used("INSERT INTO events"); c == nil || c.args[6] != "text:New Hall||Paris" {
		t.Errorf("venue key stored: %v", c)
	}
}

func TestVenueOf(t *testing.T) {
	cases := []struct {
		attrs     map[string]any
		key, text string
	}{
		{map[string]any{"venue": []any{"v1"}, "venue_name": "Ignored"}, "id:v1", ""},
		{map[string]any{"venue_name": " Hall ", "venue_address": "1 Main St", "venue_city": "Paris"}, "text:Hall|1 Main St|Paris", "Hall, Paris"},
		{map[string]any{"venue_city": "Paris"}, "text:||Paris", "Paris"},
		{map[string]any{"venue": []any{}}, "", ""},
		{nil, "", ""},
	}
	for _, c := range cases {
		if k, text := venueOf(c.attrs); k != c.key || text != c.text {
			t.Errorf("%v: got %q %q, want %q %q", c.attrs, k, text, c.key, c.text)
		}
	}
}

func TestDetectRefreshAlsoObserves(t *testing.T) {
	h := newMaintainHost(t)
	h.stale = `["e1"]`
	h.entities["e1"] = gigEntity
	h.snapshotCell = snapOldDate
	refreshSnapshots(loadSettings(), func() bool { return false })
	if c := h.first("INSERT INTO event_changes"); c == nil || c.args[2] != "date" {
		t.Errorf("the hourly refresh did not record the change: %v", c)
	}
}
