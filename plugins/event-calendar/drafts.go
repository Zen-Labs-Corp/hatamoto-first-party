package main

import (
	"encoding/json"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// Drafts are not in the event index (it holds published events only), so the
// Drafts tab reads them from the host, a bounded number at a time.

const (
	maxDraftScan = 500
	draftBudget  = 2 * time.Second
)

// draftRow is the part of a draft the admin lists read.
type draftRow struct {
	ID, Slug, Name        string
	Start, End            time.Time
	HasStart              bool
	AllDay                bool
	VenueName, VenueID    string
	SourceKind, SourceUID string
}

// scanDrafts lists the drafts of the accepted types, as the host returns them,
// at most maxDraftScan rows and until the clock passes until. capped is true
// when either limit stopped the scan before every draft was read. search is
// passed to the host and applied here again, for a host that ignores it.
func scanDrafts(s settings, search string, until time.Time) (rows []draftRow, capped bool, err error) {
	needle := strings.ToLower(search)
	for si, schema := range s.slugs() {
		cursor, limit := "", listPageSize
		for {
			if !clock().Before(until) {
				return rows, true, nil
			}
			args := map[string]any{"schema_slug": schema, "status": "draft", "limit": limit}
			if search != "" {
				args["search"] = search
			}
			if cursor != "" {
				args["cursor"] = cursor
			}
			var res struct {
				Items      []entityDTO `json:"items"`
				NextCursor string      `json:"next_cursor"`
			}
			err := callHost(sdk.FnEntitiesList, args, &res)
			if isInvalidArgument(err) && limit > smallPageSize {
				// the answer was over the host's size cap: read this page again in smaller pieces
				limit = smallPageSize
				args["limit"] = limit
				err = callHost(sdk.FnEntitiesList, args, &res)
			}
			if err != nil {
				if isNotFound(err) || si > 0 {
					break // the type does not exist, or the older type cannot be read: it has no drafts
				}
				return nil, false, err
			}
			for _, raw := range res.Items {
				if needle != "" && !strings.Contains(strings.ToLower(raw.Name), needle) {
					continue
				}
				if len(rows) == maxDraftScan {
					return rows, true, nil
				}
				rows = append(rows, draftOf(raw, s))
			}
			if res.NextCursor == "" {
				break
			}
			if len(rows) == maxDraftScan {
				return rows, true, nil // the host has more
			}
			cursor = res.NextCursor
		}
	}
	return rows, false, nil
}

// draftOf reads a draft's attributes; an unreadable one is listed without dates.
func draftOf(raw entityDTO, s settings) draftRow {
	d := draftRow{ID: raw.ID, Slug: raw.Slug, Name: raw.Name}
	var attrs map[string]any
	if len(raw.Attributes) == 0 || json.Unmarshal(raw.Attributes, &attrs) != nil {
		return d
	}
	loc := zoneOr(attrString(attrs, "time_zone"), s.location())
	d.Start, d.HasStart = parseTime(attrString(attrs, "start_at"), loc)
	if end, ok := parseTime(attrString(attrs, "end_at"), loc); ok {
		d.End = end
	}
	d.AllDay = attrBool(attrs, "all_day")
	d.VenueName = attrString(attrs, "venue_name")
	if ids := attrIDs(attrs, "venue"); len(ids) > 0 {
		d.VenueID = ids[0]
	}
	d.SourceKind, d.SourceUID = attrString(attrs, "source_kind"), attrString(attrs, "source_uid")
	return d
}
