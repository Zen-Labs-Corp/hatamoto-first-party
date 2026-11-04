package main

import (
	"strings"

	"hatamoto.dev/sdk"
)

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
			for _, it := range rows {
				res.Items = append(res.Items, dataItem(it))
			}
		}
		out = append(out, res)
	}
	req.Results = append(req.Results, out...)
	return req, nil
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
