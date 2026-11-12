package main

import (
	"bytes"
	"html/template"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk/guest"
)

func linkEvent() Event {
	return Event{
		ID: "3f2a", Slug: "jazz", Name: "Winter jazz & blues",
		Start: time.Date(2027, 2, 14, 19, 0, 0, 0, time.UTC), End: time.Date(2027, 2, 14, 21, 30, 0, 0, time.UTC), HasEnd: true,
	}
}

func parseLink(t *testing.T, raw, host string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Host != host {
		t.Fatalf("url %q: host %q, err %v", raw, u.Host, err)
	}
	if len(raw) >= 2048 {
		t.Fatalf("url is %d bytes", len(raw))
	}
	return u.Query()
}

func TestCalendarLinks(t *testing.T) {
	l := calendarLinks(linkEvent(), time.UTC, "The Blue Room, 12 High St", "Live music")
	g := parseLink(t, l.Google, "calendar.google.com")
	if g.Get("text") != "Winter jazz & blues" || g.Get("dates") != "20270214T190000Z/20270214T213000Z" ||
		g.Get("location") != "The Blue Room, 12 High St" || g.Get("details") != "Live music" || g.Get("action") != "TEMPLATE" {
		t.Errorf("google = %v", g)
	}
	o := parseLink(t, l.Outlook, "outlook.live.com")
	if o.Get("subject") != "Winter jazz & blues" || o.Get("startdt") != "2027-02-14T19:00:00Z" || o.Get("enddt") != "2027-02-14T21:30:00Z" ||
		o.Get("location") != "The Blue Room, 12 High St" || o.Get("rru") != "addevent" || o.Has("allday") {
		t.Errorf("outlook = %v", o)
	}
	y := parseLink(t, l.Yahoo, "calendar.yahoo.com")
	if y.Get("title") != "Winter jazz & blues" || y.Get("st") != "20270214T190000Z" || y.Get("et") != "20270214T213000Z" || y.Get("in_loc") != "The Blue Room, 12 High St" {
		t.Errorf("yahoo = %v", y)
	}
	if l.ICS != "/p/event-calendar/event/jazz.ics" {
		t.Errorf("ics = %q", l.ICS)
	}
}

func TestCalendarLinksZoneAndDefaultEnd(t *testing.T) {
	e := linkEvent()
	e.HasEnd = false
	e.Start = time.Date(2027, 2, 14, 19, 0, 0, 0, mustLoc(t, "Europe/Berlin"))
	g := parseLink(t, calendarLinks(e, time.UTC, "", "").Google, "calendar.google.com")
	if g.Get("dates") != "20270214T180000Z/20270214T190000Z" {
		t.Errorf("dates = %q", g.Get("dates"))
	}
}

func TestCalendarLinksAllDay(t *testing.T) {
	e := Event{Slug: "m", Name: "Market", AllDay: true, Start: time.Date(2027, 3, 7, 0, 0, 0, 0, time.UTC), End: time.Date(2027, 3, 8, 0, 0, 0, 0, time.UTC), HasEnd: true}
	l := calendarLinks(e, time.UTC, "", "")
	if g := parseLink(t, l.Google, "calendar.google.com"); g.Get("dates") != "20270307/20270309" {
		t.Errorf("google dates = %q", g.Get("dates"))
	}
	o := parseLink(t, l.Outlook, "outlook.live.com")
	if o.Get("startdt") != "2027-03-07" || o.Get("enddt") != "2027-03-09" || o.Get("allday") != "true" {
		t.Errorf("outlook = %v", o)
	}
	e.HasEnd = false
	if y := parseLink(t, calendarLinks(e, time.UTC, "", "").Yahoo, "calendar.yahoo.com"); y.Get("st") != "20270307" || y.Get("et") != "20270308" {
		t.Errorf("yahoo = %v", y)
	}
}

func TestCalendarLinksStayUnderTheCap(t *testing.T) {
	for _, unit := range []string{"a", "日"} {
		e := linkEvent()
		e.Name = strings.Repeat(unit, 5000)
		l := calendarLinks(e, time.UTC, strings.Repeat(unit, 5000), strings.Repeat(unit, 5000))
		for _, u := range []string{l.Google, l.Outlook, l.Yahoo} {
			if u == "" || len(u) >= 2048 || !utf8.ValidString(u) {
				t.Errorf("%q: link of %d bytes", unit, len(u))
			}
		}
	}
}

func addlinkCall(t *testing.T, args string) string {
	t.Helper()
	out, err := guest.Handle(guest.FuncExport("addlink"), []byte(args))
	if err != nil {
		t.Fatalf("addlink returned an error: %v", err)
	}
	return string(out)
}

func TestAddlinkFunc(t *testing.T) {
	(&fakeHost{entities: []string{evHybrid, venueDoc, evJazz}}).install(t)
	if got := addlinkCall(t, `["google","h1"]`); !strings.HasPrefix(got, "https://calendar.google.com/") ||
		!strings.Contains(got, "Old+Mill") || !strings.Contains(got, "20270214T190000Z") {
		t.Errorf("google = %q", got)
	}
	if got := addlinkCall(t, `["outlook","h1"]`); !strings.HasPrefix(got, "https://outlook.live.com/") {
		t.Errorf("outlook = %q", got)
	}
	if got := addlinkCall(t, `["yahoo","3f2a"]`); !strings.HasPrefix(got, "https://calendar.yahoo.com/") {
		t.Errorf("yahoo = %q", got)
	}
	if got := addlinkCall(t, `["ics","3f2a"]`); got != "/p/event-calendar/event/jazz.ics" {
		t.Errorf("ics = %q", got)
	}
	for _, bad := range []string{`[]`, `["google"]`, `["google",""]`, `["google","nope"]`, `["webcal","3f2a"]`, `["google",42]`} {
		if got := addlinkCall(t, bad); got != "" {
			t.Errorf("%s = %q", bad, got)
		}
	}
	(&fakeHost{entities: []string{`{"id":"p","schema_slug":"page","slug":"p","name":"P","status":"published","attributes":{"start_at":"2027-01-01"}}`}}).install(t)
	if got := addlinkCall(t, `["google","p"]`); got != "" {
		t.Errorf("page = %q", got)
	}
}

func TestPartialRules(t *testing.T) {
	if _, err := os.Stat("partials/event_details.html"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir("partials")
	if len(entries) != 1 {
		t.Fatalf("partials = %d files", len(entries))
	}
	b, err := os.ReadFile("partials/event_details.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	low := strings.ToLower(src)
	for _, bad := range []string{"<script", "style=", "webcal:", "javascript:", "<form", "<style"} {
		if strings.Contains(low, bad) {
			t.Errorf("contains %q", bad)
		}
	}
	if regexp.MustCompile(`(?i)\son[a-z]+=`).MatchString(src) {
		t.Error("contains an event handler attribute")
	}
	opens := strings.Count(src, "{{if ") + strings.Count(src, "{{with ")
	if ends := strings.Count(src, "{{end}}"); opens != ends {
		t.Errorf("%d openers, %d {{end}}", opens, ends)
	}

	// Execute it with stand-ins for the plugin funcs, with and without a status.
	funcs := template.FuncMap{
		"plugin_event_calendar_when":    func(...any) string { return "Sat 14 Feb" },
		"plugin_event_calendar_addlink": func(p, id string) string { return "/x/" + p },
	}
	tpl, err := template.New("plugin/event_calendar/event_details").Funcs(funcs).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	run := func(attrs map[string]any) string {
		var out bytes.Buffer
		data := map[string]any{"Entity": map[string]any{"ID": "e1", "Attributes": attrs}}
		if err := tpl.Execute(&out, data); err != nil {
			t.Fatalf("execute: %v", err)
		}
		return out.String()
	}
	plain := run(map[string]any{"start_at": "2027-02-14T19:00"})
	if strings.Contains(plain, "hm-ec-status") || strings.Contains(plain, "Get tickets") || !strings.Contains(plain, `href="/x/google"`) {
		t.Errorf("plain = %s", plain)
	}
	full := run(map[string]any{"event_status": "cancelled", "venue_name": "Hall", "venue_address": "1 High St", "price": "Free", "ticket_url": "https://example.org/t"})
	for _, want := range []string{"Cancelled", "Hall, 1 High St", "Free", `href="https://example.org/t"`} {
		if !strings.Contains(full, want) {
			t.Errorf("full lacks %q: %s", want, full)
		}
	}
}
