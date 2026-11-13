package main

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// feedFillBudget is how long the feed may spend reading event details the
// index does not keep.
const feedFillBudget = 2 * time.Second

var fileSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func init() { guest.OnHTTP(serve) }

// serve answers the public routes. It never returns a Go error and never
// puts host error text in a body.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	// Core mounts GET routes, which also match HEAD; it drops the body of a
	// HEAD answer itself.
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		switch r.Pattern {
		case "/events.ics":
			return serveFeed(), nil
		case "/event/{file}":
			return serveEvent(r.PathValues["file"]), nil
		case "/api/events":
			return serveAPI(r.Query), nil
		case "/submit/token":
			return serveSubmitToken(), nil
		case "/admin/repeating":
			return serveRepeating(r.User), nil
		case "/series/{slug}":
			return servePage(pageSeries, r.PathValues["slug"]), nil
		case "/venue/{slug}":
			return servePage(pageVenue, r.PathValues["slug"]), nil
		case "/organizer/{slug}":
			return servePage(pageOrganizer, r.PathValues["slug"]), nil
		}
	}
	if r.Method == http.MethodPost && r.Pattern == "/submit" {
		return serveSubmit(r), nil
	}
	return plain(http.StatusNotFound, "not found"), nil
}

func plain(code int, body string) sdk.HTTPResponse {
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		Body:    body,
	}
}

func calendar(body, filename string) sdk.HTTPResponse {
	h := map[string]string{
		"Content-Type":  "text/calendar; charset=utf-8",
		"Cache-Control": "public, max-age=900",
	}
	if filename != "" {
		h["Content-Disposition"] = `inline; filename="` + filename + `"`
	}
	return sdk.HTTPResponse{Status: http.StatusOK, Headers: h, Body: body}
}

// serveFeed is the upcoming events as one calendar.
func serveFeed() sdk.HTTPResponse {
	s := loadSettings()
	at := now()
	upcoming, _, err := eventsFor(s, "", at, true, false, maxIndexRows, time.Time{})
	if err != nil {
		return plain(http.StatusServiceUnavailable, "unavailable")
	}
	deadline := clock().Add(feedFillBudget)
	var kept []Event
	venues, details := venueMemo{}, detailMemo{}
	for _, e := range upcoming {
		if e, keep := fillDetails(e, s, deadline, venues, details); keep {
			kept = append(kept, e)
		}
	}
	return calendar(writeCalendarWith(kept, at, s.location(), venues), "")
}

// serveEvent is one published event as a calendar, past ones included.
func serveEvent(file string) sdk.HTTPResponse {
	slug, ok := strings.CutSuffix(file, ".ics")
	if !ok || !fileSlugRe.MatchString(slug) {
		return notFound()
	}
	s := loadSettings()
	e, found, hostFailed := findEvent(s, slug)
	switch {
	case found:
		at := now()
		return calendar(writeCalendar(nextDates(e, s, at), at, s.location()), slug+".ics")
	case hostFailed:
		return plain(http.StatusServiceUnavailable, "unavailable")
	}
	return notFound()
}

// maxEventICSDates is how many upcoming dates a repeating event's own calendar
// file lists.
const maxEventICSDates = 12

// nextDates is the events to list in the calendar file of e: the event itself
// when it does not repeat or has no date left, else its next dates, each with
// the event's start as BaseStart so the UIDs match the feed. It works from the
// rule, so it needs no index.
func nextDates(e Event, s settings, at time.Time) []Event {
	if e.Repeat.Kind == "" || e.Repeat.Kind == "none" {
		return []Event{e}
	}
	loc := eventLocation(e, s)
	_, to := windowFor(at)
	occs := expand(e, loc, at, to, maxEventICSDates)
	if len(occs) == 0 {
		return []Event{e}
	}
	out := make([]Event, 0, len(occs))
	for _, o := range occs {
		d := e
		d.Start, d.End, d.HasEnd = indexTimes(o.Start.UTC(), o.End.UTC(), e.AllDay, e.TimeZone, loc)
		d.BaseStart = e.Start
		out = append(out, d)
	}
	return out
}

// notFound is a 404 that may be cached briefly, so repeated probes of a
// missing event are absorbed in front of the plugin.
func notFound() sdk.HTTPResponse {
	r := plain(http.StatusNotFound, "not found")
	r.Headers["Cache-Control"] = "public, max-age=60"
	return r
}

// findEvent looks a root-level event up by slug. Only when the host has no
// such root-level event does it scan the list, which is how translated or
// nested events are reached; the scan stops at the first match. hostFailed is
// set when no answer could be had because the host failed.
func findEvent(s settings, slug string) (e Event, found, hostFailed bool) {
	if evs, err := queryEvents(indexQuery{Upcoming: true, Slug: slug, Limit: 1, Loc: s.location()}); err == nil && len(evs) > 0 && s.accepts(evs[0].SchemaSlug) {
		// the index finds the event; the entity gives its full details
		var raw entityDTO
		if callHost(sdk.FnEntitiesGet, map[string]any{"id": evs[0].ID}, &raw) == nil {
			if e, found = eventFromEntity(raw, s); found {
				return e, true, false
			}
		}
	}
	for _, slugType := range s.slugs() {
		var raw entityDTO
		err := callHost(sdk.FnEntitiesGet, map[string]any{
			"slug": slug, "parent_id": nil, "schema_slug": slugType,
		}, &raw)
		var he *sdk.HostError
		switch {
		case err == nil && raw.ID != "":
			if raw.SchemaSlug != "" && !s.accepts(raw.SchemaSlug) {
				return Event{}, false, false
			}
			e, found = eventFromEntity(raw, s)
			return e, found, false
		case err != nil && !(errors.As(err, &he) && he.Code == sdk.ErrNotFound):
			return Event{}, false, true
		}
	}
	lerr := eachEvent(s, time.Time{}, func(c Event) bool {
		if c.Slug == slug {
			e, found = c, true
		}
		return found
	})
	return e, found, lerr != nil
}
