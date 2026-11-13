package main

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// The event index lives in the plugin's own tables (migrations/001_init.sql).
// Every statement is one call: the host's SQL guard takes a single statement.

const maxIndexRows = 500

// dbExec runs one statement that changes rows.
func dbExec(sql string, args ...any) error {
	return callHost(sdk.FnDBExec, map[string]any{"sql": sql, "args": args}, nil)
}

// dbJSON runs a query that returns a single jsonb (or json) cell and decodes
// it into out. Selecting one document keeps column typing out of the guest.
func dbJSON(sql string, out any, args ...any) error {
	var res struct {
		Rows [][]json.RawMessage `json:"rows"`
	}
	if err := callHost(sdk.FnDBQuery, map[string]any{"sql": sql, "args": args}, &res); err != nil {
		return err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return errors.New("query returned no rows")
	}
	cell := res.Rows[0][0]
	// jsonb may arrive as a JSON value or as a JSON string holding JSON.
	var s string
	if json.Unmarshal(cell, &s) == nil && len(s) > 0 && (s[0] == '[' || s[0] == '{') {
		cell = json.RawMessage(s)
	}
	return json.Unmarshal(cell, out)
}

// indexRow mirrors the events table.
type indexRow struct {
	EntityID, SchemaSlug, Slug, Locale, Name string
	StartsAt, EndsAt                         time.Time
	AllDay                                   bool
	TimeZone, EventStatus, AttendanceMode    string
	VenueID, VenueName, VenueCity            string
	URL, VenueSlug, VenueAddress             string
	VenueLat, VenueLon                       *float64
	CategoryIDs, CategoryNames               []string
	CategorySlugs                            []string
	SeriesID, Price                          string
	PriceAmount                              *float64
	Featured                                 bool
	Image                                    string
	SourceKind, SourceUID, SourceURL         string
}

// rowFromEvent is the index row of an event. loc is the zone an all-day event
// ends its last day in. Venue name and city come from the resolved venue, else
// from the event's own text fields. The venue address is the line a map looks
// up (addressOf), so it keys geocode_cache.
func rowFromEvent(e Event, v *Venue, cats categoryRefs, loc *time.Location) indexRow {
	name, city := e.Venue, e.VenueCity
	var venue Venue
	if v != nil {
		venue = *v
		name, city = v.Name, v.City
	}
	return indexRow{
		EntityID: e.ID, SchemaSlug: e.SchemaSlug, Slug: e.Slug, Locale: e.Locale, Name: e.Name,
		StartsAt: e.Start.UTC(), EndsAt: e.endsAt(loc).UTC(), AllDay: e.AllDay,
		TimeZone: e.TimeZone, EventStatus: e.EventStatus, AttendanceMode: e.AttendanceMode,
		VenueID: e.VenueID, VenueName: name, VenueCity: city,
		URL: e.URL, VenueSlug: venue.Slug, VenueAddress: addressOf(venue, e), VenueLat: venue.Lat, VenueLon: venue.Lon,
		CategoryIDs: nonNil(e.CategoryIDs), CategoryNames: nonNil(cats.Names), CategorySlugs: nonNil(cats.Slugs),
		SeriesID: e.SeriesID, Price: e.Price, PriceAmount: e.PriceAmount,
		Featured: e.Featured, Image: e.Image,
		SourceKind: e.SourceKind, SourceUID: e.SourceUID, SourceURL: e.SourceURL,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func utcText(t time.Time) string { return t.UTC().Format(time.RFC3339) }

const upsertEventSQL = `INSERT INTO events (entity_id, schema_slug, slug, locale, name, starts_at, ends_at, all_day, time_zone,
 event_status, attendance_mode, venue_id, venue_name, venue_city, category_ids, category_names, series_id,
 price, price_amount, featured, image, source_kind, source_uid, source_url,
 url, venue_slug, venue_address, venue_lat, venue_lon, category_slugs)
SELECT $1, $2, $3, $4, $5, $6::timestamptz, $7::timestamptz, $8::boolean, $9, $10, $11, $12, $13, $14, $15::jsonb, $16::jsonb, $17,
 $18, $19::double precision, $20::boolean, $21, $22, $23, $24,
 $26, $27, $28, $29::double precision, $30::double precision, $31::jsonb
WHERE $25::timestamptz IS NULL OR NOT EXISTS (SELECT 1 FROM deleted_events WHERE entity_id = $1 AND deleted_at >= $25::timestamptz)
ON CONFLICT (entity_id) DO UPDATE SET schema_slug = EXCLUDED.schema_slug, slug = EXCLUDED.slug, locale = EXCLUDED.locale,
 name = EXCLUDED.name, starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, all_day = EXCLUDED.all_day,
 time_zone = EXCLUDED.time_zone, event_status = EXCLUDED.event_status, attendance_mode = EXCLUDED.attendance_mode,
 venue_id = EXCLUDED.venue_id, venue_name = EXCLUDED.venue_name, venue_city = EXCLUDED.venue_city,
 category_ids = EXCLUDED.category_ids, category_names = EXCLUDED.category_names, series_id = EXCLUDED.series_id,
 price = EXCLUDED.price, price_amount = EXCLUDED.price_amount, featured = EXCLUDED.featured, image = EXCLUDED.image,
 source_kind = EXCLUDED.source_kind, source_uid = EXCLUDED.source_uid, source_url = EXCLUDED.source_url,
 url = EXCLUDED.url, venue_slug = EXCLUDED.venue_slug, venue_address = EXCLUDED.venue_address,
 venue_lat = EXCLUDED.venue_lat, venue_lon = EXCLUDED.venue_lon, category_slugs = EXCLUDED.category_slugs, updated_at = now()`

// The delete skips the row the insert writes, so the two halves never touch
// one key. One occurrence per event for now; recurrence will write many.
const upsertOccurrenceSQL = `WITH d AS (DELETE FROM occurrences WHERE event_id = $1 AND starts_at <> $2::timestamptz)
INSERT INTO occurrences (event_id, starts_at, ends_at) SELECT $1, $2::timestamptz, $3::timestamptz
WHERE EXISTS (SELECT 1 FROM events WHERE entity_id = $1)
ON CONFLICT (event_id, starts_at) DO UPDATE SET ends_at = EXCLUDED.ends_at`

// upsertEvent writes the event row, then its occurrence. A reconcile passes
// the moment it started as notDeletedSince: an event a hook dropped after that
// is not written back. Hooks pass nil.
func upsertEvent(r indexRow, notDeletedSince *time.Time) error {
	var guard any
	if notDeletedSince != nil {
		guard = utcText(*notDeletedSince)
	}
	amount, lat, lon := floatArg(r.PriceAmount), floatArg(r.VenueLat), floatArg(r.VenueLon)
	if err := dbExec(upsertEventSQL, r.EntityID, r.SchemaSlug, r.Slug, r.Locale, r.Name,
		utcText(r.StartsAt), utcText(r.EndsAt), r.AllDay, r.TimeZone, r.EventStatus, r.AttendanceMode,
		r.VenueID, r.VenueName, r.VenueCity, jsonText(r.CategoryIDs), jsonText(r.CategoryNames), r.SeriesID,
		r.Price, amount, r.Featured, r.Image, r.SourceKind, r.SourceUID, r.SourceURL, guard,
		r.URL, r.VenueSlug, r.VenueAddress, lat, lon, jsonText(r.CategorySlugs)); err != nil {
		return err
	}
	return dbExec(upsertOccurrenceSQL, r.EntityID, utcText(r.StartsAt), utcText(r.EndsAt))
}

// floatArg is a SQL argument: NULL for nil.
func floatArg(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

// deleteEvent removes an event from the index (its occurrences cascade) and
// notes the delete for a reconcile that is running.
func deleteEvent(id string) error {
	return dbExec(`WITH t AS (INSERT INTO deleted_events (entity_id) VALUES ($1)
 ON CONFLICT (entity_id) DO UPDATE SET deleted_at = now())
DELETE FROM events WHERE entity_id = $1`, id)
}

// markReconciled records that a reconcile read every event and removed the
// rows of those that are gone, and forgets old delete notes.
// indexReady is false only when the host says no reconcile has completed yet;
// a failing check counts as ready so a hook never repeats a reconcile blindly.
func indexReady() bool {
	var ready bool
	err := dbJSON(`SELECT to_jsonb(EXISTS (SELECT 1 FROM index_state))`, &ready)
	return err != nil || ready
}

func markReconciled() error {
	if err := dbExec(`INSERT INTO index_state (id, reconciled_at) VALUES (true, now())
ON CONFLICT (id) DO UPDATE SET reconciled_at = now()`); err != nil {
		return err
	}
	return dbExec(`DELETE FROM deleted_events WHERE deleted_at < now() - interval '1 day'`)
}

// indexedIDs maps the entity id of every indexed event to its updated_at.
func indexedIDs() (map[string]string, error) {
	var m map[string]string
	err := dbJSON(`SELECT COALESCE(jsonb_object_agg(entity_id, updated_at), '{}'::jsonb) FROM events`, &m)
	return m, err
}

// categoryRefs are the names and slugs of an event's categories.
type categoryRefs struct{ Names, Slugs []string }

// categoriesOf resolves category ids with one read each; unresolved ids are
// skipped.
func categoriesOf(ids []string) categoryRefs {
	var c categoryRefs
	for _, id := range ids {
		if raw, _, ok := loadRef(id); ok && raw.Name != "" {
			c.Names = append(c.Names, raw.Name)
			if raw.Slug != "" {
				c.Slugs = append(c.Slugs, raw.Slug)
			}
		}
	}
	return c
}

// eventLocation is the zone an event's local times are read in.
func eventLocation(e Event, s settings) *time.Location { return zoneOr(e.TimeZone, s.location()) }

// dateLocation is the zone an event's dates are read and shown in: its own
// zone when it has one (an all-day event is fixed to calendar days there, and
// a timed event's naive start is read there), else loc, the site zone.
func dateLocation(e Event, loc *time.Location) *time.Location {
	return zoneOr(e.TimeZone, loc)
}

// zoneOr loads the named zone, else returns fallback.
func zoneOr(name string, fallback *time.Location) *time.Location {
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return fallback
}

// reindexEntity brings the index row of one entity in line with the entity:
// written when it is a published event of an accepted type, removed otherwise
// (also when the entity is gone). It reports whether the event is indexed.
func reindexEntity(id string, s settings) (bool, error) {
	indexed, _, err := reindexEntityAddr(id, s)
	return indexed, err
}

// reindexEntityAddr is reindexEntity that also returns the address a map would
// need looked up, as reindexDTO does.
func reindexEntityAddr(id string, s settings) (indexed bool, addr string, err error) {
	var raw entityDTO
	if err := callHost(sdk.FnEntitiesGet, map[string]any{"id": id}, &raw); err != nil {
		var he *sdk.HostError
		if errors.As(err, &he) && he.Code == sdk.ErrNotFound {
			return false, "", deleteEvent(id)
		}
		return false, "", err
	}
	return reindexDTO(raw, s, nil)
}

// reindexDTO is reindexEntity for an entity already read. addr is the
// address a map would need looked up (empty when the venue has coordinates or
// the event has no address).
func reindexDTO(raw entityDTO, s settings, notDeletedSince *time.Time) (indexed bool, addr string, err error) {
	if !s.accepts(raw.SchemaSlug) {
		return false, "", deleteEvent(raw.ID)
	}
	e, ok := eventFromEntity(raw, s)
	if !ok {
		return false, "", deleteEvent(raw.ID)
	}
	_, _, _, v := venueOrText(e)
	if err := upsertEvent(rowFromEvent(e, v, categoriesOf(e.CategoryIDs), eventLocation(e, s)), notDeletedSince); err != nil {
		return false, "", err
	}
	venue := Venue{}
	if v != nil {
		venue = *v
	}
	if venue.Lat == nil || venue.Lon == nil {
		addr = addressOf(venue, e)
	}
	return true, addr, nil
}

// indexQuery selects events from the index. Loc is the zone all-day events
// are read back in (the site zone).
type indexQuery struct {
	Upcoming bool
	At       time.Time
	Language string
	Limit    int
	Slug     string
	Loc      *time.Location
}

// queryEventsHead numbers the rows in the wanted order and aggregates them by
// that number, so the order survives the aggregation.
// The result is NULL until a reconcile has completed.
const queryEventsHead = `SELECT CASE WHEN EXISTS (SELECT 1 FROM index_state)
 THEN COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.n), '[]'::jsonb) END FROM (
SELECT e.entity_id AS id, e.schema_slug, e.slug, e.locale, e.name, o.starts_at, o.ends_at, e.all_day, e.time_zone,
 e.event_status, e.attendance_mode, e.venue_id, e.venue_name, e.venue_city, e.category_ids, e.series_id, e.price,
 e.price_amount, e.featured, e.image, e.source_kind, e.source_uid, e.source_url, e.url, e.updated_at,
 row_number() OVER (ORDER BY `
const queryEventsFrom = `) AS n
FROM events e JOIN occurrences o ON o.event_id = e.entity_id
WHERE `
const queryEventsTail = ` AND ($2::text = '' OR e.locale IN ('', 'und', $2::text)) AND ($3::text = '' OR e.slug = $3::text)
LIMIT $4::int) r`

var errIndexNotReady = errors.New("event index not reconciled yet")

// queryEvents reads events from the index. Rows written before the url column
// existed have an empty URL. Before the first complete reconcile the index is
// not trusted and errIndexNotReady is returned.
func queryEvents(q indexQuery) ([]Event, error) {
	order, cond := `o.starts_at DESC, e.name, e.entity_id`, `o.ends_at <= $1::timestamptz`
	if q.Upcoming {
		order, cond = `o.starts_at, e.name, e.entity_id`, `o.ends_at > $1::timestamptz`
	}
	limit := q.Limit
	if limit <= 0 || limit > maxIndexRows {
		limit = maxIndexRows
	}
	var rows *[]map[string]any
	if err := dbJSON(queryEventsHead+order+queryEventsFrom+cond+queryEventsTail, &rows,
		utcText(q.At), q.Language, q.Slug, limit); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, errIndexNotReady
	}
	raw := *rows
	loc := q.Loc
	if loc == nil {
		loc = time.UTC
	}
	evs := make([]Event, 0, len(raw))
	for _, m := range raw {
		if e, ok := eventFromIndex(m, loc); ok {
			evs = append(evs, e)
		}
	}
	return evs, nil
}

// eventFromIndex converts one aggregated row back to an Event.
func eventFromIndex(m map[string]any, loc *time.Location) (Event, bool) {
	start, ok1 := parseTime(attrString(m, "starts_at"), time.UTC)
	end, ok2 := parseTime(attrString(m, "ends_at"), time.UTC)
	if !ok1 || !ok2 {
		return Event{}, false
	}
	e := Event{
		ID: attrString(m, "id"), SchemaSlug: attrString(m, "schema_slug"), Slug: attrString(m, "slug"),
		Locale: attrString(m, "locale"), Name: attrString(m, "name"),
		indexed: true,
		Start:   start, AllDay: attrBool(m, "all_day"), TimeZone: attrString(m, "time_zone"),
		EventStatus: attrString(m, "event_status"), AttendanceMode: attrString(m, "attendance_mode"),
		VenueID: attrString(m, "venue_id"), Venue: attrString(m, "venue_name"), VenueCity: attrString(m, "venue_city"),
		CategoryIDs: attrIDs(m, "category_ids"), SeriesID: attrString(m, "series_id"),
		Price: attrString(m, "price"), PriceAmount: attrFloat(m, "price_amount"), Featured: attrBool(m, "featured"),
		Image: attrString(m, "image"), SourceKind: attrString(m, "source_kind"),
		SourceUID: attrString(m, "source_uid"), SourceURL: attrString(m, "source_url"),
		URL: attrString(m, "url"),
	}
	if t, err := time.Parse(time.RFC3339, attrString(m, "updated_at")); err == nil {
		e.Updated = t
	}
	e.Start, e.End, e.HasEnd = indexTimes(start, end, e.AllDay, e.TimeZone, loc)
	return e, true
}

// indexTimes reads the stored start and end back as an Event holds them. An
// all-day row ends at the midnight after its last day: step back into that
// day, in the event's zone else loc.
func indexTimes(start, end time.Time, allDay bool, tz string, loc *time.Location) (s, e time.Time, hasEnd bool) {
	if allDay {
		zone := zoneOr(tz, loc)
		s = startOfDay(start, zone)
		if last := startOfDay(end.Add(-time.Second), zone); last.After(s) {
			return s, last, true
		}
		return s, time.Time{}, false
	}
	if end.After(start) {
		return start, end, true
	}
	return start, time.Time{}, false
}

// event is the row as an Event; loc is the zone all-day events are read in
// (the site zone). Like every event from the index it lacks the description.
func (r apiRow) event(loc *time.Location) Event {
	e := Event{
		ID: r.ID, SchemaSlug: r.SchemaSlug, Slug: r.Slug, Name: r.Name, URL: r.URL, indexed: true,
		AllDay: r.AllDay, TimeZone: r.TimeZone, EventStatus: r.EventStatus,
		Venue: r.VenueName, VenueCity: r.VenueCity, Address: r.VenueAddress,
		Price: r.Price, PriceAmount: r.PriceAmount, Featured: r.Featured, Image: r.Image,
	}
	e.Start, e.End, e.HasEnd = indexTimes(r.StartsAt.UTC(), r.EndsAt.UTC(), r.AllDay, r.TimeZone, loc)
	return e
}

// rangeQuery selects the occurrences that overlap [From, To), narrowed by the
// filters. Slug and Language are exact and optional.
type rangeQuery struct {
	From, To                        time.Time
	Q, Category, Venue, City, Price string // Price: "", "free" or "paid"
	Slug, Language                  string
	Limit                           int
	Desc                            bool // latest first, for a list of past events
	StartsAfter                     bool // only events that start at or after From, not ones in progress
}

// apiRow is one occurrence as the range query returns it.
type apiRow struct {
	ID            string    `json:"id"`
	SchemaSlug    string    `json:"schema_slug"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	URL           string    `json:"url"`
	StartsAt      time.Time `json:"starts_at"`
	EndsAt        time.Time `json:"ends_at"`
	AllDay        bool      `json:"all_day"`
	TimeZone      string    `json:"time_zone"`
	EventStatus   string    `json:"event_status"`
	VenueName     string    `json:"venue_name"`
	VenueSlug     string    `json:"venue_slug"`
	VenueCity     string    `json:"venue_city"`
	VenueAddress  string    `json:"venue_address"`
	Lat           *float64  `json:"lat"`
	Lon           *float64  `json:"lon"`
	CategoryNames []string  `json:"category_names"`
	Price         string    `json:"price"`
	PriceAmount   *float64  `json:"price_amount"`
	Featured      bool      `json:"featured"`
	Image         string    `json:"image"`
}

// freeSQL is the free rule of priceText: an amount of 0 or the text "free".
const freeSQL = `(COALESCE(e.price_amount = 0, false) OR lower(btrim(e.price)) = 'free')`

// queryRangeSQL overlaps on o.starts_at < to and o.ends_at > from: an all-day
// row ends at the midnight after its last day. Every filter is a bound
// argument; an empty one matches all. Coordinates come from the venue, else
// from the geocode cache.
const queryRangeSQL = `SELECT CASE WHEN EXISTS (SELECT 1 FROM index_state)
 THEN COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.n), '[]'::jsonb) END FROM (
SELECT e.entity_id AS id, e.schema_slug, e.name, e.slug, e.url, o.starts_at, o.ends_at, e.all_day, e.time_zone, e.event_status,
 e.venue_name, e.venue_slug, e.venue_city, e.venue_address,
 COALESCE(e.venue_lat, g.latitude) AS lat, COALESCE(e.venue_lon, g.longitude) AS lon,
 e.category_names, e.price, e.price_amount, e.featured, e.image,
 row_number() OVER (ORDER BY CASE WHEN $11::boolean THEN -extract(epoch FROM o.starts_at) ELSE extract(epoch FROM o.starts_at) END,
  e.name, e.entity_id) AS n
FROM events e JOIN occurrences o ON o.event_id = e.entity_id
LEFT JOIN geocode_cache g ON g.address = e.venue_address AND g.status = 'ok'
WHERE o.starts_at < $2::timestamptz AND o.ends_at > $1::timestamptz
 AND (NOT $12::boolean OR o.starts_at >= $1::timestamptz)
 AND ($3::text = '' OR e.name ILIKE '%' || $3::text || '%' OR e.venue_name ILIKE '%' || $3::text || '%')
 AND ($4::text = '' OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(e.category_names) c WHERE lower(c) = lower($4::text))
  OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(e.category_slugs) c WHERE lower(c) = lower($4::text)))
 AND ($5::text = '' OR lower(e.venue_name) = lower($5::text) OR e.venue_slug = lower($5::text))
 AND ($6::text = '' OR lower(e.venue_city) = lower($6::text))
 AND ($7::text = '' OR ($7::text = 'free' AND ` + freeSQL + `)
  OR ($7::text = 'paid' AND NOT ` + freeSQL + ` AND (COALESCE(e.price_amount > 0, false) OR btrim(e.price) <> '')))
 AND ($8::text = '' OR e.slug = $8::text)
 AND ($9::text = '' OR e.locale IN ('', 'und', $9::text))
LIMIT $10::int) r`

// escapeLike makes s literal inside an ILIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// queryRange reads the occurrences in a range from the index, in start order.
// Before the first complete reconcile it returns errIndexNotReady.
func queryRange(r rangeQuery) ([]apiRow, error) {
	limit := r.Limit
	if limit <= 0 || limit > maxIndexRows {
		limit = maxIndexRows
	}
	var rows *[]apiRow
	if err := dbJSON(queryRangeSQL, &rows, utcText(r.From), utcText(r.To), escapeLike(r.Q), r.Category, r.Venue,
		r.City, r.Price, r.Slug, r.Language, limit, r.Desc, r.StartsAfter); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, errIndexNotReady
	}
	return *rows, nil
}
