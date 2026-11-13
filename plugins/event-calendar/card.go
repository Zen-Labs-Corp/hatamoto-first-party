package main

import (
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// answerEventCard answers the "this event" source: one item for the page's own
// event, with everything the details card shows. An empty answer (no entity
// page, another content type, an unpublished or broken event) is not an error.
// Reference lookups stop once builderBudget is used; the text fields of the
// event cover a reference that is missing.
func answerEventCard(q sdk.BuilderDataQuery, entityID string) sdk.BuilderDataResult {
	res := sdk.BuilderDataResult{ID: q.ID}
	if entityID == "" {
		return res
	}
	deadline := clock().Add(builderBudget)
	inBudget := func() bool { return clock().Before(deadline) }

	s := loadSettings()
	var raw entityDTO
	if err := callHost(sdk.FnEntitiesGet, map[string]any{"id": entityID}, &raw); err != nil || !s.accepts(raw.SchemaSlug) {
		return res
	}
	ev, ok := eventFromEntity(raw, s)
	if !ok {
		return res
	}
	loc := dateLocation(ev, s.location())

	name, address, city := ev.Venue, ev.Address, ev.VenueCity
	var venue *Venue
	if inBudget() {
		name, address, city, venue = venueOrText(ev)
	}
	where := joinNonEmpty(name, address, city)

	attrs := map[string]string{}
	set := func(k, v string) {
		if v = capString(v); v != "" {
			attrs[k] = v
		}
	}
	when := formatWhen(ev.Start, ev.End, ev.HasEnd, ev.AllDay, s.DateFormat, loc, now())
	if !ev.AllDay {
		when += " (" + loc.String() + ")"
	}
	set("when", when)
	set("status", statusLabel(ev.EventStatus))
	set("venue", where)
	if venue != nil {
		set("venue_url", venue.URL)
	}
	set("price", priceText(ev))
	if ev.AttendanceMode != "offline" && isAbsoluteURL(ev.OnlineURL) {
		set("online_url", ev.OnlineURL)
	}
	set("ticket_url", ev.TicketURL)

	item := sdk.BuilderDataItem{
		Title:       capString(ev.Name),
		URL:         capString(raw.URL),
		Excerpt:     capString(ev.Description),
		Image:       capString(ev.Image),
		PublishedAt: ev.Start.UTC().Format(time.RFC3339),
	}
	if len(ev.OrganizerIDs) > 0 && inBudget() {
		if o, ok := loadOrganizer(ev.OrganizerIDs[0]); ok {
			item.Author, item.AuthorURL = capString(o.Name), capString(o.Website)
			set("organizer_email", o.Email)
		}
	}
	if ev.SeriesID != "" && inBudget() {
		if sr, ok := loadSeries(ev.SeriesID); ok {
			set("series", sr.Name)
			set("series_url", sr.URL)
		}
	}
	if len(ev.CategoryIDs) > 0 && inBudget() {
		item.Category = capString(loadCategoryName(ev.CategoryIDs[0]))
	}

	links := calendarLinks(ev, loc, where, ev.Description)
	set("ics_url", links.ICS)
	set("google_url", links.Google)
	set("outlook_url", links.Outlook)
	set("yahoo_url", links.Yahoo)

	if s.ShowMap && inBudget() {
		set("map_url", mapURL(venue, ev))
	}
	item.Attrs = attrs
	res.Items = []sdk.BuilderDataItem{item}
	return res
}

// loadCategoryName is the name of a published category entity, empty when it
// cannot be read.
func loadCategoryName(id string) string {
	raw, _, ok := loadRef(id)
	if !ok {
		return ""
	}
	return raw.Name
}

func statusLabel(status string) string {
	switch status {
	case "cancelled":
		return "Cancelled"
	case "postponed":
		return "Postponed"
	}
	return "Scheduled"
}

// priceText is the price as the editor wrote it, else the amount and
// currency; "Free" for an amount of zero.
func priceText(e Event) string {
	switch {
	case e.Price != "":
		return e.Price
	case e.PriceAmount == nil:
		return ""
	case *e.PriceAmount == 0:
		return "Free"
	}
	return strings.TrimSpace(strconv.FormatFloat(*e.PriceAmount, 'f', -1, 64) + " " + e.Currency)
}

// mapURL is the OpenStreetMap link of the venue: its own coordinates, else the
// cached lookup of its address. Empty when the position is not known.
func mapURL(v *Venue, e Event) string {
	var lat, lon float64
	var ok bool
	var vv Venue
	if v != nil {
		vv = *v
		if v.Lat != nil && v.Lon != nil {
			lat, lon, ok = *v.Lat, *v.Lon, true
		}
	}
	if !ok {
		lat, lon, ok = cachedCoords(addressOf(vv, e))
	}
	if !ok {
		return ""
	}
	la, lo := strconv.FormatFloat(lat, 'f', 5, 64), strconv.FormatFloat(lon, 'f', 5, 64)
	return "https://www.openstreetmap.org/?mlat=" + la + "&mlon=" + lo + "#map=16/" + la + "/" + lo
}
