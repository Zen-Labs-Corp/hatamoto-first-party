package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits of a seating map. They keep every statement and reply bounded.
const (
	maxMaps           = 50
	maxSectionsPerMap = 20
	maxRows           = 60
	maxSeatsPerRow    = 100
	maxSeatsPerMap    = 5000
	maxTierRunes      = 40
)

const (
	kindGrid = "grid"
	kindZone = "zone"
)

var (
	rowLabelRe = regexp.MustCompile(`^[A-Z0-9]{1,4}$`)
	letterRe   = regexp.MustCompile(`^[A-Z]{1,4}$`)
	digitRe    = regexp.MustCompile(`^[0-9]{1,4}$`)

	errBadSection = errors.New("bad section")
)

// seatMap is a named set of sections an event's ticket types are bound to.
type seatMap struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Sections []section `json:"sections"`
	Seats    int       `json:"seats"` // unblocked seats of all grid sections
}

// section is a grid of seats (rows x seats per row) or a GA zone with a capacity.
type section struct {
	ID          int64    `json:"id"`
	MapID       int64    `json:"map_id"`
	Kind        string   `json:"kind"` // grid or zone
	Name        string   `json:"name"`
	Tier        string   `json:"tier"`
	Position    int      `json:"position"`
	Rows        []string `json:"rows"`
	SeatsPerRow int      `json:"seats_per_row"`
	Capacity    int      `json:"capacity"`
}

// nextLetters is the label after s among labels of the same length ("AZ" -> "BA").
// ok is false after the last one ("ZZ").
func nextLetters(s string) (string, bool) {
	b := []byte(s)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 'Z' {
			b[i]++
			return string(b), true
		}
		b[i] = 'A'
	}
	return "", false
}

// rowRange expands "A-J", "AA-AD" or "1-12" (ascending, same kind of label).
func rowRange(from, to string) ([]string, bool) {
	var out []string
	switch {
	case letterRe.MatchString(from) && letterRe.MatchString(to) && len(from) == len(to):
		if from > to {
			return nil, false
		}
		for l := from; len(out) <= maxRows; {
			out = append(out, l)
			if l == to {
				return out, true
			}
			var ok bool
			if l, ok = nextLetters(l); !ok {
				return nil, false
			}
		}
	case digitRe.MatchString(from) && digitRe.MatchString(to):
		a, _ := strconv.Atoi(from)
		b, _ := strconv.Atoi(to)
		if a < 1 || a > b || b-a >= maxRows {
			return nil, false
		}
		for n := a; n <= b; n++ {
			out = append(out, strconv.Itoa(n))
		}
		return out, true
	}
	return nil, false
}

// parseRows turns "A-J", "K,L,M", "AA-AD" or "1-12" into row labels (a comma
// list may mix labels and ranges); false when it is bad, has a repeated label
// or more than maxRows rows.
func parseRows(s string) ([]string, bool) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		var labels []string
		if from, to, isRange := strings.Cut(part, "-"); isRange {
			var ok bool
			if labels, ok = rowRange(strings.TrimSpace(from), strings.TrimSpace(to)); !ok {
				return nil, false
			}
		} else if rowLabelRe.MatchString(part) {
			labels = []string{part}
		} else {
			return nil, false
		}
		for _, l := range labels {
			if seen[l] || len(out) >= maxRows {
				return nil, false
			}
			seen[l] = true
			out = append(out, l)
		}
	}
	return out, len(out) > 0
}

// seatLabel is the text printed on a ticket for a grid seat: "Stalls A-12".
func seatLabel(sectionName, row string, num int) string {
	return fmt.Sprintf("%s %s-%d", sectionName, row, num)
}

// validSection checks a section as the owner typed it (names already trimmed).
func validSection(s section) error {
	if n := utf8.RuneCountInString(s.Name); s.Name != strings.TrimSpace(s.Name) || n < 1 || n > maxNameRunes {
		return errBadSection
	}
	if _, ok := cleanText(s.Name, maxNameRunes); !ok {
		return errBadSection
	}
	if s.Tier != "" {
		if t, ok := cleanText(s.Tier, maxTierRunes); !ok || t != s.Tier {
			return errBadSection
		}
	}
	if s.Position < 0 || s.Position > maxPosition {
		return errBadSection
	}
	switch s.Kind {
	case kindGrid:
		if s.Capacity != 0 || len(s.Rows) < 1 || len(s.Rows) > maxRows ||
			s.SeatsPerRow < 1 || s.SeatsPerRow > maxSeatsPerRow || len(s.Rows)*s.SeatsPerRow > maxSeatsPerMap {
			return errBadSection
		}
		seen := map[string]bool{}
		for _, r := range s.Rows {
			if !rowLabelRe.MatchString(r) || seen[r] {
				return errBadSection
			}
			seen[r] = true
		}
	case kindZone:
		if len(s.Rows) != 0 || s.SeatsPerRow != 0 || s.Capacity < 1 || s.Capacity > maxCapacity {
			return errBadSection
		}
	default:
		return errBadSection
	}
	return nil
}

var (
	errInUse   = errors.New("in use")
	errTooMany = errors.New("too many")
)

// seatCell is one seat of a grid section as the admin page draws it.
type seatCell struct {
	ID      int64
	Row     int // index of the row in the section
	Label   string
	Num     int
	Blocked bool
}

// UnmarshalJSON reads the compact [id, row index, row label, number, blocked]
// form readMap selects, which keeps a 5,000 seat map small.
func (c *seatCell) UnmarshalJSON(b []byte) error {
	var t []any
	if err := json.Unmarshal(b, &t); err != nil || len(t) != 5 {
		return errors.New("bad seat")
	}
	id, ok1 := t[0].(float64)
	row, ok2 := t[1].(float64)
	label, ok3 := t[2].(string)
	num, ok4 := t[3].(float64)
	blocked, ok5 := t[4].(bool)
	if !(ok1 && ok2 && ok3 && ok4 && ok5) {
		return errors.New("bad seat")
	}
	*c = seatCell{int64(id), int(row), label, int(num), blocked}
	return nil
}

// MarshalJSON writes the same compact form the admin page reads.
func (c seatCell) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{c.ID, c.Row, c.Label, c.Num, c.Blocked})
}

const sectionCols = `s.id, s.map_id, s.kind, s.name, s.tier, s.position, s.row_labels AS rows, s.seats_per_row, s.capacity`

func (m *seatMap) normalise() {
	if m.Sections == nil {
		m.Sections = []section{}
	}
	for i := range m.Sections {
		if m.Sections[i].Rows == nil {
			m.Sections[i].Rows = []string{}
		}
	}
}

// listMaps returns every map with its sections and its number of unblocked grid seats.
func listMaps() ([]seatMap, error) {
	out := []seatMap{}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]'::jsonb) FROM (
SELECT m.id, m.name,
 (SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.position, x.id), '[]'::jsonb) FROM (SELECT `+sectionCols+` FROM map_sections s WHERE s.map_id = m.id) x) AS sections,
 (SELECT count(*)::int FROM seats st JOIN map_sections s ON s.id = st.section_id WHERE s.map_id = m.id AND NOT st.blocked) AS seats
FROM seat_maps m) t`, &out)
	for i := range out {
		out[i].normalise()
	}
	return out, err
}

// readMap returns one map and, for each of its sections in order, the seats of
// a grid section (none for a zone). errNotFound when there is no such map.
func readMap(id int64) (*seatMap, [][]seatCell, error) {
	var m seatMap
	err := dbJSON(`SELECT coalesce((SELECT to_jsonb(t) FROM (
SELECT m.id, m.name,
 (SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.position, x.id), '[]'::jsonb) FROM (SELECT `+sectionCols+` FROM map_sections s WHERE s.map_id = m.id) x) AS sections,
 (SELECT count(*)::int FROM seats st JOIN map_sections s ON s.id = st.section_id WHERE s.map_id = m.id AND NOT st.blocked) AS seats
FROM seat_maps m WHERE m.id = $1) t), '{}'::jsonb)`, &m, id)
	if err != nil {
		return nil, nil, err
	}
	if m.ID == 0 {
		return nil, nil, errNotFound
	}
	m.normalise()
	var rows []struct {
		Section int64      `json:"s"`
		Cells   []seatCell `json:"c"`
	}
	err = dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('s', g.section_id, 'c', g.cells) ORDER BY g.section_id), '[]'::jsonb) FROM (
SELECT st.section_id, jsonb_agg(jsonb_build_array(st.id, st.row_idx, st.row_label, st.num, st.blocked) ORDER BY st.row_idx, st.num) AS cells
FROM seats st JOIN map_sections s ON s.id = st.section_id WHERE s.map_id = $1 GROUP BY st.section_id) g`, &rows, id)
	if err != nil {
		return nil, nil, err
	}
	bySection := map[int64][]seatCell{}
	for _, r := range rows {
		bySection[r.Section] = r.Cells
	}
	cells := make([][]seatCell, len(m.Sections))
	for i, s := range m.Sections {
		cells[i] = bySection[s.ID]
	}
	return &m, cells, nil
}

// saveMap adds a map (id 0, at most maxMaps) or renames one and returns its id.
func saveMap(id int64, name string) (int64, error) {
	if id == 0 {
		var got int64
		err := dbJSON(`WITH i AS (INSERT INTO seat_maps (name) SELECT $1 WHERE (SELECT count(*) FROM seat_maps) < $2 RETURNING id)
SELECT to_jsonb(coalesce((SELECT id FROM i), 0))`, &got, name, maxMaps)
		if err != nil {
			return 0, err
		}
		if got == 0 {
			return 0, errTooMany
		}
		return got, nil
	}
	n, err := dbExecN(`UPDATE seat_maps SET name = $2 WHERE id = $1`, id, name)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, errNotFound
	}
	return id, nil
}

// diagnose tells why a guarded statement on a row changed nothing: the row
// is gone (errNotFound) or a guard refused it (refusal).
func diagnose(table string, id int64, refusal error) error {
	var has bool
	if err := dbJSON(`SELECT to_jsonb(EXISTS(SELECT 1 FROM `+table+` WHERE id = $1))`, &has, id); err != nil {
		return err
	}
	if has {
		return refusal
	}
	return errNotFound
}

// deleteMap removes a map nobody uses: no ticket type bound to one of its
// sections and no ticket on one of its seats. errInUse otherwise.
func deleteMap(id int64) error {
	n, err := dbExecN(`DELETE FROM seat_maps m WHERE m.id = $1
AND NOT EXISTS (SELECT 1 FROM ticket_types t JOIN map_sections s ON s.id = t.section_id WHERE s.map_id = m.id)
AND NOT EXISTS (SELECT 1 FROM attendees a JOIN seats st ON st.id = a.seat_id JOIN map_sections s ON s.id = st.section_id WHERE s.map_id = m.id)`, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return diagnose("seat_maps", id, errInUse)
	}
	return nil
}

// deleteSection removes a section nobody uses (see deleteMap). errInUse otherwise.
func deleteSection(id int64) error {
	n, err := dbExecN(`DELETE FROM map_sections s WHERE s.id = $1
AND NOT EXISTS (SELECT 1 FROM ticket_types t WHERE t.section_id = s.id)
AND NOT EXISTS (SELECT 1 FROM attendees a JOIN seats st ON st.id = a.seat_id WHERE st.section_id = s.id)
AND NOT EXISTS (SELECT 1 FROM seat_holds h JOIN seats st ON st.id = h.seat_id WHERE st.section_id = s.id AND h.expires_at > now())`, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return diagnose("map_sections", id, errInUse)
	}
	return nil
}

// seatsWant lists the seats a section's geometry has: one per row label and number.
const seatsWant = ` want AS (SELECT sec.id AS section_id, (r.ord - 1)::int AS row_idx, r.label, n.num
    FROM sec, jsonb_array_elements_text(sec.row_labels) WITH ORDINALITY AS r(label, ord), generate_series(1, sec.seats_per_row) AS n(num)
    WHERE sec.kind = 'grid'),`

// saveSection adds a section to a map (s.ID 0) or changes one and returns its
// id. The seats of a grid section are generated in the same statement: seats
// that stay are upserted (so their blocked flag and tickets are kept), seats
// outside the new geometry are deleted. The rows and seats per row of a
// section with tickets or live holds cannot change (errInUse), nor can a
// capacity drop below the tickets taken of a type bound to it; a map has at
// most maxSectionsPerMap sections and maxSeatsPerMap seats (errTooMany).
// The capacity of the ticket types bound to the section follows in the same
// statement.
func saveSection(s section) (int64, error) {
	rows, err := json.Marshal(s.Rows)
	if err != nil {
		return 0, err
	}
	if s.Rows == nil {
		rows = []byte("[]")
	}
	nrows := len(s.Rows)
	var got int64
	if s.ID == 0 {
		err = dbJSON(`WITH cnt AS (SELECT EXISTS (SELECT 1 FROM seat_maps WHERE id = $1)
    AND (SELECT count(*) FROM map_sections WHERE map_id = $1) < $9
    AND (SELECT coalesce(sum(jsonb_array_length(o.row_labels) * o.seats_per_row), 0) FROM map_sections o WHERE o.map_id = $1 AND o.kind = 'grid') + $10::int * $6::int <= $11 AS go),
 sec AS (INSERT INTO map_sections (map_id, kind, name, tier, position, row_labels, seats_per_row, capacity)
    SELECT $1, $2, $3, $4, $5, $7::jsonb, $6, $8 FROM cnt WHERE cnt.go
    RETURNING id, kind, row_labels, seats_per_row),`+seatsWant+`
 ins AS (INSERT INTO seats (section_id, row_idx, row_label, num) SELECT section_id, row_idx, label, num FROM want RETURNING id)
SELECT to_jsonb(coalesce((SELECT id FROM sec), 0))`,
			&got, s.MapID, s.Kind, s.Name, s.Tier, s.Position, s.SeatsPerRow, string(rows), s.Capacity,
			maxSectionsPerMap, nrows, maxSeatsPerMap)
		if err != nil {
			return 0, err
		}
		if got == 0 {
			return 0, diagnose("seat_maps", s.MapID, errTooMany)
		}
		return got, nil
	}
	newCap := `$8::int`
	if s.Kind == kindGrid {
		newCap = `($9::int * $6::int - (SELECT count(*) FROM seats bs WHERE bs.section_id = $1 AND bs.blocked AND bs.row_idx < $9::int AND bs.num <= $6::int))`
	}
	err = dbJSON(`WITH cur AS (SELECT c.id, c.map_id FROM map_sections c WHERE c.id = $1 AND c.kind = $2 FOR UPDATE),
 chk AS (SELECT (SELECT count(*) FROM cur) = 1
    AND (EXISTS (SELECT 1 FROM map_sections c WHERE c.id = $1 AND c.row_labels = $7::jsonb AND c.seats_per_row = $6)
        OR (NOT EXISTS (SELECT 1 FROM attendees a JOIN seats st ON st.id = a.seat_id WHERE st.section_id = $1)
            AND NOT EXISTS (SELECT 1 FROM seat_holds h JOIN seats st ON st.id = h.seat_id WHERE st.section_id = $1 AND h.expires_at > now())))
    AND (SELECT coalesce(sum(jsonb_array_length(o.row_labels) * o.seats_per_row), 0) FROM map_sections o
        WHERE o.map_id = (SELECT map_id FROM cur) AND o.kind = 'grid' AND o.id <> $1) + $9::int * $6::int <= $10
    AND `+newCap+` >= 1
    AND NOT EXISTS (SELECT 1 FROM ticket_types t WHERE t.section_id = $1 AND t.taken > `+newCap+`) AS go),
 sec AS (UPDATE map_sections s SET name = $3, tier = $4, position = $5, row_labels = $7::jsonb, seats_per_row = $6, capacity = $8
    FROM chk WHERE chk.go AND s.id = $1 RETURNING s.id, s.kind, s.row_labels, s.seats_per_row),`+seatsWant+`
 ins AS (INSERT INTO seats (section_id, row_idx, row_label, num) SELECT section_id, row_idx, label, num FROM want
    ON CONFLICT (section_id, row_idx, num) DO UPDATE SET row_label = EXCLUDED.row_label RETURNING id),
 del AS (DELETE FROM seats st USING sec WHERE st.section_id = sec.id AND sec.kind = 'grid'
    AND NOT EXISTS (SELECT 1 FROM want w WHERE w.row_idx = st.row_idx AND w.num = st.num) RETURNING st.id),
 cap AS (UPDATE ticket_types t SET capacity = `+newCap+`, touched_at = now() FROM sec WHERE t.section_id = sec.id RETURNING t.id)
SELECT to_jsonb(coalesce((SELECT id FROM sec), 0))`,
		&got, s.ID, s.Kind, s.Name, s.Tier, s.Position, s.SeatsPerRow, string(rows), s.Capacity, nrows, maxSeatsPerMap)
	if err != nil {
		return 0, err
	}
	if got == 0 {
		return 0, diagnose("map_sections", s.ID, errInUse)
	}
	return got, nil
}

// setBlocked blocks or unblocks seats of a grid section and returns how many
// changed. All or nothing: it is refused (errInUse) when a seat is not in the
// section, a seat to block has a live hold or a ticket in any event, no free
// seat would be left, or a bound type would hold more tickets than seats. The
// capacity of the bound ticket types follows in the same statement.
func setBlocked(sectionID int64, seatIDs []int64, blocked bool) (int, error) {
	ids, err := json.Marshal(seatIDs)
	if err != nil {
		return 0, err
	}
	var res struct {
		OK bool `json:"ok"`
		N  int  `json:"n"`
	}
	const newCap = `((SELECT count(*) FROM seats u WHERE u.section_id = $1 AND NOT u.blocked)
        + (CASE WHEN $3::boolean THEN -1 ELSE 1 END) * (SELECT count(*) FROM chg))`
	err = dbJSON(`WITH ids AS (SELECT DISTINCT v::bigint AS id FROM jsonb_array_elements_text($2::jsonb) AS v),
 pick AS (SELECT s.id, s.blocked FROM seats s JOIN ids ON ids.id = s.id WHERE s.section_id = $1),
 chg AS (SELECT p.id FROM pick p WHERE p.blocked <> $3::boolean),
 chk AS (SELECT (SELECT count(*) FROM ids) = (SELECT count(*) FROM pick)
    AND EXISTS (SELECT 1 FROM map_sections WHERE id = $1 AND kind = 'grid')
    AND ($3::boolean = false OR (
        NOT EXISTS (SELECT 1 FROM chg JOIN attendees a ON a.seat_id = chg.id WHERE a.status IN ('pending', 'active'))
        AND NOT EXISTS (SELECT 1 FROM chg JOIN seat_holds h ON h.seat_id = chg.id WHERE h.expires_at > now())))
    AND `+newCap+` >= 1
    AND NOT EXISTS (SELECT 1 FROM ticket_types t WHERE t.section_id = $1 AND t.taken > `+newCap+`) AS go),
 upd AS (UPDATE seats s SET blocked = $3::boolean FROM chg, chk WHERE chk.go AND s.id = chg.id RETURNING s.id),
 cap AS (UPDATE ticket_types t SET capacity = `+newCap+`, touched_at = now() FROM chk
    WHERE chk.go AND t.section_id = $1 AND EXISTS (SELECT 1 FROM chg) RETURNING t.id)
SELECT jsonb_build_object('ok', (SELECT go FROM chk), 'n', (SELECT count(*) FROM upd))`,
		&res, sectionID, string(ids), blocked)
	if err != nil {
		return 0, err
	}
	if !res.OK {
		return 0, diagnose("map_sections", sectionID, errInUse)
	}
	return res.N, nil
}
