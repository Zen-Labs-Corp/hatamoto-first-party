package main

import "hatamoto.dev/sdk"

const overviewLimit = 200

// qOverview lists rated entities, most rated first. The path is whatever a
// visitor last posted for the entity, so the admin page treats it as text.
const qOverview = `SELECT v.entity_id::text, COALESCE(p.path, ''), round(avg(v.stars)::numeric, 1)::text, count(*),
  count(*) FILTER (WHERE v.stars = 5), count(*) FILTER (WHERE v.stars = 4), count(*) FILTER (WHERE v.stars = 3),
  count(*) FILTER (WHERE v.stars = 2), count(*) FILTER (WHERE v.stars = 1)
FROM votes v LEFT JOIN pages p ON p.entity_id = v.entity_id
GROUP BY v.entity_id, p.path
ORDER BY count(*) DESC, v.entity_id
LIMIT 200`

type pageRow struct {
	EntityID string `json:"entity_id"`
	Path     string `json:"path"`
	Average  string `json:"average"`
	Count    int    `json:"count"`
	Dist     [5]int `json:"dist"` // votes for 5, 4, 3, 2, 1 stars
}

// handleOverview answers the admin page. A database error is an empty list.
func handleOverview() sdk.HTTPResponse {
	pages := []pageRow{}
	rows, err := dbRows(qOverview)
	if err == nil {
		for _, r := range rows {
			if len(r) < 9 {
				continue
			}
			p := pageRow{EntityID: cellString(r[0]), Path: cellString(r[1]), Average: cellString(r[2]), Count: cellInt(r[3])}
			for i := range p.Dist {
				p.Dist[i] = cellInt(r[4+i])
			}
			pages = append(pages, p)
		}
	}
	return reply(200, map[string]any{"pages": pages})
}
