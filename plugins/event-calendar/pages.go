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

	// maxSeriesEvents and pageFillBudget bound the reads a series page makes
	// to describe the rule of each of its events.
	maxSeriesEvents = 10
	pageFillBudget  = time.Second

	// pagePreview is how many dates a page list shows before "More".
	pagePreview = 10
)

// pageCSS is the pages' only style: static, no url() and no import, so the
// page loads nothing else (plugin routes allow inline style, not scripts).
const pageCSS = `body{margin:0;font-family:system-ui,sans-serif;line-height:1.5}
.hm-ec-page{max-width:48rem;margin:0 auto;padding:var(--hm-space-4,1rem)}
.hm-ec-page .card{margin:var(--hm-space-4,1rem) 0;padding:var(--hm-space-4,1rem);border:1px solid var(--hm-color-border,#d8dce3);border-radius:var(--hm-radius,.5rem)}
.hm-ec-page .kind{margin:0;font-size:.75rem;font-weight:600;text-transform:uppercase}
.hm-ec-page h1{margin:0 0 var(--hm-space-3,.75rem);font-size:1.5rem}
.hm-ec-page h2{margin:0 0 var(--hm-space-3,.75rem);font-size:1.125rem}
.hm-ec-page .btn{display:inline-block;padding:var(--hm-space-2,.5rem) var(--hm-space-4,1rem);border-radius:var(--hm-radius,.5rem);background:var(--hm-color-accent,#1a56db);color:var(--hm-color-on-accent,#fff);font-weight:600;text-decoration:none}
.hm-ec-page .badge{display:inline-block;padding:0 var(--hm-space-2,.5rem);border-radius:var(--hm-radius,.5rem);background:var(--hm-color-accent,#1a56db);color:var(--hm-color-on-accent,#fff);font-size:.75rem;font-weight:600}
.hm-ec-page .skipped{text-decoration:line-through}
.hm-ec-page ul.dates{list-style:none;margin:0;padding:0}
.hm-ec-page ul.dates li{padding:var(--hm-space-1,.25rem) 0}
.hm-ec-page summary{cursor:pointer;font-weight:600}
@media (max-width:600px){.hm-ec-page{padding:var(--hm-space-3,.75rem)}.hm-ec-page h1{font-size:1.25rem}}`

var pageKinds = map[string]string{
	pageSeries:    "Series",
	pageVenue:     "Venue",
	pageOrganizer: "Organizer",
}

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
	b.WriteString(`<section class="card">` + "\n<p class=\"kind\">" + pageKinds[kind] + "</p>\n<h1>" + esc(raw.Name) + "</h1>\n")
	switch kind {
	case pageVenue:
		writeVenue(&b, attrs, s)
	case pageOrganizer:
		writeOrganizer(&b, attrs)
	case pageSeries:
		writeSeries(&b, attrs)
	}
	b.WriteString("</section>\n")
	writeDates(&b, pageDates(kind, raw.ID, s, at), kind, s, at)
	return pageResponse(raw.Name, raw.Locale, b.String())
}

func esc(s string) string { return html.EscapeString(s) }

// pageResponse wraps the body in a page. The page has no script and no
// outside resource, only the one static style, so any theme-free browser
// shows it plainly.
func pageResponse(title, locale, body string) sdk.HTTPResponse {
	lang := ""
	if langRe.MatchString(locale) {
		lang = ` lang="` + esc(locale) + `"`
	}
	doc := "<!doctype html>\n<html" + lang + ">\n<head>\n<meta charset=\"utf-8\">\n" +
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n" +
		"<title>" + esc(title) + "</title>\n<style>" + pageCSS + "</style>\n</head>\n<body>\n<main class=\"hm-ec-page\">\n" + body + "</main>\n</body>\n</html>\n"
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

// linkClass is link with a class on the anchor.
func linkClass(href, text, class string) string {
	if !safeHref(href) {
		return esc(text)
	}
	return `<a class="` + esc(class) + `" href="` + esc(href) + `">` + esc(text) + `</a>`
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
		b.WriteString("<p>" + linkClass("https://www.openstreetmap.org/?mlat="+la+"&mlon="+lo+"#map=16/"+la+"/"+lo, "Map", "btn") + "</p>\n")
	}
}

func writeOrganizer(b *strings.Builder, a map[string]any) {
	if attrBool(a, "show_contact") {
		if email := attrString(a, "email"); email != "" {
			b.WriteString("<p>" + esc(email) + "</p>\n")
		}
		if phone := attrString(a, "phone"); phone != "" {
			b.WriteString("<p>" + esc(phone) + "</p>\n")
		}
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

// writeDatesList writes list items as one list; those past pagePreview go
// behind a "More" disclosure (no script).
func writeDatesList(b *strings.Builder, items []string) {
	head, rest := items, []string(nil)
	if len(items) > pagePreview {
		head, rest = items[:pagePreview], items[pagePreview:]
	}
	b.WriteString("<ul class=\"dates\">\n" + strings.Join(head, "") + "</ul>\n")
	if len(rest) > 0 {
		b.WriteString("<details><summary>More (" + strconv.Itoa(len(rest)) + ")</summary>\n<ul class=\"dates\">\n" +
			strings.Join(rest, "") + "</ul>\n</details>\n")
	}
}

// writeDates lists the dates, flat by start, or (a series) under the event
// each belongs to.
func writeDates(b *strings.Builder, p pageRows, kind string, s settings, at time.Time) {
	heading := "Upcoming"
	if kind == pageVenue {
		heading = "Upcoming here"
	}
	b.WriteString("<section class=\"card\">\n<h2>" + heading + "</h2>\n")
	defer b.WriteString("</section>\n")
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
	if kind != pageSeries {
		var items []string
		for _, r := range p.rows {
			items = append(items, "<li>"+esc(when(r))+": "+link(r.URL, r.Name)+"</li>\n")
		}
		writeDatesList(b, items)
		return
	}
	writeSeriesDates(b, p, s, at, when)
}

// writeSeriesDates lists each event of a series with its rule: how many dates
// are ahead, how many changed, and every date with skipped and changed ones
// marked. An event that cannot be read in time is listed from the index.
func writeSeriesDates(b *strings.Builder, p pageRows, s settings, at time.Time, when func(apiRow) string) {
	var order []string
	byEvent := map[string][]apiRow{}
	for _, r := range p.rows {
		if _, seen := byEvent[r.ID]; !seen {
			order = append(order, r.ID)
		}
		byEvent[r.ID] = append(byEvent[r.ID], r)
	}
	deadline := clock().Add(pageFillBudget)
	for i, id := range order {
		rows := byEvent[id]
		b.WriteString("<h3>" + link(rows[0].URL, rows[0].Name) + "</h3>\n")
		if i < maxSeriesEvents && clock().Before(deadline) && writeRule(b, id, s, at) {
			continue
		}
		var items []string
		for _, r := range rows {
			items = append(items, "<li>"+esc(when(r))+"</li>\n")
		}
		writeDatesList(b, items)
	}
}

// writeRule writes the rule summary and the dates of one event, read from the
// event itself. It writes nothing and reports false when the event cannot be
// read.
func writeRule(b *strings.Builder, id string, s settings, at time.Time) bool {
	var raw entityDTO
	if callHost(sdk.FnEntitiesGet, map[string]any{"id": id}, &raw) != nil || raw.Status != "published" || !s.accepts(raw.SchemaSlug) {
		return false
	}
	e, ok := eventFromEntity(raw, s)
	if !ok {
		return false
	}
	loc := eventLocation(e, s)
	_, to := windowFor(at)
	var items []string
	dates, changed := 0, 0
	for _, o := range expandAll(e, loc, at, to, maxPageDates) {
		text := esc(formatWhen(o.Start, o.End, e.HasEnd, e.AllDay, s.DateFormat, loc, at))
		switch o.State {
		case stateSkipped:
			items = append(items, `<li class="skipped">`+text+" (skipped)</li>\n")
			continue
		case stateChanged:
			changed++
			text += ` <span class="badge">changed</span>`
		}
		dates++
		items = append(items, "<li>"+text+"</li>\n")
	}
	b.WriteString(`<p class="summary">` + esc(ruleText(e, loc)) + ", " + strconv.Itoa(dates) + " dates, " + strconv.Itoa(changed) + " changed</p>\n")
	writeDatesList(b, items)
	return true
}
