package main

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func icsBody(events ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Example//EN\r\n" + strings.Join(events, "") + "END:VCALENDAR\r\n"
}

func vevent(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func TestParseICalEvents(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skip("no tz data")
	}
	utc := time.UTC
	at := func(loc *time.Location, y int, m time.Month, d, h, mi int) time.Time {
		return time.Date(y, m, d, h, mi, 0, 0, loc)
	}
	longTitle := strings.Repeat("a", 300)

	cases := []struct {
		name  string
		lines []string
		loc   *time.Location
		check func(t *testing.T, e importEvent)
	}{
		{"utc", []string{"UID:u1@example.org", "SUMMARY:Open mic", "DTSTART:20260612T183000Z", "DTEND:20260612T203000Z"}, london,
			func(t *testing.T, e importEvent) {
				if e.UID != "u1@example.org" || e.Title != "Open mic" || !e.Start.Equal(at(utc, 2026, 6, 12, 18, 30)) || !e.End.Equal(at(utc, 2026, 6, 12, 20, 30)) || e.AllDay {
					t.Errorf("%+v", e)
				}
				if e.Start.Location() != london {
					t.Errorf("start is not in the plugin zone: %v", e.Start.Location())
				}
			}},
		{"tzid", []string{"UID:u2", "SUMMARY:Choir", "DTSTART;TZID=Europe/London:20260612T190000", "DTEND;TZID=Europe/London:20260612T210000"}, utc,
			func(t *testing.T, e importEvent) {
				if !e.Start.Equal(at(london, 2026, 6, 12, 19, 0)) || !e.End.Equal(at(london, 2026, 6, 12, 21, 0)) {
					t.Errorf("%v - %v", e.Start, e.End)
				}
			}},
		{"unknown tzid falls back", []string{"UID:u3", "SUMMARY:x", "DTSTART;TZID=Nowhere/Land:20260612T190000"}, london,
			func(t *testing.T, e importEvent) {
				if !e.Start.Equal(at(london, 2026, 6, 12, 19, 0)) || !e.End.IsZero() {
					t.Errorf("%v - %v", e.Start, e.End)
				}
			}},
		{"floating", []string{"UID:u4", "SUMMARY:x", "DTSTART:20260612T190000"}, london,
			func(t *testing.T, e importEvent) {
				if !e.Start.Equal(at(london, 2026, 6, 12, 19, 0)) {
					t.Errorf("%v", e.Start)
				}
			}},
		{"all day, exclusive end", []string{"UID:u5", "SUMMARY:Fair", "DTSTART;VALUE=DATE:20260612", "DTEND;VALUE=DATE:20260615"}, london,
			func(t *testing.T, e importEvent) {
				if !e.AllDay || !e.Start.Equal(at(london, 2026, 6, 12, 0, 0)) || !e.End.Equal(at(london, 2026, 6, 14, 0, 0)) {
					t.Errorf("%+v", e)
				}
			}},
		{"all day, no end", []string{"UID:u6", "SUMMARY:Day", "DTSTART;VALUE=DATE:20260612"}, london,
			func(t *testing.T, e importEvent) {
				if !e.AllDay || !e.End.Equal(e.Start) {
					t.Errorf("%+v", e)
				}
			}},
		{"folded and escaped", []string{"UID:u7", "SUMMARY:Rock\\, paper\\; scissors", "DTSTART:20260612T100000Z",
			"DESCRIPTION:Line one\\nLine two with a very long tail that is folded by the pub", " lisher in the middle of a word\\\\ done",
			"LOCATION:Town Hall\\nMain Street 1\\nExample City"}, utc,
			func(t *testing.T, e importEvent) {
				if e.Title != "Rock, paper; scissors" {
					t.Errorf("title %q", e.Title)
				}
				if want := "Line one\nLine two with a very long tail that is folded by the publisher in the middle of a word\\ done"; e.Description != want {
					t.Errorf("description %q", e.Description)
				}
				if e.Venue != "Town Hall" || e.Address != "Main Street 1, Example City" {
					t.Errorf("venue %q address %q", e.Venue, e.Address)
				}
			}},
		{"cancelled and url", []string{"UID:u8", "SUMMARY:x", "DTSTART:20260612T100000Z", "STATUS:CANCELLED", "URL:https://example.org/e/8"}, utc,
			func(t *testing.T, e importEvent) {
				if e.Status != "cancelled" || e.Website != "https://example.org/e/8" {
					t.Errorf("%+v", e)
				}
			}},
		{"relative url dropped", []string{"UID:u9", "SUMMARY:x", "DTSTART:20260612T100000Z", "URL:/e/9", "STATUS:TENTATIVE"}, utc,
			func(t *testing.T, e importEvent) {
				if e.Website != "" || e.Status != "tentative" {
					t.Errorf("%+v", e)
				}
			}},
		{"caps and control characters", []string{"UID:u10", "SUMMARY:" + longTitle + "\x07", "DTSTART:20260612T100000Z"}, utc,
			func(t *testing.T, e importEvent) {
				if len(e.Title) != maxImportTitle {
					t.Errorf("title length %d", len(e.Title))
				}
			}},
		{"weekly rule with exceptions", []string{"UID:r1", "SUMMARY:Yoga", "DTSTART:20260602T180000Z", "RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,TH;UNTIL=20261231T000000Z;WKST=MO",
			"EXDATE:20260616T180000Z,20260630T180000Z", "RDATE;VALUE=DATE:20261224"}, utc,
			func(t *testing.T, e importEvent) {
				want := map[string]any{"repeat": "weekly", "repeat_every": 2, "repeat_weekdays": []string{"tu", "th"}, "repeat_until": "2026-12-31",
					"repeat_skip_dates": []string{"2026-06-16", "2026-06-30"}, "repeat_extra_dates": []string{"2026-12-24"}}
				if !reflect.DeepEqual(e.Repeat, want) || e.Dropped != 0 {
					t.Errorf("repeat %v dropped %d", e.Repeat, e.Dropped)
				}
			}},
		{"monthly nth weekday with count", []string{"UID:r2", "SUMMARY:Club", "DTSTART:20260611T183000Z", "RRULE:FREQ=MONTHLY;BYDAY=2TH;COUNT=12"}, utc,
			func(t *testing.T, e importEvent) {
				want := map[string]any{"repeat": "monthly", "repeat_by": "nth_weekday", "repeat_nth": 2, "repeat_count": 12}
				if !reflect.DeepEqual(e.Repeat, want) {
					t.Errorf("repeat %v", e.Repeat)
				}
			}},
		{"last friday", []string{"UID:r3", "SUMMARY:Club", "DTSTART:20260626T183000Z", "RRULE:FREQ=MONTHLY;BYDAY=-1FR"}, utc,
			func(t *testing.T, e importEvent) {
				if e.Repeat["repeat_nth"] != -1 {
					t.Errorf("repeat %v", e.Repeat)
				}
			}},
		{"daily and yearly", []string{"UID:r4", "SUMMARY:Daily", "DTSTART:20260626T183000Z", "RRULE:FREQ=DAILY"}, utc,
			func(t *testing.T, e importEvent) {
				if !reflect.DeepEqual(e.Repeat, map[string]any{"repeat": "daily"}) {
					t.Errorf("repeat %v", e.Repeat)
				}
			}},
		{"unsupported rule imports the first date", []string{"UID:r5", "SUMMARY:Odd", "DTSTART:20260626T183000Z", "RRULE:FREQ=MONTHLY;BYSETPOS=-1;BYDAY=FR", "EXDATE:20260703T183000Z"}, utc,
			func(t *testing.T, e importEvent) {
				if e.Repeat != nil || e.Dropped != 1 {
					t.Errorf("repeat %v dropped %d", e.Repeat, e.Dropped)
				}
			}},
		{"hourly is unsupported", []string{"UID:r6", "SUMMARY:Odd", "DTSTART:20260626T183000Z", "RRULE:FREQ=HOURLY"}, utc,
			func(t *testing.T, e importEvent) {
				if e.Repeat != nil || e.Dropped != 1 {
					t.Errorf("repeat %v dropped %d", e.Repeat, e.Dropped)
				}
			}},
		{"nth weekday of another day is unsupported", []string{"UID:r7", "SUMMARY:Odd", "DTSTART:20260626T183000Z", "RRULE:FREQ=MONTHLY;BYDAY=2TH"}, utc,
			func(t *testing.T, e importEvent) {
				if e.Repeat != nil || e.Dropped != 1 {
					t.Errorf("repeat %v dropped %d", e.Repeat, e.Dropped)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseICalEvents(icsBody(vevent(c.lines...)), c.loc)
			if err != nil || len(got) != 1 {
				t.Fatalf("events %v, err %v", got, err)
			}
			c.check(t, got[0])
		})
	}

	t.Run("skipped events", func(t *testing.T) {
		body := icsBody(
			vevent("UID:ok", "SUMMARY:Kept", "DTSTART:20260612T100000Z"),
			vevent("UID:ok", "RECURRENCE-ID:20260619T100000Z", "SUMMARY:Override", "DTSTART:20260620T100000Z"),
			vevent("SUMMARY:No uid", "DTSTART:20260612T100000Z"),
			vevent("UID:nostart", "SUMMARY:No start"),
			vevent("UID:badstart", "SUMMARY:Bad", "DTSTART:soon"),
			vevent("UID:alarm", "SUMMARY:With alarm", "DTSTART:20260613T100000Z", "BEGIN:VALARM", "ACTION:DISPLAY", "DESCRIPTION:ring", "END:VALARM"),
		)
		got, err := parseICalEvents(body, utc)
		if err != nil || len(got) != 2 || got[0].UID != "ok" || got[1].UID != "alarm" || got[1].Description != "" {
			t.Errorf("events %+v, err %v", got, err)
		}
	})

	t.Run("at most 500 events", func(t *testing.T) {
		var b strings.Builder
		for i := range 600 {
			b.WriteString(vevent("UID:e"+strconv.Itoa(i), "SUMMARY:x", "DTSTART:20260612T100000Z"))
		}
		got, err := parseICalEvents(icsBody(b.String()), utc)
		if err != nil || len(got) != maxICalEvents {
			t.Errorf("events %d, err %v", len(got), err)
		}
	})

	t.Run("garbage", func(t *testing.T) {
		for _, body := range []string{"", "<html>nope</html>", "BEGIN:VCALENDAR", "BEGIN:VCALENDAR\r\nEND:VEVENT\r\nEND:VCALENDAR", "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID", "\x00\x01:::"} {
			got, _ := parseICalEvents(body, utc)
			if len(got) != 0 {
				t.Errorf("%q gave %v", body, got)
			}
		}
	})
}
