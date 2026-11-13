package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	// defaultSpan is the range answered when the query gives none; maxSpan caps
	// any range so one request cannot read the whole index.
	defaultSpan = 31 * 24 * time.Hour
	maxSpan     = 400 * 24 * time.Hour
)

// apiEvent is one occurrence in the events route's answer. day and end_day
// are calendar dates (end_day inclusive): a timed event's in the site zone, an
// all-day event's in its own zone, so a client never does zone maths.
type apiEvent struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Slug       string   `json:"slug"`
	URL        string   `json:"url"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	Day        string   `json:"day"`
	EndDay     string   `json:"end_day"`
	AllDay     bool     `json:"all_day"`
	When       string   `json:"when"`
	Venue      string   `json:"venue"`
	VenueSlug  string   `json:"venue_slug"`
	City       string   `json:"city"`
	Address    string   `json:"address"`
	Lat        *float64 `json:"lat"`
	Lon        *float64 `json:"lon"`
	Categories []string `json:"categories"`
	Price      string   `json:"price"`
	Image      string   `json:"image"`
	Featured   bool     `json:"featured"`
	Status     string   `json:"status"`
}

// apiResponse is the JSON body. ready is false while the index cannot answer;
// events is then empty.
type apiResponse struct {
	Ready     bool       `json:"ready"`
	TZ        string     `json:"tz"`
	WeekStart string     `json:"week_start"`
	ShowMap   bool       `json:"show_map"`
	Truncated bool       `json:"truncated"`
	Events    []apiEvent `json:"events"`
}

// serveAPI answers the public events route: the occurrences overlapping
// [from, to), filtered, from the index. A bad query is a 400 with a fixed
// text; a host or index failure is a 200 with no events, never an error text.
func serveAPI(query map[string][]string) sdk.HTTPResponse {
	f, ok := filtersFromQuery(query)
	if !ok {
		return plain(http.StatusBadRequest, "bad request")
	}
	s := loadSettings()
	loc := s.location()
	at := now()
	q, ok := rangeFromQuery(query, f, at, loc)
	if !ok {
		return plain(http.StatusBadRequest, "bad request")
	}
	resp := apiResponse{TZ: loc.String(), WeekStart: s.WeekStartsOn, ShowMap: s.ShowMap, Events: []apiEvent{}}
	if rows, err := queryRange(q); err == nil {
		resp.Ready, resp.Truncated = true, len(rows) == q.Limit
		for _, r := range rows {
			if s.accepts(r.SchemaSlug) {
				resp.Events = append(resp.Events, toAPIEvent(r, s, at))
			}
		}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return plain(http.StatusServiceUnavailable, "unavailable")
	}
	return sdk.HTTPResponse{Status: http.StatusOK, Body: string(body), Headers: map[string]string{
		"Content-Type":  "application/json; charset=utf-8",
		"Cache-Control": "public, max-age=60",
	}}
}

// rangeFromQuery reads from, to, limit, slug and upcoming (1: only events that start at or after from). from and to are RFC 3339 or a
// date (midnight in the site zone); the range defaults to a month from now.
func rangeFromQuery(query map[string][]string, f filters, at time.Time, loc *time.Location) (rangeQuery, bool) {
	get := func(k string) string {
		if v := query[k]; len(v) == 1 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	q := rangeQuery{From: at, Q: f.Q, Category: f.Category, Venue: f.Venue, City: f.City, Price: f.Price, Limit: maxIndexRows}
	if v := get("from"); v != "" {
		t, ok := parseQueryTime(v, loc)
		if !ok {
			return q, false
		}
		q.From = t
	}
	q.To = q.From.Add(defaultSpan)
	if v := get("to"); v != "" {
		t, ok := parseQueryTime(v, loc)
		if !ok {
			return q, false
		}
		q.To = t
	}
	if !q.To.After(q.From) || q.To.Sub(q.From) > maxSpan {
		return q, false
	}
	if v := get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return q, false
		}
		q.Limit = min(n, maxIndexRows)
	}
	q.StartsAfter = get("upcoming") == "1"
	if v := get("slug"); v != "" {
		if !fileSlugRe.MatchString(v) {
			return q, false
		}
		q.Slug = v
	}
	return q, true
}

func parseQueryTime(v string, loc *time.Location) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	t, err := time.ParseInLocation("2006-01-02", v, loc)
	return t, err == nil
}

// toAPIEvent maps one occurrence row to its JSON shape.
func toAPIEvent(r apiRow, s settings, at time.Time) apiEvent {
	site := s.location()
	e := r.event(site)
	loc := dateLocation(e, site)
	zone := site // a timed event's days are the site's
	last := e.Start
	if e.HasEnd {
		last = e.End
	}
	if e.AllDay {
		zone = loc
	} else if last.After(e.Start) {
		last = last.Add(-time.Second) // ending at midnight ends the day before
	}
	const dateLayout = "2006-01-02"
	return apiEvent{
		ID: r.ID, Title: r.Name, Slug: r.Slug, URL: r.URL,
		Start: utcText(r.StartsAt), End: utcText(r.EndsAt),
		Day: e.Start.In(zone).Format(dateLayout), EndDay: last.In(zone).Format(dateLayout),
		AllDay: r.AllDay,
		When:   formatWhen(e.Start, e.End, e.HasEnd, e.AllDay, s.DateFormat, loc, at),
		Venue:  r.VenueName, VenueSlug: r.VenueSlug, City: r.VenueCity, Address: r.VenueAddress,
		Lat: r.Lat, Lon: r.Lon, Categories: nonNil(r.CategoryNames),
		Price: priceText(e), Image: r.Image, Featured: r.Featured, Status: statusOf(r.EventStatus),
	}
}

// statusOf is the event status, scheduled when the row has none.
func statusOf(s string) string {
	if s == "" {
		return "scheduled"
	}
	return s
}
