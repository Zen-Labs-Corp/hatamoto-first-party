package main

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// maxFilterBytes caps every filter value, as the host caps a text param.
const maxFilterBytes = 200

// filters narrow a list of events. An empty field matches everything. The
// builder params and the public route's query both reduce to this type.
type filters struct {
	Q, Category, Venue, City, Tag string
	Price                         string // "", "free" or "paid"
	Fields                        []fieldFilter
}

// fieldFilter narrows to events whose site-added field Key equals Value (or
// lists it).
type fieldFilter struct{ Key, Value string }

func (f filters) empty() bool {
	return f.Q == "" && f.Category == "" && f.Venue == "" && f.City == "" && f.Tag == "" && f.Price == "" && len(f.Fields) == 0
}

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
var queryKeys = []string{"from", "to", "q", "category", "venue", "city", "price", "view", "limit", "slug", "upcoming", "tag", "fields", "f1", "f2", "f3", "f4"}

var fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// maxFieldFilters is how many site-added fields one query can filter on.
const maxFieldFilters = 4

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
	f := filters{Q: val["q"], Category: val["category"], Venue: val["venue"], City: val["city"], Tag: val["tag"]}
	keys, ok := requestedFields(q)
	if !ok {
		return filters{}, false
	}
	for i := 1; i <= maxFieldFilters; i++ {
		v := val["f"+strconv.Itoa(i)]
		if v == "" {
			continue
		}
		if i > len(keys) {
			return filters{}, false
		}
		f.Fields = append(f.Fields, fieldFilter{Key: keys[i-1], Value: v})
	}
	switch val["price"] {
	case "", "any":
	case "free", "paid":
		f.Price = val["price"]
	default:
		return filters{}, false
	}
	return f, true
}

// requestedFields reads the fields param: 1 to 4 distinct field keys, comma
// separated, which the slots f1..f4 refer to in order. No param is no keys; it
// is false for a bad list.
func requestedFields(q map[string][]string) ([]string, bool) {
	vs := q["fields"]
	if len(vs) == 0 {
		return nil, true
	}
	if len(vs) != 1 {
		return nil, false
	}
	keys := strings.Split(strings.TrimSpace(vs[0]), ",")
	if len(keys) > maxFieldFilters {
		return nil, false
	}
	for i, k := range keys {
		if !fieldKeyRe.MatchString(k) || slices.Contains(keys[:i], k) {
			return nil, false
		}
	}
	return keys, true
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
	if f.Tag != "" && !slices.ContainsFunc(e.Tags, func(t string) bool { return eq(t, f.Tag) }) {
		return false
	}
	for _, ff := range f.Fields {
		if !fieldHas(e.Extra[ff.Key], ff.Value) {
			return false
		}
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

// fieldHas is true when a site-added field value (a string or a list of
// strings) equals want, ignoring case.
func fieldHas(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return strings.EqualFold(x, want)
	case []string:
		return slices.ContainsFunc(x, func(s string) bool { return strings.EqualFold(s, want) })
	case []any:
		return slices.ContainsFunc(x, func(a any) bool { s, _ := a.(string); return strings.EqualFold(s, want) })
	}
	return false
}
