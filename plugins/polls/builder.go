package main

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

const (
	builderSource = "ext.polls.results"
	maxItems      = 48
	maxText       = 2048
)

// cut shortens s to valid UTF-8 of at most maxText bytes, on a rune boundary.
func cut(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= maxText {
		return s
	}
	n := maxText
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// pollItems returns one item per option of the first form whose results are
// public right now (the request names no form). No public form: no items.
func pollItems() ([]sdk.BuilderDataItem, error) {
	cfgs, err := loadConfig()
	if err != nil {
		return nil, err
	}
	at := now()
	i := slices.IndexFunc(cfgs, func(c formCfg) bool { return c.public(at) })
	if i < 0 {
		return []sdk.BuilderDataItem{}, nil
	}
	forms, err := listForms()
	if err != nil {
		return nil, err
	}
	for _, cfg := range cfgs[i:] {
		form, ok := forms[cfg.Slug]
		if !ok || !cfg.public(at) {
			continue
		}
		res, err := buildResults(cfg, form)
		if err != nil {
			return nil, err
		}
		items := []sdk.BuilderDataItem{}
		for _, q := range res.Questions {
			for _, o := range q.Options {
				items = append(items, sdk.BuilderDataItem{
					Title:    cut(o.Label),
					Category: cut(q.Label),
					Attrs: map[string]string{
						"form":    cut(res.Slug),
						"field":   cut(q.Field),
						"count":   strconv.Itoa(o.Count),
						"percent": strconv.Itoa(o.Percent),
					},
				})
			}
		}
		return items, nil
	}
	return []sdk.BuilderDataItem{}, nil
}

// onBuilderData answers the "Poll results" source. It never fails the page:
// any problem is logged and the request is returned as it came.
func onBuilderData(req sdk.BuilderDataRequest) (sdk.BuilderDataRequest, error) {
	answered := map[string]bool{}
	for _, r := range req.Results {
		answered[r.ID] = true
	}
	var wanted []sdk.BuilderDataQuery
	for _, q := range req.Requests {
		if q.Source == builderSource && !answered[q.ID] {
			wanted = append(wanted, q)
		}
	}
	if len(wanted) == 0 {
		return req, nil
	}
	items, err := pollItems()
	if err != nil {
		_ = logf("warn", "polls: builder source skipped: %v", err)
		return req, nil
	}
	results := slices.Clone(req.Results)
	for _, q := range wanted {
		limit := maxItems
		if q.Limit > 0 {
			limit = min(q.Limit, maxItems)
		}
		results = append(results, sdk.BuilderDataResult{ID: q.ID, Items: items[:min(limit, len(items))]})
	}
	req.Results = results
	return req, nil
}
