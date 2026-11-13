package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestEscapeText(t *testing.T) {
	got := escapeText("a\\b;c,d\r\ne\nf\rg")
	if want := `a\\b\;c\,d\ne\nfg`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func unfold(s string) string { return strings.ReplaceAll(s, "\r\n ", "") }

func TestFoldBoundaries(t *testing.T) {
	for _, unit := range []string{"a", "é", "日", "😀"} {
		for n := 1; n < 120; n++ {
			in := "SUMMARY:" + strings.Repeat(unit, n)
			out := fold(in)
			for _, l := range strings.Split(out, "\r\n") {
				if len(l) > 75 {
					t.Fatalf("%q x%d: line of %d octets", unit, n, len(l))
				}
				if !utf8.ValidString(l) {
					t.Fatalf("%q x%d: split inside a rune", unit, n)
				}
			}
			if unfold(out) != in {
				t.Fatalf("%q x%d: unfold differs", unit, n)
			}
			if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
				t.Fatalf("bare LF")
			}
		}
	}
	if fold("short") != "short" {
		t.Fatal("short line changed")
	}
}

func TestWriteCalendarGolden(t *testing.T) {
	loc := time.UTC
	stamp := time.Date(2026, 11, 4, 9, 15, 0, 0, time.UTC)
	evs := []Event{
		{
			ID: "3f2a", Slug: "jazz", Name: "Winter jazz night", URL: "https://example.com/events/jazz",
			Venue: "The Blue Room", Address: "12 High St", TicketURL: "https://tickets.example.com/jazz",
			Start: time.Date(2027, 2, 14, 19, 0, 0, 0, time.UTC), End: time.Date(2027, 2, 14, 21, 30, 0, 0, time.UTC), HasEnd: true,
		},
		{
			ID: "9", Slug: "market", Name: "Spring market", URL: "/events/market", Description: "Stalls; food",
			Start: time.Date(2027, 3, 7, 0, 0, 0, 0, time.UTC), End: time.Date(2027, 3, 8, 0, 0, 0, 0, time.UTC), HasEnd: true, AllDay: true,
			Updated: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		},
	}
	want := strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Hatamoto//event-calendar 0.3.0//EN", "CALSCALE:GREGORIAN", "METHOD:PUBLISH",
		"BEGIN:VEVENT", "UID:3f2a@event-calendar.hatamoto", "DTSTAMP:20261104T091500Z",
		"DTSTART:20270214T190000Z", "DTEND:20270214T213000Z", "SUMMARY:Winter jazz night",
		`LOCATION:The Blue Room\, 12 High St`, "URL:https://example.com/events/jazz",
		"DESCRIPTION:Tickets: https://tickets.example.com/jazz", "END:VEVENT",
		"BEGIN:VEVENT", "UID:9@event-calendar.hatamoto", "DTSTAMP:20261001T080000Z",
		"DTSTART;VALUE=DATE:20270307", "DTEND;VALUE=DATE:20270309", "SUMMARY:Spring market",
		`DESCRIPTION:Stalls\; food`, "END:VEVENT", "END:VCALENDAR", "",
	}, "\r\n")
	if got := writeCalendar(evs, stamp, loc); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestWriteCalendarEdgeCases(t *testing.T) {
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	empty := writeCalendar(nil, stamp, time.UTC)
	if !strings.HasPrefix(empty, "BEGIN:VCALENDAR\r\n") || !strings.HasSuffix(empty, "END:VCALENDAR\r\n") || strings.Contains(empty, "VEVENT") {
		t.Fatalf("empty feed = %q", empty)
	}
	// A single-day all-day event ends the next day; no end on a timed event omits DTEND.
	out := writeCalendar([]Event{
		{ID: "a", Name: "A", AllDay: true, Start: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)},
		{ID: "b", Name: strings.Repeat("日本語", 40), Start: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)},
	}, stamp, time.UTC)
	if !strings.Contains(out, "DTEND;VALUE=DATE:20270101\r\n") {
		t.Errorf("all-day DTEND: %q", out)
	}
	if strings.Contains(out[strings.Index(out, "UID:b"):], "DTEND") {
		t.Errorf("timed event without end has DTEND")
	}
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Error("bare LF")
	}
	for _, l := range strings.Split(out, "\r\n") {
		if len(l) > 75 || !utf8.ValidString(l) {
			t.Errorf("bad line %q", l)
		}
	}
	if strings.Contains(out, "URL:") {
		t.Error("URL emitted without a value")
	}
}

func TestWriteCalendarStatus(t *testing.T) {
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	out := writeCalendar([]Event{
		{ID: "a", Name: "A", Start: start, EventStatus: "cancelled"},
		{ID: "b", Name: "B", Start: start, EventStatus: "postponed"},
		{ID: "c", Name: "C", Start: start, EventStatus: "scheduled"},
	}, stamp, time.UTC)
	if strings.Count(out, "STATUS:CANCELLED\r\n") != 1 || strings.Count(out, "STATUS:TENTATIVE\r\n") != 1 || strings.Count(out, "STATUS:") != 2 {
		t.Errorf("status lines: %q", out)
	}
}

func TestWriteCalendarVenueRef(t *testing.T) {
	(&fakeHost{entities: []string{venueDoc}}).install(t)
	out := writeCalendar([]Event{
		{ID: "a", Name: "A", Start: time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC), VenueID: "v1"},
		{ID: "b", Name: "B", Start: time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC), VenueID: "gone", Venue: "Text hall"},
	}, time.Now(), time.UTC)
	if !strings.Contains(out, `LOCATION:Old Mill\, 12 High St\, Leeds`) || !strings.Contains(out, "LOCATION:Text hall\r\n") {
		t.Errorf("locations: %q", out)
	}
}

func TestWriteCalendarTextVenueKeepsTheCity(t *testing.T) {
	(&fakeHost{}).install(t)
	out := writeCalendar([]Event{
		{ID: "a", Name: "A", Start: time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC), Venue: "Town hall", Address: "1 High St", VenueCity: "Leeds"},
	}, time.Now(), time.UTC)
	if !strings.Contains(out, `LOCATION:Town hall\, 1 High St\, Leeds`) {
		t.Errorf("location: %q", out)
	}
}
