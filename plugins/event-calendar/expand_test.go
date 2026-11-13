package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// repEvent builds an event starting at start (local wall time in zone) with
// the given rule.
func repEvent(t *testing.T, zone, start, end string, r Repeat) (Event, *time.Location) {
	t.Helper()
	loc := mustLoc(t, zone)
	s, ok := parseTime(start, loc)
	if !ok {
		t.Fatalf("bad start %q", start)
	}
	e := Event{ID: "e", Start: s, TimeZone: zone, Repeat: r}
	if end != "" {
		en, _ := parseTime(end, loc)
		e.End, e.HasEnd = en, true
	}
	return e, loc
}

func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	v, ok := parseTime(s, loc)
	if !ok {
		t.Fatalf("bad time %q", s)
	}
	return v
}

// local lists the starts as "2006-01-02 15:04" in loc.
func local(rows []occ, loc *time.Location) []string {
	var out []string
	for _, o := range rows {
		out = append(out, o.Start.In(loc).Format("2006-01-02 15:04"))
	}
	return out
}

func wantList(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func day(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	return startOfDay(at(t, loc, s), loc)
}

func TestExpandDaily(t *testing.T) {
	e, loc := repEvent(t, "Europe/Berlin", "2026-03-01T09:00", "2026-03-01T10:30", Repeat{Kind: "daily", Every: 2, Count: 4})
	rows := expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 100)
	wantList(t, local(rows, loc), "2026-03-01 09:00", "2026-03-03 09:00", "2026-03-05 09:00", "2026-03-07 09:00")
	if d := rows[1].End.Sub(rows[1].Start); d != 90*time.Minute {
		t.Errorf("length = %v", d)
	}
	// An event that never repeats keeps its one date, whatever the window.
	one, _ := repEvent(t, "Europe/Berlin", "2020-03-01T09:00", "", Repeat{Kind: "none"})
	if rows := expand(one, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 10); len(rows) != 1 || rows[0].End != one.Start {
		t.Errorf("single = %+v", rows)
	}
}

func TestExpandWeeklyWeekdays(t *testing.T) {
	// 2026-03-03 is a Tuesday.
	e, loc := repEvent(t, "UTC", "2026-03-03T18:00", "", Repeat{Kind: "weekly", Every: 1,
		Weekdays: []time.Weekday{time.Thursday, time.Monday}, Count: 5})
	rows := expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 100)
	// Dates before the first start are not generated.
	wantList(t, local(rows, loc), "2026-03-05 18:00", "2026-03-09 18:00", "2026-03-12 18:00", "2026-03-16 18:00", "2026-03-19 18:00")

	// Every 2 weeks, no weekday given: the start's own.
	e, loc = repEvent(t, "UTC", "2026-03-03T18:00", "", Repeat{Kind: "weekly", Every: 2, Count: 3})
	rows = expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 100)
	wantList(t, local(rows, loc), "2026-03-03 18:00", "2026-03-17 18:00", "2026-03-31 18:00")
}

func TestExpandMonthlyNthWeekday(t *testing.T) {
	// 2026-01-13 is the second Tuesday.
	e, loc := repEvent(t, "UTC", "2026-01-13T19:00", "", Repeat{Kind: "monthly", Every: 1, By: "nth_weekday", Count: 3})
	wantList(t, local(expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 99), loc),
		"2026-01-13 19:00", "2026-02-10 19:00", "2026-03-10 19:00")

	e.Repeat = Repeat{Kind: "monthly", Every: 1, By: "nth_weekday", Nth: -1, Count: 3}
	wantList(t, local(expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 99), loc),
		"2026-01-27 19:00", "2026-02-24 19:00", "2026-03-31 19:00")
}

func TestExpandMonthlyNthWeekdayLast(t *testing.T) {
	e, loc := repEvent(t, "UTC", "2026-01-01T19:00", "", Repeat{Kind: "monthly", Every: 1, By: "nth_weekday", Nth: -1, Count: 3})
	// 2026-01-01 is a Thursday; last Thursdays: Jan 29, Feb 26, Mar 26.
	wantList(t, local(expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 99), loc),
		"2026-01-29 19:00", "2026-02-26 19:00", "2026-03-26 19:00")

	// A fifth weekday that a month lacks skips the month (and is not counted).
	e.Start = at(t, loc, "2026-01-29T19:00") // fifth Thursday
	e.Repeat = Repeat{Kind: "monthly", Every: 1, By: "nth_weekday", Count: 3}
	wantList(t, local(expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 99), loc),
		"2026-01-29 19:00", "2026-04-30 19:00", "2026-07-30 19:00")
}

func TestExpandMonthlyDayOfMonthClamps(t *testing.T) {
	e, loc := repEvent(t, "UTC", "2026-01-31T08:00", "", Repeat{Kind: "monthly", Every: 1, Count: 4})
	wantList(t, local(expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 99), loc),
		"2026-01-31 08:00", "2026-02-28 08:00", "2026-03-31 08:00", "2026-04-30 08:00")
}

func TestExpandYearlyLeapDay(t *testing.T) {
	e, loc := repEvent(t, "UTC", "2024-02-29T12:00", "", Repeat{Kind: "yearly", Every: 1, Count: 3})
	wantList(t, local(expand(e, loc, at(t, loc, "2020-01-01"), at(t, loc, "2040-01-01"), 99), loc),
		"2024-02-29 12:00", "2028-02-29 12:00", "2032-02-29 12:00")
	e, loc = repEvent(t, "UTC", "2026-06-15T12:00", "", Repeat{Kind: "yearly", Every: 2, Count: 3})
	wantList(t, local(expand(e, loc, at(t, loc, "2020-01-01"), at(t, loc, "2040-01-01"), 99), loc),
		"2026-06-15 12:00", "2028-06-15 12:00", "2030-06-15 12:00")
}

func TestExpandEndsCountUntil(t *testing.T) {
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	e, loc := repEvent(t, "UTC", "2026-03-01T09:00", "", Repeat{Kind: "weekly", Every: 1})
	if n := len(expand(e, loc, from, to, 1000)); n != 96 { // never ends: the whole window
		t.Errorf("never = %d", n)
	}
	e.Repeat = Repeat{Kind: "daily", Every: 1, Until: day(t, loc, "2026-03-04")}
	wantList(t, local(expand(e, loc, from, to, 99), loc), "2026-03-01 09:00", "2026-03-02 09:00", "2026-03-03 09:00", "2026-03-04 09:00")
	e.Repeat = Repeat{Kind: "daily", Every: 1, Count: 2, Until: day(t, loc, "2026-03-31")}
	wantList(t, local(expand(e, loc, from, to, 99), loc), "2026-03-01 09:00", "2026-03-02 09:00")
	// Until before the first start: the single date.
	e.Repeat = Repeat{Kind: "daily", Every: 1, Until: day(t, loc, "2026-02-01")}
	wantList(t, local(expand(e, loc, from, to, 99), loc), "2026-03-01 09:00")
}

func TestExpandDSTLocalTime(t *testing.T) {
	// The clocks change in London on 2026-03-29 and in New York on 2026-03-08.
	for _, c := range []struct{ zone, start string }{
		{"Europe/London", "2026-03-27T20:30"},
		{"America/New_York", "2026-03-06T20:30"},
	} {
		e, loc := repEvent(t, c.zone, c.start, "", Repeat{Kind: "daily", Every: 1, Count: 6})
		rows := expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 99)
		if len(rows) != 6 {
			t.Fatalf("%s: %d rows", c.zone, len(rows))
		}
		for _, o := range rows {
			if h, m, _ := o.Start.In(loc).Clock(); h != 20 || m != 30 {
				t.Errorf("%s: %v", c.zone, o.Start.In(loc))
			}
		}
		if rows[2].Start.Sub(rows[1].Start) == rows[1].Start.Sub(rows[0].Start) && rows[3].Start.Sub(rows[2].Start) == rows[1].Start.Sub(rows[0].Start) {
			t.Errorf("%s: no 23h day across the change", c.zone)
		}
	}
	// A weekly event across the change, with a two hour length.
	e, loc := repEvent(t, "Europe/London", "2026-03-24T19:00", "2026-03-24T21:00", Repeat{Kind: "weekly", Every: 1, Count: 2})
	rows := expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 99)
	wantList(t, local(rows, loc), "2026-03-24 19:00", "2026-03-31 19:00")
	if d := rows[1].End.Sub(rows[1].Start); d != 2*time.Hour {
		t.Errorf("length = %v", d)
	}
}

func TestExpandAllDay(t *testing.T) {
	e, loc := repEvent(t, "Europe/London", "2026-03-27", "2026-03-28", Repeat{Kind: "weekly", Every: 1, Count: 3})
	e.AllDay = true
	e.Start, e.End = startOfDay(e.Start, loc), startOfDay(e.End, loc)
	rows := expand(e, loc, at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01"), 99)
	wantList(t, local(rows, loc), "2026-03-27 00:00", "2026-04-03 00:00", "2026-04-10 00:00")
	for _, o := range rows { // two calendar days, ending at a local midnight
		if h, _, _ := o.End.In(loc).Clock(); h != 0 || civilOf(o.End, loc).d != civilOf(o.Start, loc).addDays(2).d {
			t.Errorf("end = %v for %v", o.End.In(loc), o.Start.In(loc))
		}
	}
}

func TestExpandSkipExtraOverrides(t *testing.T) {
	e, loc := repEvent(t, "UTC", "2026-03-02T18:00", "2026-03-02T19:00", Repeat{Kind: "weekly", Every: 1, Count: 4,
		Skip:  []time.Time{day(t, time.UTC, "2026-03-09"), day(t, time.UTC, "2026-03-11")},
		Extra: []time.Time{day(t, time.UTC, "2026-03-11"), day(t, time.UTC, "2026-03-04"), day(t, time.UTC, "2026-03-16")},
		Over: map[string]Override{
			"2026-03-16": {Start: "2026-03-17T10:00", End: "2026-03-17T12:30"}, // moves to another day
			"2026-03-23": {Start: "2026-03-23T17:00"},                          // keeps its length
		}})
	from, to := at(t, loc, "2026-01-01"), at(t, loc, "2027-01-01")
	all := expandAll(e, loc, from, to, 99)
	var got []string
	for _, o := range all {
		got = append(got, fmt.Sprintf("%s %s", o.Start.Format("01-02 15:04"), o.State))
	}
	wantList(t, got,
		"03-02 18:00 normal",
		"03-04 18:00 normal",  // extra
		"03-09 18:00 skipped", // generated, skipped
		"03-11 18:00 skipped", // extra on a skipped day: skip wins
		"03-17 10:00 changed", // generated 03-16 moved a day; the extra of 03-16 merged into it
		"03-23 17:00 changed",
	)
	if d := all[4].End.Sub(all[4].Start); d != 150*time.Minute {
		t.Errorf("changed length = %v", d)
	}
	if d := all[5].End.Sub(all[5].Start); d != time.Hour {
		t.Errorf("kept length = %v", d)
	}
	rows := expand(e, loc, from, to, 99)
	wantList(t, local(rows, loc), "2026-03-02 18:00", "2026-03-04 18:00", "2026-03-17 10:00", "2026-03-23 17:00")
	// No two rows share a start.
	seen := map[time.Time]bool{}
	for _, o := range all {
		if seen[o.Start] {
			t.Errorf("duplicate start %v", o.Start)
		}
		seen[o.Start] = true
	}
}

func TestExpandWindowCap(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	from, to := windowFor(now)
	if !from.Equal(time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2027, 12, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %v .. %v", from, to)
	}
	e, loc := repEvent(t, "UTC", "2025-01-01T08:00", "", Repeat{Kind: "daily", Every: 1})
	rows := expand(e, loc, from, to, maxOccurrences)
	if len(rows) != 638 || !rows[0].Start.Equal(time.Date(2026, 3, 18, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily: %d rows from %v", len(rows), rows[0].Start)
	}
	// The row cap keeps the earliest.
	rows = expand(e, loc, from, from.AddDate(5, 0, 0), maxOccurrences)
	if len(rows) != maxOccurrences || !rows[0].Start.Equal(time.Date(2026, 3, 18, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("capped: %d rows", len(rows))
	}
	// Weekly fits: past 90 days kept, dates after 18 months not generated.
	e.Repeat = Repeat{Kind: "weekly", Every: 1}
	rows = expand(e, loc, from, to, maxOccurrences)
	if n := len(rows); n < 85 || n > 95 || rows[n-1].Start.After(to) || rows[0].Start.Before(from) {
		t.Fatalf("weekly: %d rows %v .. %v", n, rows[0].Start, rows[n-1].Start)
	}
	// A very old daily event still reaches the window.
	e, loc = repEvent(t, "UTC", "2000-01-01T08:00", "", Repeat{Kind: "daily", Every: 1})
	if rows := expand(e, loc, from, to, 5); len(rows) != 5 {
		t.Errorf("old daily = %d rows", len(rows))
	}
}
