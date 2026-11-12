package main

import (
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() { guest.OnFilter(sdk.JSONLDEnrich, enrichEvent) }

// enrichEvent turns the JSON-LD of an event entity into an Event: it sets
// @type (keeping a specific one such as MusicEvent) and adds the keys the
// document does not already carry. Any problem returns the document unchanged.
func enrichEvent(d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
	s := loadSettings()
	if !s.accepts(d.Entity.SchemaSlug) || d.Entity.ID == "" {
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
	loc := dateLocation(e, s.location())
	doc := make(map[string]any, len(d.Doc)+8)
	for k, v := range d.Doc {
		doc[k] = v
	}
	if t, _ := doc["@type"].(string); !strings.HasSuffix(t, "Event") {
		doc["@type"] = "Event"
	}
	add := func(key string, v any) {
		if _, exists := doc[key]; !exists {
			doc[key] = v
		}
	}
	add("startDate", isoTime(e.Start, e.AllDay, loc))
	if e.HasEnd {
		add("endDate", isoTime(e.End, e.AllDay, loc))
	}
	add("eventStatus", eventStatusURL(e.EventStatus))
	add("eventAttendanceMode", attendanceModeURL(e.AttendanceMode))
	if loc := locationOf(e); loc != nil {
		add("location", loc)
	}
	if o, ok := loadOrganizer(firstID(e.OrganizerIDs)); ok {
		org := map[string]any{"@type": "Organization", "name": o.Name}
		if isAbsoluteURL(o.Website) {
			org["url"] = o.Website
		}
		if o.Email != "" {
			org["email"] = o.Email
		}
		add("organizer", org)
	}
	if isAbsoluteURL(e.TicketURL) {
		offer := map[string]any{"@type": "Offer", "url": e.TicketURL}
		if e.PriceAmount != nil {
			offer["price"] = strconv.FormatFloat(*e.PriceAmount, 'f', -1, 64)
		}
		if e.Currency != "" {
			offer["priceCurrency"] = e.Currency
		}
		add("offers", offer)
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

func firstID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func eventStatusURL(status string) string {
	switch status {
	case "cancelled":
		return "https://schema.org/EventCancelled"
	case "postponed":
		return "https://schema.org/EventPostponed"
	}
	return "https://schema.org/EventScheduled"
}

func attendanceModeURL(mode string) string {
	switch mode {
	case "online":
		return "https://schema.org/OnlineEventAttendanceMode"
	case "hybrid":
		return "https://schema.org/MixedEventAttendanceMode"
	}
	return "https://schema.org/OfflineEventAttendanceMode"
}

// locationOf is the event's location: a Place for a venue (a postal address
// and coordinates when a venue entity resolves, else the text address) and a
// VirtualLocation for an online or hybrid event with a link. Nil when the event
// has neither.
func locationOf(e Event) any {
	var places []any
	name, address, _, v := venueOrText(e)
	if name != "" {
		place := map[string]any{"@type": "Place", "name": name}
		if v != nil {
			addr := map[string]any{"@type": "PostalAddress"}
			for k, val := range map[string]string{"streetAddress": v.Address, "addressLocality": v.City,
				"addressRegion": v.Region, "postalCode": v.Postcode, "addressCountry": v.Country} {
				if val != "" {
					addr[k] = val
				}
			}
			if len(addr) > 1 {
				place["address"] = addr
			}
			if v.Lat != nil && v.Lon != nil {
				place["geo"] = map[string]any{"@type": "GeoCoordinates", "latitude": *v.Lat, "longitude": *v.Lon}
			}
		} else if address != "" {
			place["address"] = address
		}
		places = append(places, place)
	}
	if e.AttendanceMode != "offline" && isAbsoluteURL(e.OnlineURL) {
		places = append(places, map[string]any{"@type": "VirtualLocation", "url": e.OnlineURL})
	}
	switch len(places) {
	case 0:
		return nil
	case 1:
		return places[0]
	}
	return places
}
