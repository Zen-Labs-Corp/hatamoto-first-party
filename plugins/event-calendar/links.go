package main

import (
	"net/url"
	"time"
)

const (
	// maxLinkURL is the builder's per-string cap: an item with a longer URL is
	// dropped whole, so every link is shrunk to fit.
	maxLinkURL      = 2048
	maxLinkTitle    = 200
	maxLinkDetails  = 300
	maxLinkLocation = 200

	linkDateTime = "20060102T150405Z"
	linkDate     = "20060102"
)

// calLinks are the add-to-calendar links of one event.
type calLinks struct{ Google, Outlook, Yahoo, ICS string }

// calendarLinks builds the links for e. Times are sent in UTC; an all-day event
// sends dates (the end day is exclusive). Without an end an event lasts an
// hour. Long text is cut so each URL stays under the builder string cap.
func calendarLinks(e Event, loc *time.Location, location, details string) calLinks {
	title := capBytes(e.Name, maxLinkTitle)
	details = capBytes(details, maxLinkDetails)
	location = capBytes(location, maxLinkLocation)

	start, end := e.Start.UTC(), e.Start.UTC().Add(time.Hour)
	if e.HasEnd {
		end = e.End.UTC()
	}
	var gs, ge, os, oe, ys, ye string
	if e.AllDay {
		first := e.Start.In(loc)
		last := first
		if e.HasEnd {
			last = e.End.In(loc)
		}
		y, m, d := first.Date()
		ly, lm, ld := last.Date()
		s := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		n := time.Date(ly, lm, ld+1, 0, 0, 0, 0, time.UTC)
		gs, ge = s.Format(linkDate), n.Format(linkDate)
		os, oe = s.Format("2006-01-02"), n.Format("2006-01-02")
		ys, ye = gs, ge
	} else {
		gs, ge = start.Format(linkDateTime), end.Format(linkDateTime)
		os, oe = start.Format("2006-01-02T15:04:05Z"), end.Format("2006-01-02T15:04:05Z")
		ys, ye = gs, ge
	}

	l := calLinks{}
	if e.Slug != "" {
		l.ICS = icsPathPrefix + e.Slug + ".ics"
	}
	l.Google = fitURL(title, details, location, func(t, d, w string) string {
		return "https://calendar.google.com/calendar/render?" + url.Values{
			"action": {"TEMPLATE"}, "text": {t}, "dates": {gs + "/" + ge}, "details": {d}, "location": {w},
		}.Encode()
	})
	l.Outlook = fitURL(title, details, location, func(t, d, w string) string {
		v := url.Values{
			"path": {"/calendar/action/compose"}, "rru": {"addevent"}, "subject": {t},
			"startdt": {os}, "enddt": {oe}, "body": {d}, "location": {w},
		}
		if e.AllDay {
			v.Set("allday", "true")
		}
		return "https://outlook.live.com/calendar/0/deeplink/compose?" + v.Encode()
	})
	l.Yahoo = fitURL(title, details, location, func(t, d, w string) string {
		return "https://calendar.yahoo.com/?" + url.Values{
			"v": {"60"}, "title": {t}, "st": {ys}, "et": {ye}, "desc": {d}, "in_loc": {w},
		}.Encode()
	})
	return l
}

// fitURL builds a link and, while it is over the cap (multi-byte text grows
// three-fold once escaped), halves the details, then the location, then the
// title; "" if even that does not fit.
func fitURL(title, details, location string, build func(t, d, w string) string) string {
	for i := 0; i < 12; i++ {
		if u := build(title, details, location); len(u) < maxLinkURL {
			return u
		}
		switch {
		case details != "":
			details = capBytes(details, len(details)/2)
		case location != "":
			location = capBytes(location, len(location)/2)
		default:
			title = capBytes(title, len(title)/2)
		}
	}
	return ""
}
