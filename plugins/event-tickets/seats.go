package main

import (
	"slices"
	"strings"
)

// Seat states in availRow.S, one letter per seat (seat i+1 is S[i]).
const (
	seatFree    = 'o'
	seatTaken   = 'x' // sold, or held by a buyer who is picking
	seatBlocked = 'b'
	seatOff     = 'n' // the ticket type of the section is not on sale
)

// availMap is what the picker draws: the sections bound to an event's ticket
// types. It carries no buyer data, so it can be shared and cached.
type availMap struct {
	Sections []availSection `json:"sections"`
}

// availSection is a grid (Rows) or a zone (Left) with the type that sells it.
type availSection struct {
	ID     int64      `json:"id"`
	Kind   string     `json:"kind"`
	Name   string     `json:"name"`
	Tier   string     `json:"tier"`
	TypeID int64      `json:"type_id"`
	Price  string     `json:"price"`
	Rows   []availRow `json:"rows,omitempty"`
	Left   int        `json:"left,omitempty"`
}

// availRow is one row of a grid: its label and one state letter per seat.
type availRow struct {
	L string `json:"l"`
	S string `json:"s"`
}

// seatAvailability reads the seat states of an event in one query. A seat is
// taken when a pending or active ticket of the event has it or a seat hold
// has not expired yet; a lapsed hold reads as free. Sections that no ticket
// type of the event is bound to are left out. nil when no type is bound.
func seatAvailability(eventID, currency string) (*availMap, error) {
	var rows []struct {
		availSection
		PriceCents int64 `json:"price_cents"`
	}
	err := dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('id', s.id, 'kind', s.kind, 'name', s.name, 'tier', s.tier,
    'type_id', t.id, 'price_cents', t.price_cents, 'left', greatest(t.capacity - t.taken, 0),
    'rows', (SELECT jsonb_agg(jsonb_build_object('l', g.row_label, 's', g.states) ORDER BY g.row_idx) FROM (
        SELECT st.row_idx, st.row_label, string_agg(CASE
            WHEN EXISTS (SELECT 1 FROM attendees a WHERE a.seat_id = st.id AND a.event_id = $1 AND a.status IN ('pending', 'active'))
                OR EXISTS (SELECT 1 FROM seat_holds h WHERE h.seat_id = st.id AND h.event_id = $1 AND h.expires_at > now()) THEN 'x'
            WHEN st.blocked THEN 'b'
            WHEN NOT t.on_sale THEN 'n'
            ELSE 'o' END, '' ORDER BY st.num) AS states
        FROM seats st WHERE st.section_id = s.id AND s.kind = 'grid' GROUP BY st.row_idx, st.row_label) g)
    ) ORDER BY s.position, s.id), '[]'::jsonb)
FROM ticket_types t JOIN map_sections s ON s.id = t.section_id WHERE t.event_id = $1`, &rows, eventID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	m := &availMap{Sections: make([]availSection, 0, len(rows))}
	for _, r := range rows {
		s := r.availSection
		s.Price = formatMoney(r.PriceCents, currency)
		if s.Kind == kindGrid {
			s.Left = 0
		}
		m.Sections = append(m.Sections, s)
	}
	return m, nil
}

// bestSeats picks n seats in a section as (row index, seat number) pairs,
// numbers counted from 1. It takes the front row that has n free seats side
// by side, the block nearest the middle of the row. When no row has such a
// block it takes single seats, front rows first and from the middle of each
// row outwards. nil when fewer than n seats are free.
func bestSeats(rows []availRow, n int) [][2]int {
	if n < 1 {
		return nil
	}
	for ri, r := range rows {
		best, bestOff := 0, 0
		for start := 1; start+n-1 <= len(r.S); start++ {
			if strings.Count(r.S[start-1:start-1+n], string(seatFree)) != n {
				continue
			}
			// distance of the block's middle from the row's middle, doubled to stay in integers
			off := abs(2*start + n - 1 - (len(r.S) + 1))
			if best == 0 || off < bestOff {
				best, bestOff = start, off
			}
		}
		if best > 0 {
			out := make([][2]int, n)
			for i := range out {
				out[i] = [2]int{ri, best + i}
			}
			return out
		}
	}
	var out [][2]int
	for ri, r := range rows {
		var free []int
		for i := range len(r.S) {
			if r.S[i] == seatFree {
				free = append(free, i+1)
			}
		}
		slices.SortStableFunc(free, func(a, b int) int { return abs(2*a-(len(r.S)+1)) - abs(2*b-(len(r.S)+1)) })
		for _, num := range free {
			if out = append(out, [2]int{ri, num}); len(out) == n {
				return out
			}
		}
	}
	return nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
