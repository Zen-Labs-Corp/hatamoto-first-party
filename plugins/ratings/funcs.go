package main

import (
	"encoding/json"
	"fmt"
)

// Template funcs never fail: a failing func makes the host skip the page
// cache, so every problem is an empty text. Nothing depends on the visitor,
// because the page cache is shared.

// funcSummary: "4.3 out of 5 (27 ratings)"; empty without votes or on error.
func funcSummary(args []json.RawMessage) (string, error) {
	if len(args) != 1 {
		return "", nil
	}
	var id string
	if json.Unmarshal(args[0], &id) != nil || !isUUID(id) {
		return "", nil
	}
	rows, err := dbRows(`SELECT count(*), COALESCE(avg(stars), 0) FROM votes WHERE entity_id = $1::uuid`, id)
	if err != nil || len(rows) == 0 || len(rows[0]) < 2 {
		return "", nil
	}
	n := cellInt(rows[0][0])
	if n <= 0 {
		return "", nil
	}
	word := "ratings"
	if n == 1 {
		word = "rating"
	}
	return fmt.Sprintf("%.1f out of 5 (%d %s)", cellFloat(rows[0][1]), n, word), nil
}

// funcEnabled returns "1" when the schema slug may be rated.
func funcEnabled(args []json.RawMessage) (string, error) {
	if len(args) != 1 {
		return "", nil
	}
	var slug string
	if json.Unmarshal(args[0], &slug) != nil || !loadConfig().allows(slug) {
		return "", nil
	}
	return "1", nil
}
