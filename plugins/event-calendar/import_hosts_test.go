package main

import (
	"slices"
	"testing"
)

func TestScheduledHostsMatchManifest(t *testing.T) {
	m, _ := loadManifest(t)
	var want []string
	for _, h := range m.AllowedHosts {
		if h != "*" && h != "nominatim.openstreetmap.org" {
			want = append(want, h)
		}
	}
	if !slices.Equal(scheduledHosts, want) {
		t.Errorf("scheduledHosts = %v, want %v", scheduledHosts, want)
	}
}

func TestOutlookICSShapes(t *testing.T) {
	for _, u := range []string{
		"https://outlook.office365.com/owa/calendar/0123abcd@example.com/4567ef/calendar.ics",
		"https://outlook.live.com/owa/calendar/00000000-0000-0000-0000-000000000000/11111111-2222-3333-4444-555555555555/cid-0123456789ABCDEF/calendar.ics",
		"webcal://outlook.office365.com/owa/calendar/0123abcd@example.com/4567ef/calendar.ics",
	} {
		if !hostListed(u) {
			t.Errorf("%s is not listed", u)
		}
	}
	for _, u := range []string{"https://calendar.example.org/x.ics", "", "ftp://calendar.google.com/x"} {
		if hostListed(u) {
			t.Errorf("%q is listed", u)
		}
	}
}
