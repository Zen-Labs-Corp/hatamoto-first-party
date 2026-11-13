package main

import (
	"strings"
	"testing"
	"time"
)

func ldPage(blocks ...string) string {
	s := "<html><head><title>Example</title>"
	for _, b := range blocks {
		s += `<script type="application/ld+json">` + b + `</script>`
	}
	return s + "</head><body></body></html>"
}

func TestParseJSONLDEvents(t *testing.T) {
	loc := time.UTC
	single := `{"@context":"https://schema.org","@type":"Event","@id":"https://example.org/e/1#event","name":"Open mic &amp; jam",
"description":"<p>Bring an <b>instrument</b>.</p>","startDate":"2026-06-12T19:00:00+00:00","endDate":"2026-06-12T22:00:00+00:00",
"url":"https://example.org/e/1","eventStatus":"https://schema.org/EventCancelled",
"offers":{"@type":"Offer","url":"https://example.org/tickets/1"},
"location":{"@type":"Place","name":"Town Hall","address":{"@type":"PostalAddress","streetAddress":"Main Street 1","addressLocality":"Example City"}}}`

	t.Run("single event", func(t *testing.T) {
		got := parseJSONLDEvents(ldPage(single), loc)
		if len(got) != 1 {
			t.Fatalf("events %+v", got)
		}
		e := got[0]
		if e.UID != "https://example.org/e/1#event" || e.Title != "Open mic & jam" || e.Description != "Bring an instrument." {
			t.Errorf("%+v", e)
		}
		if !e.Start.Equal(time.Date(2026, 6, 12, 19, 0, 0, 0, time.UTC)) || !e.End.Equal(time.Date(2026, 6, 12, 22, 0, 0, 0, time.UTC)) || e.AllDay {
			t.Errorf("times %v - %v", e.Start, e.End)
		}
		if e.Status != "cancelled" || e.Website != "https://example.org/e/1" || e.TicketURL != "https://example.org/tickets/1" {
			t.Errorf("%+v", e)
		}
		if e.Venue != "Town Hall" || e.Address != "Main Street 1" || e.City != "Example City" {
			t.Errorf("place %+v", e)
		}
	})

	t.Run("array and graph", func(t *testing.T) {
		arr := `[{"@type":"MusicEvent","name":"A","startDate":"2026-07-01"},{"@type":["Thing","Event"],"name":"B","startDate":"2026-07-02T10:00:00"},{"@type":"Organization","name":"Org"}]`
		graph := `{"@context":"https://schema.org","@graph":[{"@type":"WebSite","name":"Site"},{"@type":"schema:Event","name":"C","url":"https://example.org/c","startDate":"2026-07-03T10:00:00Z"}]}`
		got := parseJSONLDEvents(ldPage(arr, graph), loc)
		if len(got) != 3 || got[0].Title != "A" || got[1].Title != "B" || got[2].Title != "C" {
			t.Fatalf("events %+v", got)
		}
		if !got[0].AllDay || !got[0].End.Equal(got[0].Start) {
			t.Errorf("date-only start is an all day event: %+v", got[0])
		}
		if got[1].UID != "B|2026-07-02T10:00:00" || got[2].UID != "https://example.org/c" {
			t.Errorf("uids %q %q", got[1].UID, got[2].UID)
		}
	})

	t.Run("online event", func(t *testing.T) {
		on := `{"@type":"Event","name":"Webinar","startDate":"2026-07-03T10:00:00Z","eventAttendanceMode":"https://schema.org/OnlineEventAttendanceMode",
"location":{"@type":"VirtualLocation","url":"https://example.com/join/1"}}`
		got := parseJSONLDEvents(ldPage(on), loc)
		if len(got) != 1 || got[0].OnlineURL != "https://example.com/join/1" || got[0].Venue != "" {
			t.Errorf("%+v", got)
		}
	})

	t.Run("nothing usable", func(t *testing.T) {
		for name, page := range map[string]string{
			"bad json":       ldPage(`{"@type":"Event",`, `not json`),
			"organizer only": ldPage(`{"@type":"Organization","name":"Example Org","event":[{"@type":"Event","name":"nested"}]}`),
			"no start":       ldPage(`{"@type":"Event","name":"Undated"}`),
			"no name":        ldPage(`{"@type":"Event","startDate":"2026-07-03"}`),
			"no blocks":      "<html><body>hello</body></html>",
			"empty":          "",
		} {
			if got := parseJSONLDEvents(page, loc); len(got) != 0 {
				t.Errorf("%s: %+v", name, got)
			}
		}
	})

	t.Run("a bad block does not hide the next", func(t *testing.T) {
		got := parseJSONLDEvents(ldPage(`{oops`, `{"@type":"Event","name":"Ok","startDate":"2026-07-03"}`), loc)
		if len(got) != 1 || got[0].Title != "Ok" {
			t.Errorf("%+v", got)
		}
	})
}

func TestNormaliseFeedURL(t *testing.T) {
	for in, want := range map[string]string{
		"webcal://example.org/cal.ics":      "https://example.org/cal.ics",
		"WEBCAL://example.org/cal.ics":      "https://example.org/cal.ics",
		"webcals://example.org/cal.ics?a=1": "https://example.org/cal.ics?a=1",
		"  https://example.org/cal.ics ":    "https://example.org/cal.ics",
		"http://example.org/cal.ics":        "http://example.org/cal.ics",
	} {
		if got, err := normaliseFeedURL(in); err != nil || got != want {
			t.Errorf("normaliseFeedURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ftp://example.org/x", "file:///etc/passwd", "https://", "https://user:pw@example.org/x", "javascript:alert(1)",
		"https://example.org/a b", "https://example.org/\nx", "example.org/cal.ics", "https://example.org/" + strings.Repeat("a", 2100)} {
		if got, err := normaliseFeedURL(in); err == nil {
			t.Errorf("normaliseFeedURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{"https://Calendar.Example.org:8443/x?y=1": "calendar.example.org", "https://example.org": "example.org", "nonsense": "", "": ""} {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}
