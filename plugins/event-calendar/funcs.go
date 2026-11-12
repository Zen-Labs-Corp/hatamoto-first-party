package main

import (
	"encoding/json"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() { guest.OnFunc("addlink", addlinkFunc) }

// addlinkFunc is the template func `addlink provider entity_id`; provider is
// google, outlook, yahoo or ics. Anything wrong renders nothing, never an error.
func addlinkFunc(args []json.RawMessage) (string, error) {
	if len(args) < 2 {
		return "", nil
	}
	provider, id := jsonString(args[0]), jsonString(args[1])
	if id == "" {
		return "", nil
	}
	s := loadSettings()
	var raw entityDTO
	if err := callHost(sdk.FnEntitiesGet, map[string]any{"id": id}, &raw); err != nil || !s.accepts(raw.SchemaSlug) {
		return "", nil
	}
	e, ok := eventFromEntity(raw, s)
	if !ok {
		return "", nil
	}
	name, address, city, _ := venueOrText(e)
	where := joinNonEmpty(name, address, city)
	links := calendarLinks(e, s.location(), where, e.Description)
	switch provider {
	case "google":
		return links.Google, nil
	case "outlook":
		return links.Outlook, nil
	case "yahoo":
		return links.Yahoo, nil
	case "ics":
		return links.ICS, nil
	}
	return "", nil
}
