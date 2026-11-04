package main

import (
	"encoding/json"
	"strings"
	"time"

	"hatamoto.dev/sdk/guest"
)

func init() { guest.OnFunc("when", whenFunc) }

// whenFunc is the template func `when start_at end_at all_day`. Bad input
// renders nothing, never an error.
func whenFunc(args []json.RawMessage) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	s := loadSettings()
	loc := s.location()
	start, ok := parseTime(jsonString(args[0]), loc)
	if !ok {
		return "", nil
	}
	var end time.Time
	hasEnd := false
	if len(args) > 1 {
		if t, ok := parseTime(jsonString(args[1]), loc); ok && !t.Before(start) {
			end, hasEnd = t, true
		}
	}
	allDay := false
	if len(args) > 2 {
		allDay = jsonBool(args[2])
	}
	return formatWhen(start, end, hasEnd, allDay, s.DateFormat, loc, now()), nil
}

// jsonString reads a JSON string argument, empty for anything else.
func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// jsonBool reads a JSON bool or the string "true".
func jsonBool(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	return strings.EqualFold(strings.TrimSpace(jsonString(raw)), "true")
}

// formatDate renders a day in the chosen format; the year is added only when
// it differs from the year of at.
func formatDate(t, at time.Time, format string, loc *time.Location) string {
	t = t.In(loc)
	var out string
	switch format {
	case "iso":
		return t.Format("2006-01-02")
	case "us":
		out = t.Format("Mon, Jan 2")
		if t.Year() != at.In(loc).Year() {
			out += t.Format(", 2006")
		}
	default:
		out = t.Format("Mon 2 Jan")
		if t.Year() != at.In(loc).Year() {
			out += t.Format(" 2006")
		}
	}
	return out
}

// formatWhen renders an event's date and time as plain text, for example
// "Sat 14 Feb, 7:00 to 9:30 pm". Times are shown in loc.
func formatWhen(start, end time.Time, hasEnd, allDay bool, format string, loc *time.Location, at time.Time) string {
	start, end = start.In(loc), end.In(loc)
	day := func(t time.Time) string { return formatDate(t, at, format, loc) }
	clock := func(t time.Time) string { return t.Format("3:04 pm") }
	sameDay := hasEnd && sameCalendarDay(start, end)

	if allDay {
		if hasEnd && !sameDay {
			return day(start) + " to " + day(end) + " (all day)"
		}
		return day(start) + " (all day)"
	}
	if !hasEnd || end.Equal(start) {
		return day(start) + ", " + clock(start)
	}
	if !sameDay {
		return day(start) + ", " + clock(start) + " to " + day(end) + ", " + clock(end)
	}
	from := clock(start)
	if start.Format("pm") == end.Format("pm") {
		from = start.Format("3:04")
	}
	return day(start) + ", " + from + " to " + clock(end)
}

func sameCalendarDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
