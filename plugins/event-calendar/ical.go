package main

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	icsProdID    = "-//Hatamoto//event-calendar 0.1.0//EN"
	icsUIDDomain = "event-calendar.hatamoto"
	icsMaxOctets = 75
	icsDateTime  = "20060102T150405Z"
	icsDate      = "20060102"
)

// escapeText escapes an iCalendar TEXT value: backslash, semicolon, comma and
// newlines; carriage returns are dropped.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	r := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, "\n", `\n`)
	return r.Replace(s)
}

// fold splits a content line into physical lines of at most 75 octets joined
// by CRLF and a space, never inside a UTF-8 sequence.
func fold(line string) string {
	if len(line) <= icsMaxOctets {
		return line
	}
	var b strings.Builder
	limit := icsMaxOctets
	size := 0
	for _, r := range line {
		n := utf8.RuneLen(r)
		if size+n > limit {
			b.WriteString("\r\n ")
			size = 0
			limit = icsMaxOctets - 1
		}
		b.WriteRune(r)
		size += n
	}
	return b.String()
}

// isAbsoluteURL reports an http or https URL with a host.
func isAbsoluteURL(s string) bool {
	rest, ok := strings.CutPrefix(s, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(s, "http://")
	}
	return ok && rest != "" && !strings.ContainsAny(s, " \r\n") && rest[0] != '/'
}

// writeCalendar renders events as one VCALENDAR with CRLF line ends. stamp is
// the DTSTAMP of events that carry no update time.
func writeCalendar(evs []Event, stamp time.Time, loc *time.Location) string {
	var b strings.Builder
	line := func(s string) {
		b.WriteString(fold(s))
		b.WriteString("\r\n")
	}
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:" + icsProdID)
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	for _, e := range evs {
		writeEvent(line, e, stamp, loc)
	}
	line("END:VCALENDAR")
	return b.String()
}

func writeEvent(line func(string), e Event, stamp time.Time, loc *time.Location) {
	uid := e.ID
	if uid == "" {
		uid = e.Slug
	}
	ds := e.Updated
	if ds.IsZero() {
		ds = stamp
	}
	line("BEGIN:VEVENT")
	line("UID:" + escapeText(uid) + "@" + icsUIDDomain)
	line("DTSTAMP:" + ds.UTC().Format(icsDateTime))
	if e.AllDay {
		last := e.Start
		if e.HasEnd {
			last = e.End
		}
		y, m, d := last.In(loc).Date()
		line("DTSTART;VALUE=DATE:" + e.Start.In(loc).Format(icsDate))
		line("DTEND;VALUE=DATE:" + time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC).Format(icsDate))
	} else {
		line("DTSTART:" + e.Start.UTC().Format(icsDateTime))
		if e.HasEnd {
			line("DTEND:" + e.End.UTC().Format(icsDateTime))
		}
	}
	line("SUMMARY:" + escapeText(e.Name))
	var where []string
	for _, p := range []string{e.Venue, e.Address} {
		if p = strings.TrimSpace(p); p != "" {
			where = append(where, p)
		}
	}
	if len(where) > 0 {
		line("LOCATION:" + escapeText(strings.Join(where, ", ")))
	}
	if isAbsoluteURL(e.URL) {
		line("URL:" + e.URL)
	}
	var desc []string
	if e.Description != "" {
		desc = append(desc, e.Description)
	}
	if e.TicketURL != "" {
		desc = append(desc, "Tickets: "+e.TicketURL)
	}
	if len(desc) > 0 {
		line("DESCRIPTION:" + escapeText(strings.Join(desc, "\n")))
	}
	line("END:VEVENT")
}
