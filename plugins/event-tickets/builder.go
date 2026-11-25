package main

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// sourceTickets lists the ticket types on sale for the page's own event.
const sourceTickets = "ext.event-tickets.tickets"

func init() { guest.OnFilter(sdk.BuilderData, answerBuilder) }

// answerBuilder answers the tickets source and leaves other requests alone.
// It reads one table and never the event itself, so no event attribute (the
// join link above all) can reach a cached page. Counts may be as old as the
// page cache; the RSVP route re-checks capacity atomically. A failure yields
// an empty list, never an error string.
func answerBuilder(r sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
	var types []ticketType
	loaded := false
	for _, q := range r.Requests {
		if q.Source != sourceTickets {
			continue
		}
		res := sdk.BuilderDataResult{ID: q.ID}
		if r.EntityID != "" {
			if !loaded {
				loaded = true
				all, err := listTypes(r.EntityID)
				if err == nil {
					for _, t := range all {
						if t.OnSale {
							types = append(types, t)
						}
					}
				}
			}
			limit := q.Limit
			if limit <= 0 || limit > sdk.MaxBuilderDataItems {
				limit = sdk.MaxBuilderDataItems
			}
			for _, t := range types[:min(len(types), limit)] {
				res.Items = append(res.Items, ticketItem(t))
			}
		}
		r.Results = append(r.Results, res)
	}
	return r, nil
}

func ticketItem(t ticketType) sdk.BuilderDataItem {
	left := max(t.Capacity-t.Taken, 0)
	attrs := map[string]string{
		"type_id":  strconv.FormatInt(t.ID, 10),
		"event_id": t.EventID,
		"price":    "Free",
		"left":     strconv.Itoa(left),
		"sold_out": "",
		"waitlist": "",
	}
	if left == 0 {
		attrs["sold_out"] = "1"
		if t.Waitlist {
			attrs["waitlist"] = "1"
		}
	}
	return sdk.BuilderDataItem{Title: capString(t.Name), Attrs: attrs}
}

// capString shortens s to at most sdk.MaxBuilderDataString bytes on a rune
// boundary, replacing invalid UTF-8.
func capString(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= sdk.MaxBuilderDataString {
		return s
	}
	s = s[:sdk.MaxBuilderDataString]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
