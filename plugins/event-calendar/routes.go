package main

import (
	"net/http"
	"regexp"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

var fileSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func init() { guest.OnHTTP(serve) }

// serve answers the two public routes. It never returns a Go error and never
// puts host error text in a body.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /events.ics":
		return serveFeed(), nil
	case "GET /event/{file}":
		return serveEvent(r.PathValues["file"]), nil
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
	evs, err := loadEvents(s)
	if err != nil {
		return plain(http.StatusServiceUnavailable, "unavailable")
	}
	loc := s.location()
	at := now()
	upcoming, _ := split(evs, at, loc)
	return calendar(writeCalendar(upcoming, at, loc), "")
}

// serveEvent is one published event as a calendar, past ones included.
func serveEvent(file string) sdk.HTTPResponse {
	slug, ok := strings.CutSuffix(file, ".ics")
	if !ok || !fileSlugRe.MatchString(slug) {
		return plain(http.StatusNotFound, "not found")
	}
	s := loadSettings()
	e, found, hostFailed := findEvent(s, slug)
	switch {
	case found:
		at := now()
		return calendar(writeCalendar([]Event{e}, at, s.location()), slug+".ics")
	case hostFailed:
		return plain(http.StatusServiceUnavailable, "unavailable")
	}
	return plain(http.StatusNotFound, "not found")
}

// findEvent looks a root-level event up by slug and falls back to scanning
// the list (translated or nested events). hostFailed is set when no answer
// could be had because the host failed.
func findEvent(s settings, slug string) (e Event, found, hostFailed bool) {
	var raw entityDTO
	err := callHost(sdk.FnEntitiesGet, map[string]any{
		"slug": slug, "parent_id": nil, "schema_slug": s.SchemaSlug,
	}, &raw)
	if err == nil && raw.ID != "" {
		if raw.SchemaSlug != "" && raw.SchemaSlug != s.SchemaSlug {
			return Event{}, false, false
		}
		e, found = eventFromEntity(raw, s)
		return e, found, false
	}
	evs, lerr := loadEvents(s)
	if lerr != nil {
		return Event{}, false, err != nil
	}
	for _, c := range evs {
		if c.Slug == slug {
			return c, true, false
		}
	}
	return Event{}, false, false
}
