package main

import (
	"errors"
	"net/http"
	"strings"

	"hatamoto.dev/sdk"
)

// The admin routes of the Seating tab. Like the Events routes they are open
// to the owner and editor roles (a map moves no money), and every handler
// still refuses a request without a user.

const maxBlockSeats = 500

// sectionBody is the body of POST /admin/maps/{id}/sections: add a section
// (id 0) or change one. Rows is the text the owner typed, "A-J" or "K,L,M";
// a zone has no rows and a capacity instead.
type sectionBody struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Tier        string `json:"tier"`
	Rows        string `json:"rows"`
	SeatsPerRow int    `json:"seats_per_row"`
	Capacity    int    `json:"capacity"`
	Position    int    `json:"position"`
}

// seatingFailure answers a refusal of a seating statement with a fixed text.
// inUse is the text for errInUse, which depends on what was being done.
func seatingFailure(err error, inUse string) sdk.HTTPResponse {
	switch {
	case errors.Is(err, errInUse):
		return failReply(http.StatusConflict, inUse)
	case errors.Is(err, errTooMany):
		return failReply(http.StatusConflict, "Too many maps, sections or seats")
	case errors.Is(err, errNotFound):
		return failReply(http.StatusNotFound, "not found")
	}
	return unavailable()
}

// serveMaps answers GET /admin/maps with every map and its sections.
func serveMaps(u *sdk.HTTPUser) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	maps, err := listMaps()
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"maps": maps})
}

// serveMap answers GET /admin/maps/{id}: the map and the seats of each of its
// sections, in the order of "map.sections".
func serveMap(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	m, seats, err := readMap(id)
	if err != nil {
		return seatingFailure(err, "")
	}
	return jsonReply(http.StatusOK, map[string]any{"map": m, "seats": seats})
}

// serveSaveMap answers POST /admin/maps: add a map, or rename the one named by id.
func serveSaveMap(r sdk.HTTPRequest) sdk.HTTPResponse {
	if r.User == nil {
		return forbidden()
	}
	var b struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	name, ok := cleanText(b.Name, maxNameRunes)
	if !ok || b.ID < 0 {
		return failReply(http.StatusBadRequest, "bad request")
	}
	id, err := saveMap(b.ID, name)
	if err != nil {
		return seatingFailure(err, "")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "id": id})
}

// serveDeleteMap answers POST /admin/maps/{id}/delete.
func serveDeleteMap(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	if err := deleteMap(id); err != nil {
		return seatingFailure(err, "In use by ticket types")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true})
}

// serveSaveSection answers POST /admin/maps/{id}/sections.
func serveSaveSection(r sdk.HTTPRequest, rawMapID string) sdk.HTTPResponse {
	if r.User == nil {
		return forbidden()
	}
	mapID, ok := parseID(rawMapID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var b sectionBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	s := section{ID: b.ID, MapID: mapID, Kind: b.Kind, Name: strings.TrimSpace(b.Name), Tier: strings.TrimSpace(b.Tier),
		Position: b.Position, SeatsPerRow: b.SeatsPerRow, Capacity: b.Capacity}
	if b.ID < 0 {
		return failReply(http.StatusBadRequest, "bad request")
	}
	if s.Kind == kindGrid {
		if s.Rows, ok = parseRows(b.Rows); !ok {
			return failReply(http.StatusBadRequest, "bad request")
		}
	} else if strings.TrimSpace(b.Rows) != "" {
		return failReply(http.StatusBadRequest, "bad request")
	}
	if validSection(s) != nil {
		return failReply(http.StatusBadRequest, "bad request")
	}
	id, err := saveSection(s)
	if err != nil {
		return seatingFailure(err, "This section has sold seats; add a new section instead")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "id": id})
}

// serveDeleteSection answers POST /admin/sections/{id}/delete.
func serveDeleteSection(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	if err := deleteSection(id); err != nil {
		return seatingFailure(err, "In use by ticket types")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true})
}

// serveSetBlocked answers POST /admin/sections/{id}/seats: block or unblock
// up to 500 seats of a grid section.
func serveSetBlocked(r sdk.HTTPRequest, rawID string) sdk.HTTPResponse {
	if r.User == nil {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var b struct {
		Seats   []int64 `json:"seats"`
		Blocked bool    `json:"blocked"`
	}
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	if len(b.Seats) == 0 || len(b.Seats) > maxBlockSeats {
		return failReply(http.StatusBadRequest, "bad request")
	}
	for _, s := range b.Seats {
		if s <= 0 {
			return failReply(http.StatusBadRequest, "bad request")
		}
	}
	n, err := setBlocked(id, b.Seats, b.Blocked)
	if err != nil {
		return seatingFailure(err, "Those seats are held or sold, or no free seat would be left")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "changed": n})
}

// mapChoice is a map as the type rows of the event editor offer it.
type mapChoice struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Sections []sectionChoice `json:"sections"`
}

type sectionChoice struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Tier string `json:"tier"`
}

// mapChoices lists the maps with just what a type row needs to pick a section.
func mapChoices() ([]mapChoice, error) {
	maps, err := listMaps()
	if err != nil {
		return nil, err
	}
	out := make([]mapChoice, 0, len(maps))
	for _, m := range maps {
		c := mapChoice{ID: m.ID, Name: m.Name, Sections: []sectionChoice{}}
		for _, s := range m.Sections {
			c.Sections = append(c.Sections, sectionChoice{s.ID, s.Name, s.Kind, s.Tier})
		}
		out = append(out, c)
	}
	return out, nil
}
