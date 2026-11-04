package main

import (
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"
)

const (
	pageSize   = 50
	seriesDays = 30
	maxOffset  = 1_000_000
)

type fileRow struct {
	ID        string  `json:"id"`
	Filename  string  `json:"filename"`
	Size      int64   `json:"size"`
	SizeLabel string  `json:"size_label"`
	MimeType  string  `json:"mime_type"`
	Total     int64   `json:"total"`
	Last30    int64   `json:"last30"`
	Series    []int64 `json:"series"`
}

type overview struct {
	Totals struct {
		All    int64 `json:"all"`
		Last30 int64 `json:"last30"`
	} `json:"totals"`
	Files  []fileRow `json:"files"`
	Offset int       `json:"offset"`
	Limit  int       `json:"limit"`
	Total  int       `json:"total"`
}

// qOverview answers everything the page needs in one document: the site-wide
// totals and, for the ids in $1 (comma separated), each file's all-time total
// and its per-day counts between $2 and $3 (UTC days, inclusive).
const qOverview = `SELECT jsonb_build_object(
  'all', (SELECT coalesce(sum(count), 0) FROM (SELECT count FROM downloads_daily UNION ALL SELECT count FROM downloads_monthly) u),
  'last30', (SELECT coalesce(sum(count), 0) FROM downloads_daily WHERE day >= $2::date AND day <= $3::date),
  'files', (SELECT coalesce(jsonb_object_agg(f.id, jsonb_build_object('total', f.total, 'days', f.days)), '{}'::jsonb) FROM (
    SELECT i.id::text AS id,
      (SELECT coalesce(sum(c.count), 0) FROM (SELECT count FROM downloads_daily d WHERE d.media_id = i.id UNION ALL SELECT count FROM downloads_monthly m WHERE m.media_id = i.id) c) AS total,
      (SELECT coalesce(jsonb_object_agg(to_char(d.day, 'YYYY-MM-DD'), d.count), '{}'::jsonb) FROM downloads_daily d WHERE d.media_id = i.id AND d.day >= $2::date AND d.day <= $3::date) AS days
    FROM (SELECT unnest(string_to_array($1, ',')::uuid[]) AS id) i) f))`

type overviewDoc struct {
	All    int64 `json:"all"`
	Last30 int64 `json:"last30"`
	Files  map[string]struct {
		Total int64            `json:"total"`
		Days  map[string]int64 `json:"days"`
	} `json:"files"`
}

// parseOffset reads the offset query value; anything unusable means 0.
func parseOffset(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return min(n, maxOffset)
}

func buildOverview(offset int) (overview, error) {
	var page struct {
		Items []media `json:"items"`
		Total int     `json:"total"`
	}
	if err := callHost("media.list", map[string]any{"limit": pageSize, "offset": offset}, &page); err != nil {
		return overview{}, err
	}
	ids := make([]string, 0, len(page.Items))
	for _, m := range page.Items {
		if isUUID(m.ID) {
			ids = append(ids, strings.ToLower(m.ID))
		}
	}
	today := now().UTC()
	days := make([]string, seriesDays) // oldest first, ending today
	for i := range days {
		days[i] = today.AddDate(0, 0, i-seriesDays+1).Format(dateFmt)
	}
	var doc overviewDoc
	if err := dbJSON(qOverview, &doc, strings.Join(ids, ","), days[0], days[seriesDays-1]); err != nil {
		return overview{}, err
	}

	o := overview{Files: []fileRow{}, Offset: offset, Limit: pageSize, Total: page.Total}
	o.Totals.All, o.Totals.Last30 = doc.All, doc.Last30
	for _, m := range page.Items {
		id := strings.ToLower(m.ID)
		f := fileRow{ID: id, Filename: m.Filename, Size: m.FileSize, SizeLabel: sizeLabel(m.FileSize), MimeType: m.MimeType, Series: make([]int64, seriesDays)}
		if d, ok := doc.Files[id]; ok {
			f.Total = d.Total
			for i, day := range days {
				f.Series[i] = d.Days[day]
				f.Last30 += d.Days[day]
			}
		}
		o.Files = append(o.Files, f)
	}
	return o, nil
}

func handleOverview(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	o, err := buildOverview(parseOffset(first(r.Query["offset"])))
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
	}
	return reply(http.StatusOK, o)
}
