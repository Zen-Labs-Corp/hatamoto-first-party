package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"hatamoto.dev/sdk"
)

// maxSplitRules caps the payees of one event.
const maxSplitRules = 10

// maxFixedSplit caps a fixed share per ticket, in minor units.
const maxFixedSplit = 99999999

// splitRule is one payee's share of an event's ticket money. A percent rule
// holds basis points (1..10000); a fixed rule holds minor units per ticket.
type splitRule struct {
	ID    int64  `json:"id,omitempty"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Kind  string `json:"kind"`
	Value int64  `json:"value"`
}

// validSplits checks and cleans a whole rule list: printable names, plain
// payout addresses (no address twice in one event) and percent shares that
// add up to at most 100 %.
func validSplits(in []splitRule) ([]splitRule, bool) {
	out := make([]splitRule, len(in))
	seen := map[string]bool{}
	var percent int64
	for i, x := range in {
		name, ok := cleanText(x.Name, 80)
		email := strings.TrimSpace(x.Email)
		if !ok || len(email) > maxEmailLen || !plainAddress(email) || seen[strings.ToLower(email)] {
			return nil, false
		}
		seen[strings.ToLower(email)] = true
		switch x.Kind {
		case "percent":
			if x.Value < 1 || x.Value > 10000 {
				return nil, false
			}
			percent += x.Value
		case "fixed":
			if x.Value < 1 || x.Value > maxFixedSplit {
				return nil, false
			}
		default:
			return nil, false
		}
		out[i] = splitRule{Name: name, Email: email, Kind: x.Kind, Value: x.Value}
	}
	return out, percent <= 10000
}

// listSplits reads an event's split rules in the order the owner set them.
func listSplits(eventID string) ([]splitRule, error) {
	out := []splitRule{}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.position, r.id), '[]'::jsonb) FROM (
SELECT id, payee_name AS name, payee_email AS email, kind, value, position FROM split_rules WHERE event_id = $1) r`, &out, eventID)
	return out, err
}

// serveSaveSplits answers POST /admin/events/{id}/splits with the event's
// whole rule list, and answers the saved rules. Owner only: the rules decide
// who is paid and hold personal addresses.
func serveSaveSplits(r sdk.HTTPRequest, eventID string) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	if !idOK(eventID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var b struct {
		Rules []splitRule `json:"rules"`
	}
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	if len(b.Rules) > maxSplitRules {
		return failReply(http.StatusBadRequest, "at most 10 split rules")
	}
	rules, ok := validSplits(b.Rules)
	if !ok {
		return failReply(http.StatusBadRequest, "bad request")
	}
	type row struct {
		splitRule
		Pos int `json:"pos"`
	}
	rows := make([]row, len(rules))
	for i, x := range rules {
		rows[i] = row{x, i}
	}
	if _, found, err := readEvent(eventID, loadSettings()); err != nil {
		return unavailable()
	} else if !found {
		return failReply(http.StatusNotFound, "not found")
	}
	doc, _ := json.Marshal(rows)
	err := dbExec(`WITH d AS (DELETE FROM split_rules WHERE event_id = $1)
INSERT INTO split_rules (event_id, position, payee_name, payee_email, kind, value)
SELECT $1, x.pos, x.name, x.email, x.kind, x.value
FROM jsonb_to_recordset($2::jsonb) AS x(name text, email text, kind text, value bigint, pos int)`, eventID, string(doc))
	if err != nil {
		return unavailable()
	}
	saved, err := listSplits(eventID)
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "splits": saved})
}
