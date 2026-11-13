package main

import (
	"slices"
	"strings"
)

// maxFilterBytes caps every filter value, as the host caps a text param.
const maxFilterBytes = 200

// filters narrow a list of events. An empty field matches everything. The
// builder params and the public route's query both reduce to this type.
type filters struct {
	Q, Category, Venue, City string
	Price                    string // "", "free" or "paid"
}

func (f filters) empty() bool { return f == filters{} }

// filtersFromParams reads the params of an upcoming or past source: text
// params category, venue and city, and the bool free. The host sanitises them
// already; values of another type are ignored.
func filtersFromParams(p map[string]any) filters {
	text := func(k string) string {
		s, _ := p[k].(string)
		return capBytes(strings.TrimSpace(s), maxFilterBytes)
	}
	f := filters{Category: text("category"), Venue: text("venue"), City: text("city")}
	if free, _ := p["free"].(bool); free {
		f.Price = "free"
	}
	return f
}

// queryKeys are the only keys the public events route accepts.
var queryKeys = []string{"from", "to", "q", "category", "venue", "city", "price", "view", "limit", "slug", "upcoming"}

var queryViews = []string{"", "month", "list", "week", "day", "photo", "summary", "map"}

// filtersFromQuery checks the shape of a route query and returns its filter
// fields. It is false for an unknown key, a key given twice or not at all,
// a value over the cap, a price that is not any, free or paid, or an unknown
// view. from, to, limit and slug are only checked for shape here.
func filtersFromQuery(q map[string][]string) (filters, bool) {
	val := map[string]string{}
	for k, vs := range q {
		if !slices.Contains(queryKeys, k) || len(vs) != 1 || len(vs[0]) > maxFilterBytes {
			return filters{}, false
		}
		val[k] = strings.TrimSpace(vs[0])
	}
	if !slices.Contains(queryViews, val["view"]) {
		return filters{}, false
	}
	if val["upcoming"] != "" && val["upcoming"] != "1" {
		return filters{}, false
	}
	f := filters{Q: val["q"], Category: val["category"], Venue: val["venue"], City: val["city"]}
	switch val["price"] {
	case "", "any":
	case "free", "paid":
		f.Price = val["price"]
	default:
		return filters{}, false
	}
	return f, true
}

// isFree is the free rule of the index query and priceText: an amount of zero
// or the text "free".
func isFree(e Event) bool {
	return (e.PriceAmount != nil && *e.PriceAmount == 0) || strings.EqualFold(strings.TrimSpace(e.Price), "free")
}

// isPaid is not free but has a price.
func isPaid(e Event) bool {
	return !isFree(e) && ((e.PriceAmount != nil && *e.PriceAmount > 0) || strings.TrimSpace(e.Price) != "")
}

// match is the index query's filtering for an event read from the entities.
// Such an event holds category ids and the venue's text only, so cats are the
// names and slugs of its categories (the caller resolves them when a category
// filter is set) and the venue is matched by name.
func (f filters) match(e Event, cats categoryRefs) bool {
	eq := strings.EqualFold
	if f.Q != "" && !containsFold(e.Name, f.Q) && !containsFold(e.Venue, f.Q) {
		return false
	}
	if f.Category != "" && !slices.ContainsFunc(append(slices.Clone(cats.Names), cats.Slugs...), func(c string) bool { return eq(c, f.Category) }) {
		return false
	}
	if f.Venue != "" && !eq(e.Venue, f.Venue) {
		return false
	}
	if f.City != "" && !eq(e.VenueCity, f.City) {
		return false
	}
	switch f.Price {
	case "free":
		return isFree(e)
	case "paid":
		return isPaid(e)
	}
	return true
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
