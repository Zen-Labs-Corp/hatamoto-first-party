package main

import (
	"encoding/json"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	maxLDBlocks = 20
	maxLDDepth  = 6
)

var (
	ldScriptRe = regexp.MustCompile(`(?is)<script[^>]+type\s*=\s*["']?application/ld\+json["']?[^>]*>(.*?)</script>`)
	htmlTagRe  = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlBreak  = regexp.MustCompile(`(?i)<(br|/p|/div|/li)[^>]*>`)
)

// parseJSONLDEvents reads the Event objects of every JSON-LD block of an HTML
// page: a single object, an array or an @graph. Blocks that do not parse are
// skipped; a page without events gives none.
func parseJSONLDEvents(body string, loc *time.Location) []importEvent {
	var out []importEvent
	for i, m := range ldScriptRe.FindAllStringSubmatch(body, -1) {
		if i >= maxLDBlocks {
			break
		}
		var doc any
		if json.Unmarshal([]byte(strings.TrimSpace(html.UnescapeString(m[1]))), &doc) != nil {
			continue
		}
		var found []map[string]any
		collectLDEvents(doc, &found, 0)
		for _, obj := range found {
			if ev, ok := eventFromLD(obj, loc); ok {
				out = append(out, ev)
			}
		}
	}
	return out
}

func collectLDEvents(v any, out *[]map[string]any, depth int) {
	if depth > maxLDDepth {
		return
	}
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			collectLDEvents(item, out, depth+1)
		}
	case map[string]any:
		if ldIsEvent(x["@type"]) {
			*out = append(*out, x)
			return
		}
		if g, ok := x["@graph"]; ok {
			collectLDEvents(g, out, depth+1)
		}
	}
}

// ldIsEvent is true for Event and its subtypes (MusicEvent, ...), written bare
// or with a vocabulary prefix.
func ldIsEvent(t any) bool {
	var types []any
	switch x := t.(type) {
	case string:
		types = []any{x}
	case []any:
		types = x
	}
	for _, v := range types {
		s, _ := v.(string)
		if i := strings.LastIndexAny(s, "/:"); i >= 0 {
			s = s[i+1:]
		}
		if strings.HasSuffix(s, "Event") {
			return true
		}
	}
	return false
}

func ldString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// ldObjects lists the objects of a value that is an object or a list.
func ldObjects(v any) []map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return []map[string]any{x}
	case []any:
		var out []map[string]any
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func ldURL(v any) string {
	if u := ldString(v); isAbsoluteURL(u) && len(u) <= maxImportURLLen {
		return u
	}
	return ""
}

func ldTime(s string, loc *time.Location) (t time.Time, allDay, ok bool) {
	s = strings.TrimSpace(s)
	t, ok = parseTime(s, loc)
	return t.In(loc), len(s) == len("2006-01-02"), ok
}

func eventFromLD(o map[string]any, loc *time.Location) (importEvent, bool) {
	var ev importEvent
	ev.Title = cleanText(html.UnescapeString(htmlTagRe.ReplaceAllString(ldString(o["name"]), "")), maxImportTitle, false)
	rawStart := ldString(o["startDate"])
	var ok bool
	if ev.Start, ev.AllDay, ok = ldTime(rawStart, loc); !ok || ev.Title == "" {
		return ev, false
	}
	if t, allDay, ok := ldTime(ldString(o["endDate"]), loc); ok && !t.Before(ev.Start) && allDay == ev.AllDay {
		ev.End = t
	}
	if ev.AllDay && ev.End.IsZero() {
		ev.End = ev.Start
	}
	ev.Description = cleanText(html.UnescapeString(htmlTagRe.ReplaceAllString(htmlBreak.ReplaceAllString(ldString(o["description"]), "\n"), "")), maxImportText, true)
	ev.Website = ldURL(o["url"])
	ev.UID = cleanText(ldString(o["@id"]), maxImportUID, false)
	if ev.UID == "" {
		ev.UID = ldURL(o["url"])
	}
	if ev.UID == "" {
		ev.UID = ev.Title + "|" + rawStart
	}
	if strings.Contains(ldString(o["eventStatus"]), "EventCancelled") {
		ev.Status = "cancelled"
	}
	for _, offer := range ldObjects(o["offers"]) {
		if u := ldURL(offer["url"]); u != "" {
			ev.TicketURL = u
			break
		}
	}
	online := strings.Contains(ldString(o["eventAttendanceMode"]), "Online")
	var locations []map[string]any
	if s := ldString(o["location"]); s != "" {
		ev.Venue = cleanText(s, maxImportTitle, false)
	}
	locations = ldObjects(o["location"])
	for _, l := range locations {
		if online || ldString(l["@type"]) == "VirtualLocation" {
			if ev.OnlineURL == "" {
				ev.OnlineURL = ldURL(l["url"])
			}
			continue
		}
		if ev.Venue != "" {
			continue
		}
		ev.Venue = cleanText(ldString(l["name"]), maxImportTitle, false)
		switch a := l["address"].(type) {
		case string:
			ev.Address = cleanText(a, maxImportTitle, false)
		case map[string]any:
			ev.Address = cleanText(ldString(a["streetAddress"]), maxImportTitle, false)
			ev.City = cleanText(ldString(a["addressLocality"]), maxImportTitle, false)
		}
	}
	return ev, true
}

// normaliseFeedURL turns webcal:// into https:// and accepts only http(s)
// addresses with a host, no user info and no whitespace.
func normaliseFeedURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "webcals://"):
		raw = "https://" + raw[len("webcals://"):]
	case strings.HasPrefix(lower, "webcal://"):
		raw = "https://" + raw[len("webcal://"):]
	}
	if len(raw) > maxImportURLLen || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errors.New("not a usable address")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("not a usable address")
	}
	return raw, nil
}

// hostOf is the lower-case host name of an address, empty when it has none.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
