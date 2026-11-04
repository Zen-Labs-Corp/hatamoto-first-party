package main

import (
	"strings"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// maxString is the host's byte limit for a builder string (exclusive).
const maxString = 2048

func init() {
	guest.OnFilter(sdk.BuilderData, handleBuilder)
}

// clip cuts s below the host's 2048-byte limit at a rune boundary; one string
// over the limit would drop the whole result.
func clip(s string) string {
	if len(s) < maxString {
		return strings.ToValidUTF8(s, "")
	}
	s = s[:maxString-1]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// shortURL keeps a URL only when it fits the limit; a cut URL would be wrong.
func shortURL(s string) string {
	if len(s) >= maxString {
		return ""
	}
	return s
}

// handleBuilder answers ext.reviews.latest. It never fails the page: an
// unknown source or any error is an empty result without an error string.
func handleBuilder(req sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
	for _, q := range req.Requests {
		res := sdk.BuilderDataResult{ID: q.ID}
		if q.Source[strings.LastIndex(q.Source, ".")+1:] == "latest" {
			res.Items = latestItems(q.Limit)
		}
		req.Results = append(req.Results, res)
	}
	return req, nil
}

func latestItems(limit int) []sdk.BuilderDataItem {
	snap := current()
	rs := snap.Reviews
	if limit > 0 && limit < len(rs) {
		rs = rs[:limit]
	}
	if len(rs) > maxItemsCap {
		rs = rs[:maxItemsCap]
	}
	items := make([]sdk.BuilderDataItem, 0, len(rs))
	for _, r := range rs {
		link := r.Link
		if link == "" {
			link = snap.Sources[r.Source].ProfileURL
		}
		date := r.Published.UTC().Format(dateLayout)
		items = append(items, sdk.BuilderDataItem{
			Title:       clip(r.Author),
			Excerpt:     clip(r.Body),
			URL:         shortURL(link),
			Image:       shortURL(r.PhotoURL),
			Category:    sourceLabels[r.Source],
			Author:      clip(r.Author),
			AuthorURL:   shortURL(r.AuthorURL),
			PublishedAt: r.Published.UTC().Format("2006-01-02T15:04:05Z07:00"),
			Attrs: map[string]string{
				"rating": itoa(r.Rating),
				"date":   date,
				"source": r.Source,
				"url":    shortURL(link),
			},
		})
	}
	return items
}
