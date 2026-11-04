package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// qTotals sums daily and monthly rows per file for a comma-separated id list
// and returns one object {"<id>": total}.
const qTotals = `SELECT coalesce(jsonb_object_agg(media_id::text, n), '{}'::jsonb) FROM (SELECT media_id, sum(count) AS n FROM (SELECT media_id, count FROM downloads_daily UNION ALL SELECT media_id, count FROM downloads_monthly) u WHERE media_id = ANY(string_to_array($1, ',')::uuid[]) GROUP BY media_id) t`

// totals returns the all-time count per file id. Ids must be valid UUIDs; a
// file without rows is absent from the map.
func totals(ids []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	err := dbJSON(qTotals, &out, strings.Join(ids, ","))
	return out, err
}

// funcCount is the template func plugin_downloads_count: the all-time total as
// plain text. Every failure prints 0, never an error.
func funcCount(args []json.RawMessage) (string, error) {
	var id string
	if len(args) != 1 || json.Unmarshal(args[0], &id) != nil {
		return "0", nil
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if !isUUID(id) {
		return "0", nil
	}
	t, err := totals([]string{id})
	if err != nil {
		return "0", nil
	}
	return strconv.FormatInt(t[id], 10), nil
}
