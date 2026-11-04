package main

import (
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// instagramMaxAge is how long cached Instagram rows are served after the last
// successful refresh: their image addresses are signed and expire after days.
const instagramMaxAge = 3 * 24 * time.Hour

// maxFieldBytes is the host's limit for one string of a data item.
const maxFieldBytes = 2048

// filterData answers the builder.data requests for this plugin's two sources
// from the cached rows. It reads the database only (the host gives a filter a
// fraction of a second), and any failure returns the request unchanged so the
// page renders without the data.
func filterData(req sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
	s := loadSettings()
	out := make([]sdk.BuilderDataResult, 0, len(req.Requests))
	for _, q := range req.Requests {
		res := sdk.BuilderDataResult{ID: q.ID, Items: []sdk.BuilderDataItem{}}
		name := q.Source[strings.LastIndex(q.Source, ".")+1:]
		if name == "instagram" || name == "youtube" {
			limit := min(s.Items, maxItems)
			if q.Limit > 0 {
				limit = min(limit, q.Limit)
			}
			rows, err := listItems(name, limit)
			if err != nil {
				logf("warn", "builder.data: %s", redact(err.Error()))
				return req, nil
			}
			if name == "instagram" && instagramStale() {
				rows = nil
			}
			for _, it := range rows {
				res.Items = append(res.Items, dataItem(it))
			}
		}
		out = append(out, res)
	}
	req.Results = append(req.Results, out...)
	return req, nil
}

// instagramStale tells whether the last successful Instagram refresh is too old
// for its cached image addresses to still work. Unknown counts as fresh.
func instagramStale() bool {
	st, err := getStatus("instagram")
	if err != nil {
		return false
	}
	at, err := time.Parse(time.RFC3339, st.RefreshedAt)
	return err == nil && now().Sub(at) > instagramMaxAge
}

func dataItem(it item) sdk.BuilderDataItem {
	return sdk.BuilderDataItem{
		Title:       truncate(it.Title, maxFieldBytes),
		Excerpt:     truncate(it.Excerpt, maxFieldBytes),
		URL:         truncate(it.URL, maxFieldBytes),
		Image:       truncate(it.Image, maxFieldBytes),
		PublishedAt: it.PublishedAt,
		Attrs:       map[string]string{"id": truncate(it.ID, maxFieldBytes), "type": it.Type, "source": it.Source},
	}
}
