package main

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// The data behind the Events workspace of the admin page: the paged lists of
// upcoming and past events with their filters, and the counts of the tabs.

const (
	eventsPerPage = 50
	maxEventsPage = 2000
	maxFacet      = 200
	maxQueryBytes = 200
	maxMonthRows  = 1500
)

var (
	eventsDayRe   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	eventsMonthRe = regexp.MustCompile(`^(20[0-9]{2})-(0[1-9]|1[0-2])$`)
)

// eventItem is one row of the Events list.
type eventItem struct {
	ID         string   `json:"id"`
	Slug       string   `json:"slug"`
	Name       string   `json:"name"`
	Start      string   `json:"start"` // RFC3339 in the site's time zone
	End        string   `json:"end"`
	Day        string   `json:"day"`  // YYYY-MM-DD in the site's time zone
	Time       string   `json:"time"` // "09:00", empty for an all-day event
	AllDay     bool     `json:"all_day"`
	Venue      string   `json:"venue"`
	City       string   `json:"city"`
	NoVenue    bool     `json:"no_venue"`
	Categories []string `json:"categories"`
	Price      string   `json:"price"`
	Status     string   `json:"status"`
	Repeating  bool     `json:"repeating"`
	Imported   bool     `json:"imported"`
}

type eventCounts struct {
	Upcoming           int  `json:"upcoming"`
	Past               int  `json:"past"`
	Drafts             *int `json:"drafts,omitempty"`
	DraftsCapped       bool `json:"drafts_capped,omitempty"`
	SubmissionsPending int  `json:"submissions_pending"`
	Repeating          int  `json:"repeating"`
}

type eventFacets struct {
	Venues     []string `json:"venues"`
	Categories []string `json:"categories"`
}

type eventsPage struct {
	Ready       bool           `json:"ready"`
	Role        string         `json:"role"`
	Items       []eventItem    `json:"items"`
	Total       int            `json:"total"`
	TotalCapped bool           `json:"total_capped,omitempty"`
	DayCounts   map[string]int `json:"day_counts"`
	Page        int            `json:"page"`
	PerPage     int            `json:"per_page"`
	Counts      eventCounts    `json:"counts"`
	Facets      *eventFacets   `json:"facets,omitempty"`
}

// eventsQuery is the validated query of GET /admin/events.
type eventsQuery struct {
	Tab, Q, Venue, Category string
	NoVenue                 bool
	Day, Month              string
	Page                    int
	Facets, WithDrafts      bool
}

// parseEventsQuery validates the query; false means a bad request. Unknown
// keys are ignored.
func parseEventsQuery(q map[string][]string) (eventsQuery, bool) {
	out := eventsQuery{Tab: "upcoming", Page: 1}
	if v := queryValue(q["tab"]); v != "" {
		out.Tab = v
	}
	if !slices.Contains([]string{"upcoming", "past", "drafts"}, out.Tab) {
		return out, false
	}
	out.Q, out.Venue, out.Category = queryValue(q["q"]), queryValue(q["venue"]), queryValue(q["category"])
	if len(out.Q) > maxQueryBytes || len(out.Venue) > maxQueryBytes || len(out.Category) > maxQueryBytes {
		return out, false
	}
	out.NoVenue = queryValue(q["novenue"]) == "1"
	out.Facets = queryValue(q["facets"]) == "1"
	out.WithDrafts = queryValue(q["with_drafts"]) == "1"
	if out.Day = queryValue(q["day"]); out.Day != "" {
		if !eventsDayRe.MatchString(out.Day) {
			return out, false
		}
		if _, err := time.Parse("2006-01-02", out.Day); err != nil {
			return out, false
		}
	}
	if out.Month = queryValue(q["month"]); out.Month != "" && !eventsMonthRe.MatchString(out.Month) {
		return out, false
	}
	if v := queryValue(q["page"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxEventsPage {
			return out, false
		}
		out.Page = n
	}
	return out, true
}

// eventsFilterSQL is the search, category, venue and no-venue predicates shared
// by the list and the month. They use $3 to $6 in both statements.
const eventsFilterSQL = ` AND ($3::text = '' OR e.name ILIKE '%' || $3::text || '%' OR e.venue_name ILIKE '%' || $3::text || '%' OR e.venue_city ILIKE '%' || $3::text || '%')
 AND ($4::text = '' OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(e.category_names) c WHERE lower(c) = lower($4::text))
  OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(e.category_slugs) c WHERE lower(c) = lower($4::text)))
 AND ($5::text = '' OR lower(e.venue_name) = lower($5::text) OR e.venue_slug = lower($5::text))
 AND (NOT $6::boolean OR (e.venue_id = '' AND e.venue_name = ''))`

// eventsColumns are the columns a row of the list reads.
const eventsColumns = `e.entity_id AS id, e.slug, e.name, o.starts_at AS os, o.ends_at AS oe, e.all_day, e.venue_name, e.venue_id,
 e.venue_city, e.category_names, e.price, e.price_amount, e.repeating, e.source_kind`

// eventsListSQL answers one page of the occurrences before or after now.
// $1 now, $2 past, $3 q, $4 category, $5 venue, $6 no venue, $7 day (” for
// any), $8 time zone, $9 limit, $10 offset. The day counts cover the days on
// the page. It is NULL before the first complete reconcile.
const eventsListSQL = `WITH f AS (SELECT ` + eventsColumns + `
 FROM events e JOIN occurrences o ON o.event_id = e.entity_id
 WHERE (CASE WHEN $2::boolean THEN o.ends_at <= $1::timestamptz ELSE o.ends_at > $1::timestamptz END)` + eventsFilterSQL + `
 AND ($7::text = '' OR (o.starts_at AT TIME ZONE $8::text)::date = $7::date)),
p AS (SELECT * FROM f
 ORDER BY CASE WHEN $2::boolean THEN -extract(epoch FROM os) ELSE extract(epoch FROM os) END, name, id
 LIMIT $9::int OFFSET $10::int)
SELECT CASE WHEN EXISTS (SELECT 1 FROM index_state) THEN jsonb_build_object(
 'total', (SELECT count(*) FROM f),
 'items', COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY CASE WHEN $2::boolean THEN -extract(epoch FROM p.os) ELSE extract(epoch FROM p.os) END, p.name, p.id) FROM p), '[]'::jsonb),
 'day_counts', COALESCE((SELECT jsonb_object_agg(d, c) FROM (SELECT (f.os AT TIME ZONE $8::text)::date::text AS d, count(*) AS c FROM f
  WHERE (f.os AT TIME ZONE $8::text)::date IN (SELECT (p.os AT TIME ZONE $8::text)::date FROM p) GROUP BY 1) x), '{}'::jsonb)) END`

// eventsMonthSQL answers the occurrences overlapping the month [$1, $2) in
// start order, at most $8 of them, and the count of each local day by start.
// $3 to $6 filter as in eventsListSQL and $7 is the time zone. It is NULL
// before the first complete reconcile.
const eventsMonthSQL = `WITH f AS (SELECT e.entity_id AS id, e.name, o.starts_at AS os, e.all_day
 FROM events e JOIN occurrences o ON o.event_id = e.entity_id
 WHERE o.starts_at < $2::timestamptz AND o.ends_at > $1::timestamptz` + eventsFilterSQL + `)
SELECT CASE WHEN EXISTS (SELECT 1 FROM index_state) THEN jsonb_build_object(
 'total', (SELECT count(*) FROM f),
 'truncated', (SELECT count(*) FROM f) > $8::int,
 'items', COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.os, p.name, p.id) FROM (SELECT * FROM f ORDER BY os, name, id LIMIT $8::int) p), '[]'::jsonb),
 'day_counts', COALESCE((SELECT jsonb_object_agg(d, c) FROM (SELECT (f.os AT TIME ZONE $7::text)::date::text AS d, count(*) AS c FROM f GROUP BY 1) x), '{}'::jsonb)) END`

// eventCountsSQL counts the occurrences before and after $1 (now), the
// repeating events and the pending submissions, without any filter.
const eventCountsSQL = `SELECT jsonb_build_object(
 'upcoming', (SELECT count(*) FROM occurrences WHERE ends_at > $1::timestamptz),
 'past', (SELECT count(*) FROM occurrences WHERE ends_at <= $1::timestamptz),
 'repeating', (SELECT count(*) FROM events WHERE repeating),
 'submissions_pending', (SELECT count(*) FROM submissions WHERE status = 'pending'))`

// eventFacetsSQL lists the venues and categories the filters offer.
const eventFacetsSQL = `SELECT jsonb_build_object(
 'venues', COALESCE((SELECT jsonb_agg(v ORDER BY lower(v)) FROM (SELECT venue_name AS v FROM events WHERE venue_name <> ''
  GROUP BY venue_name ORDER BY lower(venue_name) LIMIT $1::int) x), '[]'::jsonb),
 'categories', COALESCE((SELECT jsonb_agg(c ORDER BY lower(c)) FROM (SELECT c FROM events, jsonb_array_elements_text(category_names) AS c
  WHERE c <> '' GROUP BY c ORDER BY lower(c) LIMIT $1::int) y), '[]'::jsonb))`

// monthItem is one row of the month grid.
type monthItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Day    string `json:"day"`
	Time   string `json:"time"`
	AllDay bool   `json:"all_day"`
}

// monthPage is the answer for ?month=YYYY-MM.
type monthPage struct {
	Ready     bool           `json:"ready"`
	Role      string         `json:"role"`
	Month     string         `json:"month"`
	Items     []monthItem    `json:"items"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`
	DayCounts map[string]int `json:"day_counts"`
	Page      int            `json:"page"`
	PerPage   int            `json:"per_page"`
	Counts    eventCounts    `json:"counts"`
	Facets    *eventFacets   `json:"facets,omitempty"`
}

// eventRow is one list row as the statement returns it.
type eventRow struct {
	ID            string    `json:"id"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Start         time.Time `json:"os"`
	End           time.Time `json:"oe"`
	AllDay        bool      `json:"all_day"`
	VenueName     string    `json:"venue_name"`
	VenueID       string    `json:"venue_id"`
	VenueCity     string    `json:"venue_city"`
	CategoryNames []string  `json:"category_names"`
	Price         string    `json:"price"`
	PriceAmount   *float64  `json:"price_amount"`
	Repeating     bool      `json:"repeating"`
	SourceKind    string    `json:"source_kind"`
}

// serveEvents answers GET /admin/events. It is an admin route: without a
// signed-in user it is refused.
func serveEvents(u *sdk.HTTPUser, query map[string][]string) sdk.HTTPResponse {
	if u == nil {
		return plain(http.StatusForbidden, "forbidden")
	}
	q, ok := parseEventsQuery(query)
	if !ok {
		return jsonReply(http.StatusBadRequest, map[string]string{"error": "bad request"})
	}
	s := loadSettings()
	loc := s.location()
	at := now()
	if q.Month != "" {
		return serveMonth(u, q, loc, at)
	}
	out := eventsPage{
		Role: u.Role, Items: []eventItem{}, DayCounts: map[string]int{},
		Page: q.Page, PerPage: eventsPerPage,
	}
	if err := dbJSON(eventCountsSQL, &out.Counts, utcText(at)); err != nil {
		return unavailable()
	}
	if q.Tab == "drafts" {
		out.Ready = true
	} else {
		var res *struct {
			Total     int            `json:"total"`
			Items     []eventRow     `json:"items"`
			DayCounts map[string]int `json:"day_counts"`
		}
		if err := dbJSON(eventsListSQL, &res, utcText(at), q.Tab == "past", escapeLike(q.Q), q.Category, q.Venue,
			q.NoVenue, q.Day, loc.String(), eventsPerPage, (q.Page-1)*eventsPerPage); err != nil {
			return unavailable()
		}
		if res != nil {
			out.Ready = true
			out.Total = res.Total
			if res.DayCounts != nil {
				out.DayCounts = res.DayCounts
			}
			for _, r := range res.Items {
				out.Items = append(out.Items, itemOf(r, loc))
			}
		}
	}
	if q.Facets {
		var err error
		if out.Facets, err = readFacets(); err != nil {
			return unavailable()
		}
	}
	return jsonReply(http.StatusOK, out)
}

// readFacets reads the venues and categories the filters offer.
func readFacets() (*eventFacets, error) {
	f := &eventFacets{Venues: []string{}, Categories: []string{}}
	return f, dbJSON(eventFacetsSQL, f, maxFacet)
}

// itemOf is the list row of an indexed occurrence, in the site's time zone.
func itemOf(r eventRow, loc *time.Location) eventItem {
	start, end := r.Start.In(loc), r.End.In(loc)
	it := eventItem{
		ID: r.ID, Slug: r.Slug, Name: r.Name,
		Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339), Day: start.Format("2006-01-02"),
		AllDay: r.AllDay, Venue: r.VenueName, City: r.VenueCity,
		NoVenue: r.VenueID == "" && r.VenueName == "", Categories: r.CategoryNames,
		Status: "published", Repeating: r.Repeating, Imported: slices.Contains(importKinds, r.SourceKind),
	}
	if it.Categories == nil {
		it.Categories = []string{}
	}
	if !r.AllDay {
		it.Time = start.Format("15:04")
	}
	it.Price = r.Price
	if r.PriceAmount != nil && *r.PriceAmount == 0 || strings.EqualFold(strings.TrimSpace(r.Price), "free") {
		it.Price = "Free"
	}
	return it
}

// serveMonth answers ?month=YYYY-MM: the month's occurrences in one page, in
// the site's time zone. The month ignores the tab and has no second page.
func serveMonth(u *sdk.HTTPUser, q eventsQuery, loc *time.Location, at time.Time) sdk.HTTPResponse {
	if q.Tab == "drafts" || q.Page != 1 {
		return jsonReply(http.StatusBadRequest, map[string]string{"error": "bad request"})
	}
	y, _ := strconv.Atoi(q.Month[:4])
	m, _ := strconv.Atoi(q.Month[5:])
	from := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, loc)
	out := monthPage{
		Role: u.Role, Month: q.Month, Items: []monthItem{}, DayCounts: map[string]int{},
		Page: 1, PerPage: maxMonthRows,
	}
	if err := dbJSON(eventCountsSQL, &out.Counts, utcText(at)); err != nil {
		return unavailable()
	}
	var res *struct {
		Total     int            `json:"total"`
		Truncated bool           `json:"truncated"`
		Items     []eventRow     `json:"items"`
		DayCounts map[string]int `json:"day_counts"`
	}
	if err := dbJSON(eventsMonthSQL, &res, utcText(from), utcText(from.AddDate(0, 1, 0)), escapeLike(q.Q), q.Category, q.Venue,
		q.NoVenue, loc.String(), maxMonthRows); err != nil {
		return unavailable()
	}
	if res != nil {
		out.Ready, out.Total, out.Truncated = true, res.Total, res.Truncated
		if res.DayCounts != nil {
			out.DayCounts = res.DayCounts
		}
		for _, r := range res.Items {
			it := itemOf(r, loc)
			out.Items = append(out.Items, monthItem{ID: it.ID, Name: it.Name, Day: it.Day, Time: it.Time, AllDay: it.AllDay})
		}
	}
	if q.Facets {
		var err error
		if out.Facets, err = readFacets(); err != nil {
			return unavailable()
		}
	}
	return jsonReply(http.StatusOK, out)
}
