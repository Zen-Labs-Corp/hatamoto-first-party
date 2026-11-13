package main

import (
	"encoding/json"
	"strings"

	"hatamoto.dev/sdk"
)

// Venue is a resolved venue entity.
type Venue struct {
	ID, Slug, Name, URL, Address, City, Region, Postcode, Country, Phone, Website string
	Lat, Lon                                                                      *float64
}

// Organizer is a resolved organizer entity.
type Organizer struct {
	ID, Name, URL, Email, Phone, Website string
}

// Series is a resolved series entity.
type Series struct {
	ID, Name, URL string
}

// loadRef reads one published entity by id and its attributes. It is false
// for an empty id (no host call), a host error or an entity that is not
// published.
func loadRef(id string) (entityDTO, map[string]any, bool) {
	if id == "" {
		return entityDTO{}, nil, false
	}
	var raw entityDTO
	if err := callHost(sdk.FnEntitiesGet, map[string]any{"id": id}, &raw); err != nil || raw.Status != "published" {
		return entityDTO{}, nil, false
	}
	var attrs map[string]any
	if len(raw.Attributes) > 0 {
		_ = json.Unmarshal(raw.Attributes, &attrs)
	}
	return raw, attrs, true
}

func loadVenue(id string) (Venue, bool) {
	raw, a, ok := loadRef(id)
	if !ok {
		return Venue{}, false
	}
	return Venue{
		ID: id, Slug: raw.Slug, Name: raw.Name, URL: raw.URL,
		Address: attrString(a, "address"), City: attrString(a, "city"), Region: attrString(a, "region"),
		Postcode: attrString(a, "postcode"), Country: attrString(a, "country"),
		Phone: attrString(a, "phone"), Website: attrString(a, "website"),
		Lat: attrFloat(a, "latitude"), Lon: attrFloat(a, "longitude"),
	}, true
}

// venueMemo remembers the venues one list or feed request has read, a venue
// that could not be read included, so events at the same venue cost one host
// call. Make one per request.
type venueMemo map[string]*Venue

func (m venueMemo) get(id string) (Venue, bool) {
	v, seen := m[id]
	if !seen {
		if r, ok := loadVenue(id); ok {
			v = &r
		}
		m[id] = v
	}
	if v == nil {
		return Venue{}, false
	}
	return *v, true
}

func loadOrganizer(id string) (Organizer, bool) {
	raw, a, ok := loadRef(id)
	if !ok {
		return Organizer{}, false
	}
	return Organizer{
		ID: id, Name: raw.Name, URL: raw.URL,
		Email: attrString(a, "email"), Phone: attrString(a, "phone"), Website: attrString(a, "website"),
	}, true
}

func loadSeries(id string) (Series, bool) {
	raw, _, ok := loadRef(id)
	if !ok {
		return Series{}, false
	}
	return Series{ID: id, Name: raw.Name, URL: raw.URL}, true
}

// joinNonEmpty joins the non-empty parts with ", ".
func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// line is "Name, Address, City".
func (v Venue) line() string { return joinNonEmpty(v.Name, v.Address, v.City) }

// venueOrText is the event's venue: the referenced venue when it resolves,
// else the plain text fields of the event (how a v0.1 event holds it).
func venueOrText(e Event) (name, address, city string, v *Venue) {
	if venue, ok := loadVenue(e.VenueID); ok {
		return venue.Name, venue.Address, venue.City, &venue
	}
	return e.Venue, e.Address, e.VenueCity, nil
}
