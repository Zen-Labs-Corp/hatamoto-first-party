package main

import (
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
