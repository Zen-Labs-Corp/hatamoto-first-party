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
)

// ticketType is one kind of free ticket of an event. Left is Capacity - Taken.
type ticketType struct {
	ID       int64  `json:"id"`
	EventID  string `json:"event_id"`
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
	Taken    int    `json:"taken"`
	Waitlist bool   `json:"waitlist"`
	OnSale   bool   `json:"on_sale"`
	Position int    `json:"position"`
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

	fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	nonKeyRe   = regexp.MustCompile(`[^a-z0-9]+`)
)

// onSeatsFreed is called with a ticket type that was changed, so waiting
// people can be offered the seats a raised capacity gives. Tests replace it.
var onSeatsFreed = promoteAll

func listTypes(eventID string) ([]ticketType, error) {
	out := []ticketType{}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.position, t.id), '[]'::jsonb) FROM (
SELECT id, event_id, name, capacity, taken, waitlist, on_sale, position FROM ticket_types WHERE event_id = $1) t`, &out, eventID)
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
// write are one statement. The number of types per event is capped.
func saveType(t ticketType) error {
	if t.ID == 0 {
		n, err := dbExecN(`INSERT INTO ticket_types (event_id, name, capacity, waitlist, on_sale, position)
SELECT $1, $2, $3, $4, $5, $6
WHERE (SELECT count(*) FROM ticket_types WHERE event_id = $1) < $7`,
			t.EventID, t.Name, t.Capacity, t.Waitlist, t.OnSale, t.Position, maxTypesPerEvent)
		if err != nil {
			return err
		}
		if n == 0 {
			return errTooManyTypes
		}
		return nil
	}
	n, err := dbExecN(`UPDATE ticket_types SET name = $3, capacity = $4, waitlist = $5, on_sale = $6, position = $7, touched_at = now()
WHERE id = $1 AND event_id = $2 AND taken <= $4`,
		t.ID, t.EventID, t.Name, t.Capacity, t.Waitlist, t.OnSale, t.Position)
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
