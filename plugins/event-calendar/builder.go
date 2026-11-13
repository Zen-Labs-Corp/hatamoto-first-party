package main

import (
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	sourceUpcoming = "ext.event-calendar.upcoming"
	sourcePast     = "ext.event-calendar.past"
	// sourceEvent is the details card of the page's own event.
	sourceEvent   = "ext.event-calendar.event"
	icsPathPrefix = "/p/event-calendar/event/"

	// builderBudget is how long a builder call may keep paging. Core cancels
	// the whole external-data round trip at 500 ms, so the plugin returns what
	// it has well before that.
	builderBudget = 300 * time.Millisecond
)

func init() { guest.OnFilter(sdk.BuilderData, answerBuilder) }

// answerBuilder answers the two event sources and leaves other requests
// alone. A failure yields an empty list, never an error string.
func answerBuilder(r sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
	var upcoming, past []Event
	loaded, started := false, false
	var s settings
	var at, deadline time.Time
	type filledEvent struct {
		Event
		keep bool
	}
	filled := map[string]filledEvent{}
	venues := venueMemo{}
	for _, q := range r.Requests {
		if q.Source == sourceEvent {
			r.Results = append(r.Results, answerEventCard(q, r.EntityID))
			continue
		}
		if q.Source != sourceUpcoming && q.Source != sourcePast {
			continue
		}
		if !started {
			started = true
			s = loadSettings()
			at = now()
			deadline = clock().Add(builderBudget)
		}
		f := filtersFromParams(q.Params)
		limit := q.Limit
		if limit <= 0 || limit > sdk.MaxBuilderDataItems {
			limit = sdk.MaxBuilderDataItems
		}
		var list []Event
		switch {
		case !f.empty():
			list = filteredEvents(s, r.Language, at, q.Source == sourceUpcoming, f, limit, deadline, venues)
		default:
			if !loaded {
				loaded = true
				var wantUp, wantPast bool
				for _, w := range r.Requests {
					if filtersFromParams(w.Params).empty() {
						wantUp = wantUp || w.Source == sourceUpcoming
						wantPast = wantPast || w.Source == sourcePast
					}
				}
				upcoming, past, _ = eventsFor(s, r.Language, at, wantUp, wantPast, sdk.MaxBuilderDataItems, deadline)
			}
			list = upcoming
			if q.Source == sourcePast {
				list = past
			}
		}
		if len(list) > limit {
			list = list[:limit]
		}
		res := sdk.BuilderDataResult{ID: q.ID}
		for _, e := range list {
			f, ok := filled[e.ID]
			if !ok {
				f.Event, f.keep = fillDetails(e, s, deadline, venues)
				filled[e.ID] = f
			}
			if !f.keep {
				continue
			}
			e = f.Event
			res.Items = append(res.Items, toItem(e, s, at))
		}
		r.Results = append(r.Results, res)
	}
	return r, nil
}

// filterLanguage keeps events of the given language and those shared across
// languages (no locale, or core's "und"); no language keeps all.
func filterLanguage(evs []Event, lang string) []Event {
	if lang == "" {
		return evs
	}
	var out []Event
	for _, e := range evs {
		if e.Locale == "" || e.Locale == "und" || e.Locale == lang {
			out = append(out, e)
		}
	}
	return out
}

// capString shortens s to at most sdk.MaxBuilderDataString bytes on a rune
// boundary, replacing invalid UTF-8.
func capString(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= sdk.MaxBuilderDataString {
		return s
	}
	s = s[:sdk.MaxBuilderDataString]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func toItem(e Event, s settings, at time.Time) sdk.BuilderDataItem {
	loc := dateLocation(e, s.location())
	attrs := map[string]string{
		"when":    formatWhen(e.Start, e.End, e.HasEnd, e.AllDay, s.DateFormat, loc, at),
		"all_day": "false",
	}
	set := func(k, v string) {
		if v != "" {
			attrs[k] = capString(v)
		}
	}
	set("start_at", e.Start.In(loc).Format(time.RFC3339))
	if e.HasEnd {
		set("end_at", e.End.In(loc).Format(time.RFC3339))
	}
	if e.AllDay {
		attrs["all_day"] = "true"
	}
	set("time_zone", e.TimeZone)
	set("venue_name", e.Venue)
	set("venue_address", e.Address)
	set("ticket_url", e.TicketURL)
	if e.Slug != "" {
		set("ics_url", icsPathPrefix+e.Slug+".ics")
	}
	return sdk.BuilderDataItem{
		Title:       capString(e.Name),
		URL:         capString(e.URL),
		Excerpt:     capString(e.Description),
		Image:       capString(e.Image),
		PublishedAt: e.Start.UTC().Format(time.RFC3339),
		Attrs:       attrs,
	}
}

// eventsFor reads the wanted lists, each of at most limit events: upcoming
// (not over at the given moment, soonest first) and past (latest first). The
// index answers first; a list it cannot answer (no db, or no complete
// reconcile yet) comes from the entities, which
// are listed once for both. The error is that listing's, set only when it was
// needed and failed; lists the index answered are still returned.
func eventsFor(s settings, lang string, at time.Time, wantUp, wantPast bool, limit int, until time.Time) (upcoming, past []Event, err error) {
	fromIndex := func(up bool) ([]Event, bool) {
		evs, qerr := queryEvents(indexQuery{Upcoming: up, At: at, Language: lang, Limit: limit, Loc: s.location()})
		var out []Event
		for _, e := range evs {
			if s.accepts(e.SchemaSlug) {
				out = append(out, e)
			}
		}
		return out, qerr == nil
	}
	okUp, okPast := !wantUp, !wantPast
	if wantUp {
		upcoming, okUp = fromIndex(true)
	}
	if wantPast {
		past, okPast = fromIndex(false)
	}
	if okUp && okPast {
		return upcoming, past, nil
	}
	evs, err := loadEventsUntil(s, until)
	if err != nil {
		return upcoming, past, err
	}
	up, pa := split(filterLanguage(evs, lang), at, s.location())
	if !okUp {
		upcoming = firstN(up, limit)
	}
	if !okPast {
		past = firstN(pa, limit)
	}
	return upcoming, past, nil
}

// filteredEvents is one upcoming or past list narrowed by f. The filters run
// in the index query, so the cut to limit comes after them. While the index
// cannot answer, the entities are listed and filtered here.
func filteredEvents(s settings, lang string, at time.Time, upcoming bool, f filters, limit int, until time.Time, venues venueMemo) []Event {
	q := rangeQuery{
		From: at.AddDate(-100, 0, 0), To: at.AddDate(100, 0, 0), Q: f.Q, Category: f.Category, Venue: f.Venue,
		City: f.City, Price: f.Price, Language: lang, Limit: maxIndexRows, Desc: !upcoming,
	}
	if upcoming {
		q.From = at
	} else {
		q.To = at
	}
	if rows, err := queryRange(q); err == nil {
		var out []Event
		for _, r := range rows {
			e := r.event(s.location())
			// the query overlaps [From, To): keep the side of the split it must have
			if s.accepts(e.SchemaSlug) && at.Before(e.endsAt(s.location())) == upcoming {
				out = append(out, e)
			}
		}
		return firstN(out, limit)
	}
	evs, err := loadEventsUntil(s, until)
	if err != nil {
		return nil
	}
	up, pa := split(filterLanguage(evs, lang), at, s.location())
	list := pa
	if upcoming {
		list = up
	}
	var out []Event
	for _, e := range list {
		if matchEntityEvent(e, f, until, venues) {
			out = append(out, e)
		}
	}
	return firstN(out, limit)
}

// matchEntityEvent is f.match for an event read from the entities: the venue
// and category names are read first, while the time budget lasts, and only
// when the filter needs them.
func matchEntityEvent(e Event, f filters, until time.Time, venues venueMemo) bool {
	if e.VenueID != "" && (f.Venue != "" || f.City != "" || f.Q != "") && clock().Before(until) {
		if v, ok := venues.get(e.VenueID); ok {
			if e.Venue == "" {
				e.Venue = v.Name
			}
			if e.VenueCity == "" {
				e.VenueCity = v.City
			}
		}
	}
	var cats categoryRefs
	if f.Category != "" && clock().Before(until) {
		cats = categoriesOf(e.CategoryIDs)
	}
	return f.match(e, cats)
}

func firstN(evs []Event, n int) []Event {
	if len(evs) > n {
		return evs[:n]
	}
	return evs
}

// fillDetails completes an event that came from the index, which keeps no
// URL, description, address or ticket link: they are read from the entity,
// while the time budget lasts. Without a budget or when the host fails the
// event stays as the index had it. Events of the entities path are complete
// and returned unchanged. keep is false for an event whose entity is gone or
// no longer published: it must not be listed, and its row is dropped.
func fillDetails(e Event, s settings, deadline time.Time, venues venueMemo) (_ Event, keep bool) {
	if e.ID == "" || !clock().Before(deadline) {
		return e, true
	}
	if e.indexed {
		var keep bool
		if e, keep = fillFromEntity(e, s); !keep {
			return e, false
		}
	}
	return withVenue(e, deadline, venues), true
}

// withVenue puts the name and address of the chosen venue entity on an event
// that has no text of its own for them (an event read from the entities has
// only the venue reference; the index has the name but not the address).
// Without a budget, a venue or when the host fails the event is unchanged. A
// venue is read once per request, however many events share it.
func withVenue(e Event, deadline time.Time, venues venueMemo) Event {
	if e.VenueID == "" || (e.Venue != "" && e.Address != "") || !clock().Before(deadline) {
		return e
	}
	if v, ok := venues.get(e.VenueID); ok {
		if e.Venue == "" {
			e.Venue = v.Name
		}
		if e.Address == "" {
			e.Address = joinNonEmpty(v.Address, v.City)
		}
	}
	return e
}

// fillFromEntity is the entity part of fillDetails.
func fillFromEntity(e Event, s settings) (_ Event, keep bool) {
	var raw entityDTO
	err := callHost(sdk.FnEntitiesGet, map[string]any{"id": e.ID}, &raw)
	if isNotFound(err) || (err == nil && raw.Status != "published") {
		_ = deleteEvent(e.ID)
		return e, false
	}
	if err != nil {
		return e, true
	}
	full, ok := eventFromEntity(raw, s)
	if !ok {
		return e, true
	}
	e.URL, e.Description, e.TicketURL, e.Website = full.URL, full.Description, full.TicketURL, full.Website
	if e.Address == "" {
		e.Address = full.Address
	}
	if e.Image == "" {
		e.Image = full.Image
	}
	return e, true
}
