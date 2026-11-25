package main

import (
	"strconv"
	"time"
)

// segment says which people a campaign goes to. Every filter becomes a bound
// parameter in where(); no value of it is ever put into SQL text, and the count
// shown to the owner and the fan-out use the same condition.
type segment struct {
	EventID      string `json:"event_id"`
	TypeID       int64  `json:"type_id"`
	CheckedIn    string `json:"checked_in"`    // "", "yes" or "no"
	BoughtBefore string `json:"bought_before"` // YYYY-MM-DD (UTC) or ""
	RSVPOnly     bool   `json:"rsvp_only"`
	NotBought    bool   `json:"not_bought"`
}

// validate returns what is wrong with the segment, empty when it is fine. That
// a ticket type belongs to the event is checked where the type is looked up.
func (g segment) validate() string {
	switch {
	case g.EventID != "" && !idOK(g.EventID):
		return "bad event"
	case g.TypeID < 0:
		return "bad ticket type"
	case g.CheckedIn != "" && g.CheckedIn != "yes" && g.CheckedIn != "no":
		return "bad checked-in filter"
	case (g.TypeID != 0 || g.CheckedIn != "" || g.NotBought) && g.EventID == "":
		return "choose an event"
	case g.NotBought && (g.TypeID != 0 || g.CheckedIn != "" || g.RSVPOnly):
		return "not-bought cannot be combined with a ticket type, checked-in or RSVP only"
	}
	if g.BoughtBefore != "" {
		if _, err := time.Parse("2006-01-02", g.BoughtBefore); err != nil {
			return "bad date"
		}
	}
	return ""
}

// where is the condition on orders o that selects the people, with its
// arguments numbered from $start. It always carries recipientSQL, so people
// who unsubscribed, anonymised orders and orders without a live ticket never match.
func (g segment) where(start int) (string, []any) {
	args := []any{}
	add := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(start+len(args)-1)
	}
	cond := recipientSQL
	if g.NotBought {
		ev := add(g.EventID)
		cond += ` AND o.event_id <> ` + ev + ` AND NOT EXISTS (SELECT 1 FROM orders o2 JOIN attendees a2 ON a2.order_id = o2.id AND a2.status = 'active'
  WHERE o2.event_id = ` + ev + ` AND lower(o2.email) = lower(o.email))`
	} else {
		if g.EventID != "" {
			cond += ` AND o.event_id = ` + add(g.EventID)
		}
		if g.TypeID != 0 || g.CheckedIn != "" {
			attendee := `EXISTS (SELECT 1 FROM attendees a WHERE a.order_id = o.id AND a.status = 'active'`
			if g.TypeID != 0 {
				attendee += ` AND a.type_id = ` + add(g.TypeID)
			}
			switch g.CheckedIn {
			case "yes":
				attendee += ` AND a.checked_in_at IS NOT NULL`
			case "no":
				attendee += ` AND a.checked_in_at IS NULL`
			}
			cond += ` AND ` + attendee + `)`
		}
		if g.RSVPOnly {
			cond += ` AND o.total_cents = 0`
		}
	}
	if g.BoughtBefore != "" {
		cond += ` AND o.created_at < ` + add(g.BoughtBefore+"T00:00:00Z") + `::timestamptz`
	}
	return cond, args
}

// countSegment is how many people the segment reaches: one per address.
func countSegment(g segment) (int, error) {
	cond, args := g.where(1)
	var n int
	err := dbJSON(`SELECT to_jsonb(count(DISTINCT lower(o.email))) FROM orders o WHERE `+cond, &n, args...)
	return n, err
}
