package main

import (
	"sort"
	"time"
)

const (
	maxOccurrences = 800 // rows kept per event
	windowMonths   = 18  // how far ahead occurrences are written
	windowPastDays = 90  // how far back occurrences are kept
	maxIterations  = 20000
)

// Occurrence states.
const (
	stateNormal  = "normal"
	stateSkipped = "skipped"
	stateChanged = "changed"
)

// occ is one date of an event. End is when it is over (the next local
// midnight for an all-day date), as Event.endsAt.
type occ struct {
	Start, End time.Time
	State      string
}

// windowFor is the span occurrences are written for: from 90 days before now
// to 18 months after it.
func windowFor(now time.Time) (from, to time.Time) {
	return now.AddDate(0, 0, -windowPastDays), now.AddDate(0, windowMonths, 0)
}

// civil is a calendar date, free of any zone and time of day, so that a wall
// clock time is rebuilt for every date (never stepped in elapsed time, which
// a daylight saving change would shift).
type civil struct {
	y int
	m time.Month
	d int
}

func civilOf(t time.Time, loc *time.Location) civil {
	y, m, d := t.In(loc).Date()
	return civil{y, m, d}
}

// addDays normalises the day overflow (time.Date does).
func (c civil) addDays(n int) civil {
	return civilOf(time.Date(c.y, c.m, c.d+n, 12, 0, 0, 0, time.UTC), time.UTC)
}

func (c civil) at(h, mi, s int, loc *time.Location) time.Time {
	return time.Date(c.y, c.m, c.d, h, mi, s, 0, loc)
}

func (c civil) key() string { return c.at(0, 0, 0, time.UTC).Format("2006-01-02") }

func (c civil) before(o civil) bool { return c.key() < o.key() }

func (c civil) weekday() time.Weekday { return c.at(12, 0, 0, time.UTC).Weekday() }

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 12, 0, 0, 0, time.UTC).Day() }

// expand lists the dates of e inside [from, to), earliest first, at most max,
// without the skipped ones. loc is the zone the event's local times are read
// in (eventLocation). A single event always yields its one date.
func expand(e Event, loc *time.Location, from, to time.Time, max int) []occ {
	var out []occ
	for _, o := range expandAll(e, loc, from, to, max) {
		if o.State != stateSkipped {
			out = append(out, o)
		}
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// expandAll is expand that keeps the skipped dates, flagged. Rules: every date
// keeps the event's local time of day; COUNT counts the generated dates before
// skipping (extra dates are not counted); a skipped day beats an extra one; a
// changed date may move to another day; an until before the first start leaves
// the single date.
func expandAll(e Event, loc *time.Location, from, to time.Time, max int) []occ {
	base := e.Start.In(loc)
	end := e.endsAt(loc)
	single := []occ{{Start: base, End: end, State: stateNormal}}
	r := e.Repeat
	if r.Kind == "" || r.Kind == "none" {
		return single
	}
	startDay := civilOf(base, loc)
	if !r.Until.IsZero() && civilOf(r.Until, loc).before(startDay) {
		return single
	}
	h, mi, s := base.Clock()
	if e.AllDay {
		h, mi, s = 0, 0, 0
	}
	length := func(start civil) time.Time { // End of a date starting on day start
		if e.AllDay {
			days := int(civilOf(end, loc).at(12, 0, 0, time.UTC).Sub(startDay.at(12, 0, 0, time.UTC)) / (24 * time.Hour))
			return start.addDays(days).at(0, 0, 0, loc)
		}
		return start.at(h, mi, s, loc).Add(end.Sub(base))
	}

	skip := map[string]bool{}
	for _, d := range r.Skip {
		skip[civilOf(d, loc).key()] = true
	}
	var rows []occ
	add := func(day civil) {
		o := occ{Start: day.at(h, mi, s, loc), End: length(day), State: stateNormal}
		if skip[day.key()] {
			o.State = stateSkipped
		} else if ov, ok := r.Over[day.key()]; ok {
			o = override(o, ov, e.AllDay, loc)
		}
		rows = append(rows, o)
	}
	generate(r, startDay, base.Weekday(), loc, to, func(day civil) { add(day) })
	for _, d := range r.Extra {
		add(civilOf(d, loc))
	}

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Start.Before(rows[j].Start) })
	var out []occ
	for _, o := range rows {
		if o.Start.Before(from) || !o.Start.Before(to) {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Start.Equal(o.Start) {
			if out[n-1].State == stateSkipped && o.State != stateSkipped {
				out[n-1] = o
			}
			continue
		}
		out = append(out, o)
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// override applies a changed date: a new start (and end, else the same
// length). An unreadable start leaves the date as generated.
func override(o occ, ov Override, allDay bool, loc *time.Location) occ {
	start, ok := parseTime(ov.Start, loc)
	if !ok {
		return o
	}
	length := o.End.Sub(o.Start)
	if allDay {
		start = startOfDay(start, loc)
		days := civilOf(o.End, loc).at(12, 0, 0, time.UTC).Sub(civilOf(o.Start, loc).at(12, 0, 0, time.UTC))
		o.End = civilOf(start, loc).addDays(int(days/(24*time.Hour))).at(0, 0, 0, loc)
	} else {
		o.End = start.Add(length)
	}
	o.Start = start
	if end, ok := parseTime(ov.End, loc); ok {
		if allDay {
			end = civilOf(end, loc).addDays(1).at(0, 0, 0, loc)
		}
		if end.After(start) || end.Equal(start) {
			o.End = end
		}
	}
	o.State = stateChanged
	return o
}

// generate calls emit with each date the rule produces, earliest first, from
// the start day on, until the rule ends, the window ends (to) or the loop
// bound is hit.
func generate(r Repeat, start civil, wd time.Weekday, loc *time.Location, to time.Time, emit func(civil)) {
	every := r.Every
	if every < 1 {
		every = 1
	}
	var until civil
	hasUntil := !r.Until.IsZero()
	if hasUntil {
		until = civilOf(r.Until, loc)
	}
	weekdays := r.Weekdays
	if len(weekdays) == 0 {
		weekdays = []time.Weekday{wd}
	}
	n := 0
	for k := 0; k < maxIterations; k++ {
		period, days := periodDates(r, start, wd, weekdays, every, k)
		if !period.at(0, 0, 0, loc).Before(to) {
			return
		}
		for _, d := range days {
			if d.before(start) {
				continue
			}
			if hasUntil && until.before(d) || r.Count > 0 && n >= r.Count {
				return
			}
			emit(d)
			n++
		}
	}
}

// periodDates is the k-th period of the rule (a day, a week from Monday, a
// month or a year): its first day and the dates of the rule inside it.
func periodDates(r Repeat, start civil, wd time.Weekday, weekdays []time.Weekday, every, k int) (civil, []civil) {
	switch r.Kind {
	case "weekly":
		monday := start.addDays(-((int(wd) + 6) % 7)).addDays(k * every * 7)
		var days []civil
		for off := 0; off < 7; off++ {
			d := monday.addDays(off)
			for _, w := range weekdays {
				if d.weekday() == w {
					days = append(days, d)
				}
			}
		}
		return monday, days
	case "monthly":
		first := civilOf(time.Date(start.y, start.m+time.Month(k*every), 1, 12, 0, 0, 0, time.UTC), time.UTC)
		if r.By == "nth_weekday" {
			nth := r.Nth
			if nth == 0 {
				nth = (start.d-1)/7 + 1
			}
			if d, ok := nthWeekday(first.y, first.m, wd, nth); ok {
				return first, []civil{d}
			}
			return first, nil
		}
		d := start.d
		if dim := daysIn(first.y, first.m); d > dim {
			d = dim
		}
		return first, []civil{{first.y, first.m, d}}
	case "yearly":
		first := civil{start.y + k*every, 1, 1}
		if start.m == time.February && start.d == 29 && daysIn(first.y, time.February) != 29 {
			return first, nil
		}
		return first, []civil{{first.y, start.m, start.d}}
	}
	day := start.addDays(k * every)
	return day, []civil{day}
}

// nthWeekday is the nth (1 to 5; -1 the last) weekday wd of the month; false
// when the month has no such day.
func nthWeekday(y int, m time.Month, wd time.Weekday, nth int) (civil, bool) {
	dim := daysIn(y, m)
	if nth == -1 {
		last := civil{y, m, dim}
		return civil{y, m, dim - (int(last.weekday())-int(wd)+7)%7}, true
	}
	first := 1 + (int(wd)-int(civil{y, m, 1}.weekday())+7)%7
	if d := first + 7*(nth-1); nth >= 1 && d <= dim {
		return civil{y, m, d}, true
	}
	return civil{}, false
}
