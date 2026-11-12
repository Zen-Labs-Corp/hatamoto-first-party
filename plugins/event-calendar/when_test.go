package main

import (
	"encoding/json"
	"testing"
	"time"

	"hatamoto.dev/sdk/guest"
)

func TestFormatWhen(t *testing.T) {
	loc := time.UTC
	at := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	d := func(y int, m time.Month, day, h, min int) time.Time {
		return time.Date(y, m, day, h, min, 0, 0, time.UTC)
	}
	cases := []struct {
		name          string
		start, end    time.Time
		hasEnd, allDy bool
		format, want  string
	}{
		{"same meridiem", d(2026, 2, 14, 19, 0), d(2026, 2, 14, 21, 30), true, false, "short", "Sat 14 Feb, 7:00 to 9:30 pm"},
		{"differing meridiem", d(2026, 2, 14, 11, 0), d(2026, 2, 14, 13, 0), true, false, "short", "Sat 14 Feb, 11:00 am to 1:00 pm"},
		{"no end", d(2026, 2, 14, 19, 0), time.Time{}, false, false, "short", "Sat 14 Feb, 7:00 pm"},
		{"end equals start", d(2026, 2, 14, 19, 0), d(2026, 2, 14, 19, 0), true, false, "short", "Sat 14 Feb, 7:00 pm"},
		{"multi-day timed", d(2026, 2, 14, 19, 0), d(2026, 2, 15, 1, 0), true, false, "short", "Sat 14 Feb, 7:00 pm to Sun 15 Feb, 1:00 am"},
		{"all day", d(2026, 3, 7, 0, 0), time.Time{}, false, true, "short", "Sat 7 Mar (all day)"},
		{"all day same-day end", d(2026, 3, 7, 0, 0), d(2026, 3, 7, 0, 0), true, true, "short", "Sat 7 Mar (all day)"},
		{"all day multi-day", d(2026, 3, 7, 0, 0), d(2026, 3, 8, 0, 0), true, true, "short", "Sat 7 Mar to Sun 8 Mar (all day)"},
		{"us", d(2026, 2, 14, 19, 0), d(2026, 2, 14, 21, 30), true, false, "us", "Sat, Feb 14, 7:00 to 9:30 pm"},
		{"iso", d(2026, 2, 14, 19, 0), d(2026, 2, 14, 21, 30), true, false, "iso", "2026-02-14, 7:00 to 9:30 pm"},
		{"other year short", d(2028, 2, 14, 19, 0), time.Time{}, false, false, "short", "Mon 14 Feb 2028, 7:00 pm"},
		{"other year us", d(2028, 2, 14, 19, 0), time.Time{}, false, false, "us", "Mon, Feb 14, 2028, 7:00 pm"},
		{"midnight and noon", d(2026, 2, 14, 0, 0), d(2026, 2, 14, 12, 0), true, false, "short", "Sat 14 Feb, 12:00 am to 12:00 pm"},
	}
	for _, c := range cases {
		if got := formatWhen(c.start, c.end, c.hasEnd, c.allDy, c.format, loc, at); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatWhenShowsZone(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")
	at := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 2, 14, 18, 0, 0, 0, time.UTC)
	if got := formatWhen(start, time.Time{}, false, false, "short", berlin, at); got != "Sat 14 Feb, 7:00 pm" {
		t.Fatalf("got %q", got)
	}
}

// whenCall runs the registered template func with raw JSON args.
func whenCall(t *testing.T, settingsJSON, args string) string {
	t.Helper()
	fakeSettings(t, settingsJSON, nil)
	out, err := guest.Handle(guest.FuncExport("when"), []byte(args))
	if err != nil {
		t.Fatalf("when returned an error: %v", err)
	}
	return string(out)
}

func TestWhenFunc(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	cases := []struct{ name, settings, args, want string }{
		{"timed", `{}`, `["2026-02-14T19:00","2026-02-14T21:30",false]`, "Sat 14 Feb, 7:00 to 9:30 pm"},
		{"zone and format", `{"time_zone":"Europe/Berlin","date_format":"us"}`, `["2026-02-14T19:00:00Z",null,false]`, "Sat, Feb 14, 8:00 pm"},
		{"all day string flag", `{}`, `["2026-03-07","","true"]`, "Sat 7 Mar (all day)"},
		{"all day multi", `{}`, `["2026-03-07","2026-03-08",true]`, "Sat 7 Mar to Sun 8 Mar (all day)"},
		{"missing end", `{}`, `["2026-02-14T19:00"]`, "Sat 14 Feb, 7:00 pm"},
		{"bad start", `{}`, `["soon","2026-02-14",false]`, ""},
		{"not a string", `{}`, `[42]`, ""},
		{"no args", `{}`, `[]`, ""},
	}
	for _, c := range cases {
		if got := whenCall(t, c.settings, c.args); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWhenFuncBadSettingsStillRenders(t *testing.T) {
	fakeSettings(t, "", json.Unmarshal([]byte("{"), new(any)))
	out, err := guest.Handle(guest.FuncExport("when"), []byte(`["2026-02-14T19:00",null,false]`))
	if err != nil || len(out) == 0 {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

// The template func must read an event the way the lists do: in its own zone.
func TestWhenFuncMatchesTheListItemInTheEventZone(t *testing.T) {
	now = func() time.Time { return time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	e := Event{
		Start: time.Date(2027, 7, 10, 17, 0, 0, 0, time.UTC), End: time.Date(2027, 7, 10, 19, 0, 0, 0, time.UTC),
		HasEnd: true, TimeZone: "Europe/Berlin",
	}
	item := toItem(e, settings{}, now()).Attrs
	if item["time_zone"] != "Europe/Berlin" {
		t.Fatalf("item time_zone = %q", item["time_zone"])
	}
	args, _ := json.Marshal([]any{item["start_at"], item["end_at"], false, item["time_zone"]})
	if got := whenCall(t, `{}`, string(args)); got != item["when"] || got != "Sat 10 Jul, 7:00 to 9:00 pm" {
		t.Errorf("when = %q, item when = %q", got, item["when"])
	}
	// a stored value with an offset, read with the event zone
	if got := whenCall(t, `{}`, `["2027-07-10T17:00:00Z",null,false,"Europe/Berlin"]`); got != "Sat 10 Jul, 7:00 pm" {
		t.Errorf("stored value: %q", got)
	}
}
