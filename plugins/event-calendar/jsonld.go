package main

import (
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() { guest.OnFilter(sdk.JSONLDEnrich, enrichEvent) }

// enrichEvent turns the JSON-LD of an event entity into an Event: it sets
// @type and adds the keys the document does not already carry. Any problem
// returns the document unchanged.
func enrichEvent(d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
	s := loadSettings()
	if d.Entity.SchemaSlug != s.SchemaSlug || d.Entity.ID == "" {
		return d, nil
	}
	var raw entityDTO
	if err := callHost(sdk.FnEntitiesGet, map[string]any{"id": d.Entity.ID}, &raw); err != nil {
		return d, nil
	}
	e, ok := eventFromEntity(raw, s)
	if !ok {
		return d, nil
	}
	loc := s.location()
	doc := make(map[string]any, len(d.Doc)+8)
	for k, v := range d.Doc {
		doc[k] = v
	}
	doc["@type"] = "Event"
	add := func(key string, v any) {
		if _, exists := doc[key]; !exists {
			doc[key] = v
		}
	}
	add("startDate", isoTime(e.Start, e.AllDay, loc))
	if e.HasEnd {
		add("endDate", isoTime(e.End, e.AllDay, loc))
	}
	add("eventStatus", "https://schema.org/EventScheduled")
	add("eventAttendanceMode", "https://schema.org/OfflineEventAttendanceMode")
	if e.Venue != "" {
		place := map[string]any{"@type": "Place", "name": e.Venue}
		if e.Address != "" {
			place["address"] = e.Address
		}
		add("location", place)
	}
	if isAbsoluteURL(e.TicketURL) {
		add("offers", map[string]any{"@type": "Offer", "url": e.TicketURL})
	}
	if img := absoluteURL(d.BaseURL, e.Image); img != "" {
		add("image", img)
	}
	d.Doc = doc
	return d, nil
}

// isoTime is an ISO 8601 date (all-day) or an RFC 3339 time in loc.
func isoTime(t time.Time, allDay bool, loc *time.Location) string {
	if allDay {
		return t.In(loc).Format("2006-01-02")
	}
	return t.In(loc).Format(time.RFC3339)
}

// absoluteURL resolves a site-relative path against base; empty when the
// result would not be an absolute http(s) URL.
func absoluteURL(base, ref string) string {
	switch {
	case ref == "":
		return ""
	case isAbsoluteURL(ref):
		return ref
	case strings.HasPrefix(ref, "/") && !strings.HasPrefix(ref, "//") && isAbsoluteURL(base):
		return strings.TrimRight(base, "/") + ref
	}
	return ""
}
