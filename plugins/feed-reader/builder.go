package main

import (
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnFilter(sdk.BuilderData, handleBuilder)
}

// handleBuilder answers the page-builder requests naming this plugin's
// sources from the stored items. It only reads the database and never returns
// an error: a database problem or an unknown source gives an empty result, so
// a page always renders.
func handleBuilder(req sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
	req.Results = req.Results[:0:0]
	for _, q := range req.Requests {
		res := sdk.BuilderDataResult{ID: q.ID}
		name := q.Source[strings.LastIndex(q.Source, ".")+1:]
		rows, err := itemsFor(name, q.Limit)
		if err != nil {
			logf("error", "feed-reader: cannot read the items")
		}
		for _, r := range rows {
			if it, ok := builderItem(r); ok {
				res.Items = append(res.Items, it)
			}
		}
		req.Results = append(req.Results, res)
	}
	return req, nil
}

// builderItem converts a stored item. An item without a usable link is
// skipped; every string is clipped to what the host accepts.
func builderItem(r itemRow) (sdk.BuilderDataItem, bool) {
	if r.Link == "" || len(r.Link) >= sdk.MaxBuilderDataString {
		return sdk.BuilderDataItem{}, false
	}
	site := r.FeedSiteURL
	if site == "" || len(site) >= sdk.MaxBuilderDataString {
		site = r.FeedURL
	}
	attrs := map[string]string{"source": clip(r.FeedName)}
	if len(site) < sdk.MaxBuilderDataString {
		attrs["source_url"] = site
	}
	it := sdk.BuilderDataItem{
		Title:    clip(r.Title),
		URL:      r.Link,
		Excerpt:  clip(r.Excerpt),
		Category: clip(r.FeedName),
		Attrs:    attrs,
	}
	if !r.Published.IsZero() {
		it.PublishedAt = r.Published.UTC().Format(time.RFC3339)
	}
	return it, true
}

// clip cuts s to the host's string limit at a rune boundary and makes sure it
// is valid UTF-8.
func clip(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) < sdk.MaxBuilderDataString {
		return s
	}
	n := sdk.MaxBuilderDataString - 1
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
