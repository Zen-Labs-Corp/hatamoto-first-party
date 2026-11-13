package main

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	maxICalEvents   = 500
	maxImportTitle  = 200
	maxImportText   = 4000
	maxImportUID    = 512
	maxImportURLLen = 2048
)

// importEvent is one event read from a calendar feed or an event page, before
// it is matched with the events on the site.
type importEvent struct {
	UID, Title, Description, Venue, Address, City string
	Start, End                                    time.Time      // End is zero when the feed gives none
	AllDay                                        bool           // Start and End are midnights, End is the last day
	Status                                        string         // "", "cancelled" or "tentative"
	Repeat                                        map[string]any // repeat* attributes, nil for a single event
	TicketURL, OnlineURL, Website                 string
	Dropped                                       int // 1 when a series was imported as its first date only
}

// icalProp is one unfolded content line.
type icalProp struct {
	name   string
	params map[string]string
	value  string
}

// parseICalEvents reads the VEVENTs of an iCalendar body. Times without a
// zone are read in loc. Overrides of one date (RECURRENCE-ID) and events
// without UID or start are skipped.
func parseICalEvents(body string, loc *time.Location) ([]importEvent, error) {
	if !strings.Contains(body, "BEGIN:VCALENDAR") {
		return nil, errors.New("not an iCalendar body")
	}
	var (
		out    []importEvent
		cur    []icalProp
		inside bool
		alarm  int
		seen   int
	)
	for _, line := range unfoldLines(body) {
		p, ok := parseICalLine(line)
		if !ok {
			continue
		}
		switch {
		case p.name == "BEGIN" && strings.EqualFold(p.value, "VEVENT"):
			inside, cur, alarm = true, nil, 0
		case p.name == "END" && strings.EqualFold(p.value, "VEVENT"):
			if !inside {
				continue
			}
			inside = false
			if seen++; seen > maxICalEvents {
				return out, nil
			}
			if ev, ok := eventFromProps(cur, loc); ok {
				out = append(out, ev)
			}
		case !inside:
		case p.name == "BEGIN":
			alarm++
		case p.name == "END":
			alarm--
		case alarm == 0:
			cur = append(cur, p)
		}
	}
	return out, nil
}

// unfoldLines joins folded lines (a line that starts with a space or tab
// continues the one before).
func unfoldLines(body string) []string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		out = append(out, l)
	}
	return out
}

// parseICalLine splits NAME;PARAM=v:value (a colon inside quotes does not end
// the name part).
func parseICalLine(line string) (icalProp, bool) {
	quoted, cut := false, -1
	for i := 0; i < len(line) && cut < 0; i++ {
		switch line[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				cut = i
			}
		}
	}
	if cut <= 0 {
		return icalProp{}, false
	}
	head, value := line[:cut], line[cut+1:]
	parts := splitQuoted(head, ';')
	p := icalProp{name: strings.ToUpper(strings.TrimSpace(parts[0])), value: value}
	for _, kv := range parts[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if p.params == nil {
			p.params = map[string]string{}
		}
		p.params[strings.ToUpper(strings.TrimSpace(k))] = strings.Trim(v, `"`)
	}
	return p, p.name != ""
}

func splitQuoted(s string, sep byte) []string {
	var out []string
	quoted, from := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			quoted = !quoted
		case s[i] == sep && !quoted:
			out = append(out, s[from:i])
			from = i + 1
		}
	}
	return append(out, s[from:])
}

// unescapeText reverses escapeText (and accepts \N).
func unescapeText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// cleanText drops control characters (a newline stays when multiline) and
// trims, then caps the result.
func cleanText(s string, n int, multiline bool) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && multiline:
			return r
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	return capBytes(strings.TrimSpace(s), n)
}

// parseICalTime reads a DATE or DATE-TIME value.
func parseICalTime(p icalProp, loc *time.Location) (t time.Time, allDay, ok bool) {
	v := strings.TrimSpace(p.value)
	if strings.EqualFold(p.params["VALUE"], "DATE") || len(v) == 8 {
		t, err := time.ParseInLocation(icsDate, v, loc)
		return t, true, err == nil
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse(icsDateTime, v)
		return t.In(loc), false, err == nil
	}
	zone := loc
	if name := p.params["TZID"]; name != "" {
		if l, err := time.LoadLocation(name); err == nil {
			zone = l
		}
	}
	t, err := time.ParseInLocation("20060102T150405", v, zone)
	return t.In(loc), false, err == nil
}

func eventFromProps(props []icalProp, loc *time.Location) (importEvent, bool) {
	var ev importEvent
	var start, end, rrule icalProp
	var haveStart, haveEnd bool
	var exdates, rdates []icalProp
	for _, p := range props {
		switch p.name {
		case "RECURRENCE-ID":
			return ev, false
		case "UID":
			ev.UID = cleanText(unescapeText(p.value), maxImportUID, false)
		case "SUMMARY":
			ev.Title = cleanText(unescapeText(p.value), maxImportTitle, false)
		case "DESCRIPTION":
			ev.Description = cleanText(unescapeText(p.value), maxImportText, true)
		case "LOCATION":
			lines := strings.Split(strings.TrimSpace(unescapeText(p.value)), "\n")
			ev.Venue = cleanText(lines[0], maxImportTitle, false)
			if len(lines) > 1 {
				ev.Address = cleanText(strings.Join(lines[1:], ", "), maxImportTitle, false)
			}
		case "URL":
			if u := strings.TrimSpace(p.value); isAbsoluteURL(u) && len(u) <= maxImportURLLen {
				ev.Website = u
			}
		case "STATUS":
			switch strings.ToUpper(strings.TrimSpace(p.value)) {
			case "CANCELLED":
				ev.Status = "cancelled"
			case "TENTATIVE":
				ev.Status = "tentative"
			}
		case "DTSTART":
			start, haveStart = p, true
		case "DTEND":
			end, haveEnd = p, true
		case "RRULE":
			rrule = p
		case "EXDATE":
			exdates = append(exdates, p)
		case "RDATE":
			rdates = append(rdates, p)
		}
	}
	if ev.UID == "" || !haveStart {
		return ev, false
	}
	var ok bool
	if ev.Start, ev.AllDay, ok = parseICalTime(start, loc); !ok {
		return ev, false
	}
	if haveEnd {
		if t, allDay, ok := parseICalTime(end, loc); ok {
			if allDay && ev.AllDay {
				t = t.AddDate(0, 0, -1) // DTEND of a date is exclusive
			}
			if !t.Before(ev.Start) {
				ev.End = t
			}
		}
	}
	if ev.AllDay && ev.End.IsZero() {
		ev.End = ev.Start
	}
	if rrule.value != "" {
		ev.Repeat = repeatFromRRule(rrule.value, ev.Start, loc)
		if ev.Repeat == nil {
			ev.Dropped = 1
		} else {
			if l := icalDays(exdates, loc); len(l) > 0 {
				ev.Repeat["repeat_skip_dates"] = l
			}
			if l := icalDays(rdates, loc); len(l) > 0 {
				ev.Repeat["repeat_extra_dates"] = l
			}
		}
	}
	return ev, true
}

// icalDays lists the days (YYYY-MM-DD) of EXDATE or RDATE lines; period values
// are skipped.
func icalDays(props []icalProp, loc *time.Location) []string {
	var out []string
	for _, p := range props {
		for _, v := range strings.Split(p.value, ",") {
			if strings.Contains(v, "/") {
				continue
			}
			one := p
			one.value = v
			if t, _, ok := parseICalTime(one, loc); ok {
				out = append(out, t.Format("2006-01-02"))
			}
		}
	}
	return out
}

var icalWeekdays = map[string]string{"SU": "su", "MO": "mo", "TU": "tu", "WE": "we", "TH": "th", "FR": "fr", "SA": "sa"}

// repeatFromRRule maps the RRULE subset the plugin can express to repeat*
// attributes; nil for a rule it cannot express (the caller imports the first
// date only rather than a wrong series).
func repeatFromRRule(rule string, start time.Time, loc *time.Location) map[string]any {
	kinds := map[string]string{"DAILY": "daily", "WEEKLY": "weekly", "MONTHLY": "monthly", "YEARLY": "yearly"}
	r := map[string]any{}
	var byday string
	for _, part := range strings.Split(rule, ";") {
		k, v, _ := strings.Cut(part, "=")
		switch strings.ToUpper(k) {
		case "FREQ":
			kind, ok := kinds[strings.ToUpper(v)]
			if !ok {
				return nil
			}
			r["repeat"] = kind
		case "INTERVAL":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1000 {
				return nil
			}
			if n > 1 {
				r["repeat_every"] = n
			}
		case "COUNT":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1000 {
				return nil
			}
			r["repeat_count"] = n
		case "UNTIL":
			t, _, ok := parseICalTime(icalProp{value: v}, loc)
			if !ok {
				return nil
			}
			r["repeat_until"] = t.Format("2006-01-02")
		case "BYDAY":
			byday = strings.ToUpper(v)
		case "WKST":
		default:
			return nil
		}
	}
	kind, _ := r["repeat"].(string)
	if kind == "" {
		return nil
	}
	if byday != "" {
		days := strings.Split(byday, ",")
		switch kind {
		case "weekly":
			var codes []string
			for _, d := range days {
				c, ok := icalWeekdays[d]
				if !ok {
					return nil
				}
				codes = append(codes, c)
			}
			r["repeat_weekdays"] = codes
		case "monthly":
			if len(days) != 1 || len(days[0]) < 3 {
				return nil
			}
			n, err := strconv.Atoi(days[0][:len(days[0])-2])
			code, ok := icalWeekdays[days[0][len(days[0])-2:]]
			if err != nil || !ok || n != -1 && (n < 1 || n > 5) || weekdayCodes[code] != start.Weekday() {
				return nil
			}
			r["repeat_by"] = "nth_weekday"
			r["repeat_nth"] = n
		default:
			return nil
		}
	}
	return r
}
