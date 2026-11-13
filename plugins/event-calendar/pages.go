package main

import (
	"encoding/json"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// The public pages of a series, a venue and an organizer: what it is, and the
// upcoming dates of its events.

const (
	pageSeries    = "series"
	pageVenue     = "venue"
	pageOrganizer = "organizer"

	// maxPageDates is how many upcoming dates a page lists.
	maxPageDates = 100
)

var pageSchemas = map[string]string{
	pageSeries:    "calendar-series",
	pageVenue:     "calendar-venue",
	pageOrganizer: "calendar-organizer",
}

var langRe = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z0-9]{2,8})*$`)

// servePage answers a page by the slug of its entity. Only a published entity
// has a page; a host failure is a plain 503, never text of the error.
func servePage(kind, slug string) sdk.HTTPResponse {
	schema, ok := pageSchemas[kind]
	if !ok || !fileSlugRe.MatchString(slug) {
		return notFound()
	}
	var raw entityDTO
	err := callHost(sdk.FnEntitiesGet, map[string]any{"slug": slug, "parent_id": nil, "schema_slug": schema}, &raw)
	switch {
	case isNotFound(err):
		return notFound()
	case err != nil:
		return plain(http.StatusServiceUnavailable, "unavailable")
	case raw.ID == "" || raw.Status != "published":
		return notFound()
	}
	var attrs map[string]any
	if len(raw.Attributes) > 0 {
		_ = json.Unmarshal(raw.Attributes, &attrs)
	}
	s := loadSettings()
	at := now()
	var b strings.Builder
	b.WriteString("<h1>" + esc(raw.Name) + "</h1>\n")
	switch kind {
	case pageVenue:
		writeVenue(&b, attrs, s)
	case pageOrganizer:
		writeOrganizer(&b, attrs)
	case pageSeries:
		writeSeries(&b, attrs)
	}
	writeDates(&b, pageDates(kind, raw.ID, s, at), kind == pageSeries, s, at)
	return pageResponse(raw.Name, raw.Locale, b.String())
}

func esc(s string) string { return html.EscapeString(s) }

// pageResponse wraps the body in a page. The page has no script, style or
// outside resource, so any theme-free browser shows it plainly.
func pageResponse(title, locale, body string) sdk.HTTPResponse {
	lang := ""
	if langRe.MatchString(locale) {
		lang = ` lang="` + esc(locale) + `"`
	}
	doc := "<!doctype html>\n<html" + lang + ">\n<head>\n<meta charset=\"utf-8\">\n" +
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n" +
		"<title>" + esc(title) + "</title>\n</head>\n<body>\n<main>\n" + body + "</main>\n</body>\n</html>\n"
	return sdk.HTTPResponse{
		Status: http.StatusOK,
		Headers: map[string]string{
			"Content-Type":  "text/html; charset=utf-8",
			"Cache-Control": "public, max-age=300",
		},
		Body: doc,
	}
}

// safeHref reports a link that may be put in an href: an http(s) URL or a
// path on this site.
func safeHref(u string) bool {
	return isAbsoluteURL(u) || strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.ContainsAny(u, " \r\n\\")
}

// link is the anchor of a URL, or the plain text when the URL is not safe.
func link(href, text string) string {
	if !safeHref(href) {
		return esc(text)
	}
	return `<a href="` + esc(href) + `">` + esc(text) + `</a>`
}

// paragraphs renders plain text as one <p> per line of text.
func paragraphs(b *strings.Builder, text string) {
	for _, p := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			b.WriteString("<p>" + esc(p) + "</p>\n")
		}
	}
}

func writeVenue(b *strings.Builder, a map[string]any, s settings) {
	place := joinNonEmpty(attrString(a, "city"), attrString(a, "region"), attrString(a, "postcode"))
	if lines := []string{attrString(a, "address"), place, attrString(a, "country")}; joinNonEmpty(lines...) != "" {
		b.WriteString("<address>")
		var out []string
		for _, l := range lines {
			if l != "" {
				out = append(out, esc(l))
			}
		}
		b.WriteString(strings.Join(out, "<br>") + "</address>\n")
	}
	if phone := attrString(a, "phone"); phone != "" {
		b.WriteString("<p>" + esc(phone) + "</p>\n")
	}
	if site := attrString(a, "website"); isAbsoluteURL(site) {
		b.WriteString("<p>" + link(site, site) + "</p>\n")
	}
	if lat, lon := attrFloat(a, "latitude"), attrFloat(a, "longitude"); s.ShowMap && lat != nil && lon != nil {
		la, lo := strconv.FormatFloat(*lat, 'f', 5, 64), strconv.FormatFloat(*lon, 'f', 5, 64)
		b.WriteString("<p>" + link("https://www.openstreetmap.org/?mlat="+la+"&mlon="+lo+"#map=16/"+la+"/"+lo, "Map") + "</p>\n")
	}
}

func writeOrganizer(b *strings.Builder, a map[string]any) {
	if email := attrString(a, "email"); email != "" {
		b.WriteString("<p>" + esc(email) + "</p>\n")
	}
	if phone := attrString(a, "phone"); phone != "" {
		b.WriteString("<p>" + esc(phone) + "</p>\n")
	}
	if site := attrString(a, "website"); isAbsoluteURL(site) {
		b.WriteString("<p>" + link(site, site) + "</p>\n")
	}
}

func writeSeries(b *strings.Builder, a map[string]any) {
	if img := attrString(a, "image"); safeHref(img) {
		b.WriteString(`<p><img src="` + esc(img) + `" alt=""></p>` + "\n")
	}
	paragraphs(b, attrString(a, "description"))
}

// pageRows are the dates a page lists; ok is false while the index cannot
// answer (no db, or no complete reconcile yet).
type pageRows struct {
	rows []apiRow
	ok   bool
}

// pageDates reads the upcoming dates of the events that belong to the page.
func pageDates(kind, id string, s settings, at time.Time) pageRows {
	_, to := windowFor(at)
	q := rangeQuery{From: at, To: to, Limit: maxPageDates}
	switch kind {
	case pageSeries:
		q.SeriesID = id
	case pageVenue:
		q.VenueID = id
	case pageOrganizer:
		q.OrganizerID = id
	}
	rows, err := queryRange(q)
	if err != nil {
		return pageRows{}
	}
	var out []apiRow
	for _, r := range rows {
		if s.accepts(r.SchemaSlug) {
			out = append(out, r)
		}
	}
	return pageRows{rows: out, ok: true}
}

// writeDates lists the dates, flat by start, or (a series) under the event
// each belongs to.
func writeDates(b *strings.Builder, p pageRows, grouped bool, s settings, at time.Time) {
	b.WriteString("<h2>Upcoming</h2>\n")
	switch {
	case !p.ok:
		b.WriteString("<p>Dates are not available yet.</p>\n")
		return
	case len(p.rows) == 0:
		b.WriteString("<p>No upcoming events.</p>\n")
		return
	}
	when := func(r apiRow) string {
		e := r.event(s.location())
		return formatWhen(e.Start, e.End, e.HasEnd, e.AllDay, s.DateFormat, dateLocation(e, s.location()), at)
	}
	if !grouped {
		b.WriteString("<ul>\n")
		for _, r := range p.rows {
			b.WriteString("<li>" + esc(when(r)) + ": " + link(r.URL, r.Name) + "</li>\n")
		}
		b.WriteString("</ul>\n")
		return
	}
	var order []string
	byEvent := map[string][]apiRow{}
	for _, r := range p.rows {
		if _, seen := byEvent[r.ID]; !seen {
			order = append(order, r.ID)
		}
		byEvent[r.ID] = append(byEvent[r.ID], r)
	}
	for _, id := range order {
		rows := byEvent[id]
		b.WriteString("<h3>" + link(rows[0].URL, rows[0].Name) + "</h3>\n<ul>\n")
		for _, r := range rows {
			b.WriteString("<li>" + esc(when(r)) + "</li>\n")
		}
		b.WriteString("</ul>\n")
	}
}
