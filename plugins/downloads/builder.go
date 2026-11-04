package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

const (
	filesSource = "ext.downloads.files"
	builderFail = "downloads unavailable"
)

// trim cuts s to the host's string cap on a rune boundary.
func trim(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= sdk.MaxBuilderDataString {
		return s
	}
	s = s[:sdk.MaxBuilderDataString]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// sizeLabel is a short human size: base 1024, one decimal, ".0" dropped.
func sizeLabel(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(max(n, 0), 10) + " B"
	}
	v, unit := float64(n), "KB"
	for _, u := range []string{"KB", "MB", "GB"} {
		v, unit = v/1024, u
		if v < 1024 {
			break
		}
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + " " + unit
}

func clampLimit(n int) int {
	return min(max(n, 1), sdk.MaxBuilderDataItems)
}

// loadFiles reads one page of files with their totals: one media.list call and
// one aggregate query.
func loadFiles(limit int) ([]media, map[string]int64, error) {
	var res struct {
		Items []media `json:"items"`
	}
	if err := callHost("media.list", map[string]any{"limit": limit}, &res); err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(res.Items))
	for _, m := range res.Items {
		if isUUID(m.ID) {
			ids = append(ids, strings.ToLower(m.ID))
		}
	}
	t, err := totals(ids)
	return res.Items, t, err
}

func fileItem(m media, count int64) sdk.BuilderDataItem {
	return sdk.BuilderDataItem{
		Title: trim(m.Filename),
		URL:   "/p/downloads/f/" + strings.ToLower(m.ID),
		Attrs: map[string]string{
			"count":      strconv.FormatInt(count, 10),
			"size":       strconv.FormatInt(m.FileSize, 10),
			"size_label": sizeLabel(m.FileSize),
			"mime_type":  trim(m.MimeType),
		},
	}
}

// onBuilderData answers only the files source. A host error gives an empty
// result with a generic message, so a page never fails because of this plugin.
func onBuilderData(c sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
	var mine []sdk.BuilderDataQuery
	most := 0
	for _, q := range c.Requests {
		if q.Source == filesSource {
			mine = append(mine, q)
			most = max(most, clampLimit(q.Limit))
		}
	}
	if len(mine) == 0 {
		return c, nil
	}
	files, counts, err := loadFiles(most)
	for _, q := range mine {
		if err != nil {
			c.Results = append(c.Results, sdk.BuilderDataResult{ID: q.ID, Error: builderFail})
			continue
		}
		items := []sdk.BuilderDataItem{}
		for _, m := range files[:min(len(files), clampLimit(q.Limit))] {
			items = append(items, fileItem(m, counts[strings.ToLower(m.ID)]))
		}
		c.Results = append(c.Results, sdk.BuilderDataResult{ID: q.ID, Items: items})
	}
	return c, nil
}
