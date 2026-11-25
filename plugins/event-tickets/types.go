package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxTypesPerEvent  = 20
	maxFieldsPerEvent = 10
	maxFieldOptions   = 20
	maxNameRunes      = 80
	maxCapacity       = 100000
	maxPosition       = 1000
	maxPriceCents     = 99999999
)

// ticketType is one kind of ticket of an event, free when Price is 0. Left is Capacity - Taken.
type ticketType struct {
	ID       int64  `json:"id"`
	EventID  string `json:"event_id"`
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
	Taken    int    `json:"taken"`
	Waitlist bool   `json:"waitlist"`
	OnSale   bool   `json:"on_sale"`
	Position int    `json:"position"`
	Price    int64  `json:"price_cents"` // minor units of the currency setting

	// SectionID binds the type to a seating map section, whose seats (grid) or
	// capacity (zone) then set Capacity. Seated is true for a grid section.
	// Unbind asks saveType to drop the binding of a type that has none sold.
	SectionID *int64 `json:"section_id"`
	Seated    bool   `json:"seated"`
	Unbind    bool   `json:"-"`
}

// field is a question asked once per order.
type field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"` // text, select or checkbox
	Options  []string `json:"options"`
	Required bool     `json:"required"`
}

var (
	errCapacityBelowTaken = errors.New("capacity below tickets already taken")
	errTooManyTypes       = errors.New("too many ticket types")
	errNotFound           = errors.New("not found")

	// The texts of these are shown to the owner as they are.
	errSeatedNeedsPrice = errors.New("A seat section needs a price above zero")
	errOtherMap         = errors.New("An event uses one map; this section is on another map")
	errSectionTaken     = errors.New("This ticket type has sold tickets; add a new type for another section")
	errSectionUsed      = errors.New("Another ticket type of this event already uses this section")

	fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	nonKeyRe   = regexp.MustCompile(`[^a-z0-9]+`)
)

// onSeatsFreed is called with a ticket type that was changed, so waiting
// people can be offered the seats a raised capacity gives. Tests replace it.
var onSeatsFreed = promoteAll

func listTypes(eventID string) ([]ticketType, error) {
	out := []ticketType{}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.position, t.id), '[]'::jsonb) FROM (
SELECT id, event_id, name, capacity, taken, waitlist, on_sale, position, price_cents, section_id,
 coalesce(section_id IN (SELECT ms.id FROM map_sections ms WHERE ms.kind = 'grid'), false) AS seated FROM ticket_types WHERE event_id = $1) t`, &out, eventID)
	return out, err
}

func listFields(eventID string) ([]field, error) {
	out := []field{}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.position, t.key), '[]'::jsonb) FROM (
SELECT key, label, kind, options, required, position FROM fields WHERE event_id = $1) t`, &out, eventID)
	for i := range out {
		if out[i].Options == nil {
			out[i].Options = []string{}
		}
	}
	return out, err
}

// typeExists reports whether the ticket type belongs to the event.
func typeExists(id int64, eventID string) (bool, error) {
	var has bool
	err := dbJSON(`SELECT to_jsonb(EXISTS(SELECT 1 FROM ticket_types WHERE id = $1 AND event_id = $2))`, &has, id, eventID)
	return has, err
}

// saveType adds a ticket type (t.ID 0) or changes one of the event. Changing
// the capacity below the tickets already taken is refused; the check and the
// write are one statement. The number of types per event is capped. A priced
// type has no waitlist: seats of a priced type are only taken by paying.
// A type bound to a section (t.SectionID) is saved by saveBound.
func saveType(t ticketType) error {
	if t.Price > 0 {
		t.Waitlist = false
	}
	if t.SectionID != nil {
		return saveBound(t)
	}
	if t.Unbind && t.ID != 0 {
		return unbindType(t)
	}
	if t.ID == 0 {
		n, err := dbExecN(`INSERT INTO ticket_types (event_id, name, capacity, waitlist, on_sale, position, price_cents)
SELECT $1, $2, $3, $4, $5, $6, $8
WHERE (SELECT count(*) FROM ticket_types WHERE event_id = $1) < $7`,
			t.EventID, t.Name, t.Capacity, t.Waitlist, t.OnSale, t.Position, maxTypesPerEvent, t.Price)
		if err != nil {
			return err
		}
		if n == 0 {
			return errTooManyTypes
		}
		return nil
	}
	n, err := dbExecN(`UPDATE ticket_types SET name = $3, capacity = $4, waitlist = $5, on_sale = $6, position = $7, price_cents = $8, touched_at = now()
WHERE id = $1 AND event_id = $2 AND taken <= $4 AND section_id IS NULL`,
		t.ID, t.EventID, t.Name, t.Capacity, t.Waitlist, t.OnSale, t.Position, t.Price)
	if err != nil {
		return err
	}
	if n == 0 {
		has, err := typeExists(t.ID, t.EventID)
		if err != nil {
			return err
		}
		if has {
			return errCapacityBelowTaken
		}
		return errNotFound
	}
	onSeatsFreed(t.ID)
	return nil
}

// boundResult is what a statement that binds a type to a section answers: how
// many rows it wrote and, per guard, whether it held, so a refusal can be named.
type boundResult struct {
	N       int  `json:"n"`
	Section bool `json:"section"` // the section exists and has seats
	Type    bool `json:"type"`    // the type exists (a change only)
	Priced  bool `json:"priced"`  // the price is above zero
	OneMap  bool `json:"one_map"` // the event's other bound types are on the same map
	Free    bool `json:"free"`    // no other type of the event uses the section
	Movable bool `json:"movable"` // the type keeps its section or has sold nothing
	Roomy   bool `json:"roomy"`   // the section holds the tickets already taken
	Room    bool `json:"room"`    // the event has room for one more type (an add only)
}

// refusal names the guard that stopped a bound save.
func (r boundResult) refusal(add bool) error {
	switch {
	case !add && !r.Type, !r.Section:
		return errNotFound
	case !r.Priced:
		return errSeatedNeedsPrice
	case !r.OneMap:
		return errOtherMap
	case !r.Free:
		return errSectionUsed
	case !add && !r.Movable:
		return errSectionTaken
	case !add && !r.Roomy:
		return errCapacityBelowTaken
	}
	return errTooManyTypes
}

// sectionCapacity is the capacity a section gives a type: its unblocked seats, or the zone capacity.
const sectionCapacity = `SELECT s.id, s.map_id,
    (CASE WHEN s.kind = 'grid' THEN (SELECT count(*) FROM seats st WHERE st.section_id = s.id AND NOT st.blocked) ELSE s.capacity END)::int AS cap
    FROM map_sections s WHERE s.id = `

// saveBound adds or changes a ticket type bound to a section. Its capacity
// is the section's, whatever the form said, and it has no waitlist. One
// statement checks every guard against the latest committed rows: a price
// above zero (seats are only sold through checkout), one map per event, one
// type per section, a section change only while nothing is sold, and tickets
// taken within the section's capacity. The unique index on (event, section)
// backs the checks.
func saveBound(t ticketType) error {
	var res boundResult
	add := t.ID == 0
	if add {
		err := dbJSON(`WITH sec AS (`+sectionCapacity+`$6),
 flags AS (SELECT EXISTS (SELECT 1 FROM sec) AND (SELECT cap FROM sec) >= 1 AS section, $5::bigint > 0 AS priced,
    NOT EXISTS (SELECT 1 FROM ticket_types o JOIN map_sections os ON os.id = o.section_id
        WHERE o.event_id = $1 AND os.map_id <> (SELECT map_id FROM sec)) AS one_map,
    NOT EXISTS (SELECT 1 FROM ticket_types o WHERE o.event_id = $1 AND o.section_id = $6) AS free,
    (SELECT count(*) FROM ticket_types WHERE event_id = $1) < $7 AS room),
 i AS (INSERT INTO ticket_types (event_id, name, capacity, waitlist, on_sale, position, price_cents, section_id)
    SELECT $1, $2, sec.cap, false, $3, $4, $5, sec.id FROM sec, flags
    WHERE flags.section AND flags.priced AND flags.one_map AND flags.free AND flags.room RETURNING id)
SELECT to_jsonb(flags) || jsonb_build_object('n', (SELECT count(*) FROM i)) FROM flags`,
			&res, t.EventID, t.Name, t.OnSale, t.Position, t.Price, *t.SectionID, maxTypesPerEvent)
		if err != nil {
			return err
		}
		if res.N == 0 {
			return res.refusal(true)
		}
		return nil
	}
	err := dbJSON(`WITH sec AS (`+sectionCapacity+`$7),
 cur AS (SELECT c.id, c.section_id, c.taken FROM ticket_types c WHERE c.id = $1 AND c.event_id = $2 FOR UPDATE),
 flags AS (SELECT EXISTS (SELECT 1 FROM cur) AS type, EXISTS (SELECT 1 FROM sec) AND (SELECT cap FROM sec) >= 1 AS section,
    $6::bigint > 0 AS priced,
    NOT EXISTS (SELECT 1 FROM ticket_types o JOIN map_sections os ON os.id = o.section_id
        WHERE o.event_id = $2 AND o.id <> $1 AND os.map_id <> (SELECT map_id FROM sec)) AS one_map,
    NOT EXISTS (SELECT 1 FROM ticket_types o WHERE o.event_id = $2 AND o.id <> $1 AND o.section_id = $7) AS free,
    coalesce((SELECT c.section_id = $7 OR c.taken = 0 FROM cur c), false) AS movable,
    coalesce((SELECT c.taken <= s.cap FROM cur c, sec s), false) AS roomy),
 u AS (UPDATE ticket_types t SET name = $3, capacity = sec.cap, waitlist = false, on_sale = $4, position = $5, price_cents = $6,
        section_id = sec.id, touched_at = now()
    FROM sec, cur, flags WHERE t.id = cur.id AND flags.section AND flags.priced AND flags.one_map AND flags.free
        AND flags.movable AND flags.roomy RETURNING t.id)
SELECT to_jsonb(flags) || jsonb_build_object('n', (SELECT count(*) FROM u)) FROM flags`,
		&res, t.ID, t.EventID, t.Name, t.OnSale, t.Position, t.Price, *t.SectionID)
	if err != nil {
		return err
	}
	if res.N == 0 {
		return res.refusal(false)
	}
	onSeatsFreed(t.ID)
	return nil
}

// unbindType saves a type and drops its section binding, which is only
// allowed while nothing of it is sold (the seats of a ticket live in the section).
func unbindType(t ticketType) error {
	var res struct {
		N     int  `json:"n"`
		Found bool `json:"found"`
		Sold  bool `json:"sold"` // bound and tickets taken
	}
	err := dbJSON(`WITH cur AS (SELECT c.id, c.section_id, c.taken FROM ticket_types c WHERE c.id = $1 AND c.event_id = $2 FOR UPDATE),
 u AS (UPDATE ticket_types t SET name = $3, capacity = $4, waitlist = $5, on_sale = $6, position = $7, price_cents = $8,
        section_id = NULL, touched_at = now()
    FROM cur WHERE t.id = cur.id AND t.taken <= $4 AND (cur.section_id IS NULL OR cur.taken = 0) RETURNING t.id)
SELECT jsonb_build_object('n', (SELECT count(*) FROM u), 'found', EXISTS (SELECT 1 FROM cur),
    'sold', EXISTS (SELECT 1 FROM cur WHERE section_id IS NOT NULL AND taken > 0))`,
		&res, t.ID, t.EventID, t.Name, t.Capacity, t.Waitlist, t.OnSale, t.Position, t.Price)
	if err != nil {
		return err
	}
	switch {
	case res.N > 0:
		onSeatsFreed(t.ID)
		return nil
	case !res.Found:
		return errNotFound
	case res.Sold:
		return errSectionTaken
	}
	return errCapacityBelowTaken
}

// deleteType removes a ticket type nobody has used. One that has attendees or
// waitlist entries stays, with its sale closed; closed reports that.
func deleteType(id int64) (closed bool, err error) {
	n, err := dbExecN(`DELETE FROM ticket_types WHERE id = $1
AND NOT EXISTS (SELECT 1 FROM attendees WHERE type_id = $1)
AND NOT EXISTS (SELECT 1 FROM waitlist WHERE type_id = $1)`, id)
	if err != nil || n > 0 {
		return false, err
	}
	n, err = dbExecN(`UPDATE ticket_types SET on_sale = false, touched_at = now() WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, errNotFound
	}
	return true, nil
}

// cleanText trims s and reports whether it is 1..max runes of printable text.
func cleanText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n == 0 || n > max || !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return s, true
}

// fieldKey makes the key answers are stored under from a label: lower-case
// letters and digits joined by underscores.
func fieldKey(label string) string {
	k := strings.Trim(nonKeyRe.ReplaceAllString(strings.ToLower(label), "_"), "_")
	if k == "" {
		k = "field"
	}
	if k[0] >= '0' && k[0] <= '9' {
		k = "f_" + k
	}
	if len(k) > 32 {
		k = strings.TrimRight(k[:32], "_")
	}
	return k
}

// validateFields checks and normalises the custom fields of an event. A key
// that is given and valid is kept, so renaming a label does not orphan the
// answers already stored; otherwise it is derived from the label.
func validateFields(in []field) ([]field, error) {
	if len(in) > maxFieldsPerEvent {
		return nil, errors.New("too many fields")
	}
	seen := map[string]bool{}
	out := make([]field, 0, len(in))
	for _, f := range in {
		label, ok := cleanText(f.Label, maxNameRunes)
		if !ok {
			return nil, errors.New("bad label")
		}
		f.Label = label
		if !fieldKeyRe.MatchString(f.Key) {
			f.Key = fieldKey(label)
		}
		if !fieldKeyRe.MatchString(f.Key) || seen[f.Key] {
			return nil, errors.New("duplicate field")
		}
		seen[f.Key] = true
		switch f.Kind {
		case "text", "checkbox":
			f.Options = []string{}
		case "select":
			if len(f.Options) < 1 || len(f.Options) > maxFieldOptions {
				return nil, errors.New("a choice needs 1 to 20 options")
			}
			opts := make([]string, 0, len(f.Options))
			for _, o := range f.Options {
				o, ok := cleanText(o, maxNameRunes)
				if !ok {
					return nil, errors.New("bad option")
				}
				opts = append(opts, o)
			}
			f.Options = opts
		default:
			return nil, errors.New("bad kind")
		}
		out = append(out, f)
	}
	return out, nil
}

// saveFields replaces the custom fields of an event in one statement: the
// fields no longer listed are deleted, the others inserted or updated.
func saveFields(eventID string, fields []field) error {
	type row struct {
		field
		Pos int `json:"pos"`
	}
	rows := make([]row, len(fields))
	for i, f := range fields {
		rows[i] = row{f, i}
	}
	doc, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return dbExec(`WITH d AS (
    DELETE FROM fields WHERE event_id = $1
    AND key NOT IN (SELECT x.key FROM jsonb_to_recordset($2::jsonb) AS x(key text))
)
INSERT INTO fields (event_id, key, position, label, kind, options, required)
SELECT $1, x.key, x.pos, x.label, x.kind, x.options, x.required
FROM jsonb_to_recordset($2::jsonb) AS x(key text, pos int, label text, kind text, options jsonb, required boolean)
ON CONFLICT (event_id, key) DO UPDATE SET position = EXCLUDED.position, label = EXCLUDED.label,
    kind = EXCLUDED.kind, options = EXCLUDED.options, required = EXCLUDED.required`, eventID, string(doc))
}
