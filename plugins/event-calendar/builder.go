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
	icsPathPrefix  = "/p/event-calendar/event/"
)

func init() { guest.OnFilter(sdk.BuilderData, answerBuilder) }

// answerBuilder answers the two event sources and leaves other requests
// alone. A failure yields an empty list, never an error string.
func answerBuilder(r sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
	var upcoming, past []Event
	loaded := false
	var s settings
	var at time.Time
	for _, q := range r.Requests {
		if q.Source != sourceUpcoming && q.Source != sourcePast {
			continue
		}
		if !loaded {
			loaded = true
			s = loadSettings()
			at = now()
			if evs, err := loadEvents(s); err == nil {
				upcoming, past = split(filterLanguage(evs, r.Language), at, s.location())
			}
		}
		list := upcoming
		if q.Source == sourcePast {
			list = past
		}
		limit := q.Limit
		if limit <= 0 || limit > sdk.MaxBuilderDataItems {
			limit = sdk.MaxBuilderDataItems
		}
		if len(list) > limit {
			list = list[:limit]
		}
		res := sdk.BuilderDataResult{ID: q.ID}
		for _, e := range list {
			res.Items = append(res.Items, toItem(e, s, at))
		}
		r.Results = append(r.Results, res)
	}
	return r, nil
}

// filterLanguage keeps events of the given language and those without one;
// no language keeps all.
func filterLanguage(evs []Event, lang string) []Event {
	if lang == "" {
		return evs
	}
	var out []Event
	for _, e := range evs {
		if e.Locale == "" || e.Locale == lang {
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
	loc := s.location()
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
