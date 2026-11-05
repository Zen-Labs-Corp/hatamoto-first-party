package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"hatamoto.dev/sdk"
)

const (
	listLimit = 20
	// readBudget keeps a step under the host's 200 content reads.
	readBudget = 190
)

// params are the run inputs.
type params struct {
	Locale      string
	Schema      string
	OnlyMissing bool
}

// parseParams reads run.info Params (a JSON object; only_missing may be
// absent or a string such as "true").
func parseParams(raw json.RawMessage) (params, error) {
	var m struct {
		Locale      string          `json:"locale"`
		Schema      string          `json:"schema"`
		OnlyMissing json.RawMessage `json:"only_missing"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return params{}, errors.New("the run inputs could not be read")
	}
	p := params{Locale: strings.TrimSpace(m.Locale), Schema: strings.TrimSpace(m.Schema)}
	switch s := strings.Trim(string(m.OnlyMissing), `"`); s {
	case "true", "1", "on", "yes":
		p.OnlyMissing = true
	}
	return p, nil
}

// cursor is the fetch checkpoint: the list cursor of the current page, the
// index inside that page and the buffer sequence. It must stay under 4 KiB.
type cursor struct {
	C   string `json:"c"`
	I   int    `json:"i"`
	Seq int    `json:"q"`
	N   int    `json:"n"`           // steps done, names the step's stat record
	L   int    `json:"l,omitempty"` // list page size, 0 = listLimit; shrinks when a page is too big
	R   int    `json:"r,omitempty"` // consecutive steps that ran out of time without progress
}

type entity struct {
	ID         string                     `json:"id"`
	SchemaSlug string                     `json:"schema_slug"`
	Slug       string                     `json:"slug"`
	Locale     string                     `json:"locale"`
	Name       string                     `json:"name"`
	Group      string                     `json:"translation_group"`
	ParentID   string                     `json:"parent_id"`
	Attributes map[string]json.RawMessage `json:"attributes"`

	// Set by nextCandidates on a candidate: its index in the page and the
	// skip counts up to and including it, so a caller that uses only the
	// first candidates can resume right after them.
	idx      int
	countsAt map[string]int

	// existingSlug is the slug of the copy already published in the target
	// locale, set on a candidate.
	existingSlug string
}

type pageResult struct {
	Items []entity
	Next  string
}

// listPage reads one page of a schema, at most limit items. There is no
// status argument (a run only sees published, public entries). Paging goes by
// the returned cursor, never by the item count, because non-public items are
// dropped after paging. The host refuses a result over its size cap with
// invalid_argument; the same cursor is then read again with half the limit.
// A smaller page changes where the page cursor leads, so that is only done at
// the start of a page (index 0). The limit used is returned.
func listPage(schema, cur string, limit, index int) (pageResult, int, error) {
	for {
		args := map[string]any{"schema_slug": schema, "limit": limit}
		if cur != "" {
			args["cursor"] = cur
		}
		var out struct {
			Items      []entity `json:"items"`
			NextCursor string   `json:"next_cursor"`
		}
		err := callHost(sdk.FnEntitiesList, args, &out)
		if err == nil {
			return pageResult{Items: out.Items, Next: out.NextCursor}, limit, nil
		}
		var he *sdk.HostError
		if !errors.As(err, &he) || he.Code != sdk.ErrInvalidArgument {
			return pageResult{}, limit, fmt.Errorf("the content list could not be read: %w", hostReason(err))
		}
		switch {
		case limit <= 1:
			return pageResult{}, limit, errors.New("the content list could not be read, even one entry at a time (invalid_argument): an entry may be too large to read")
		case index > 0:
			return pageResult{}, limit, errors.New("the content changed while the run was reading it: start the run again")
		}
		limit /= 2
	}
}

// hostReason reduces a host error to its code, so no detail leaks.
func hostReason(err error) error {
	var he *sdk.HostError
	if errors.As(err, &he) {
		return errors.New(he.Code)
	}
	return errors.New("host call failed")
}

// nonEmptyJSON reports whether a raw attribute holds something.
func nonEmptyJSON(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	switch string(t) {
	case "", "null", `""`, "[]", "{}":
		return false
	}
	return true
}

// isBuilderOnly reports an entity whose body lives in the page builder
// fields. A leftover content attribute does not matter: the renderer prefers
// the builder body, which would stay untranslated.
func isBuilderOnly(e entity) bool {
	return nonEmptyJSON(e.Attributes["page_doc"]) || nonEmptyJSON(e.Attributes["page_sections"])
}

// translatable reports whether the entity is a candidate, and otherwise the
// reason it is skipped.
func translatable(e entity, target string) (bool, string) {
	switch {
	case strings.EqualFold(e.Locale, target):
		return false, "same_locale"
	case isBuilderOnly(e):
		return false, "builder"
	case strings.TrimSpace(e.Name) == "":
		return false, "no_name"
	}
	return true, ""
}

// targetCopy looks for the translation group's member in the target locale
// and returns its slug, the last segment of its public path ("" when the host
// gave no path).
func targetCopy(id, target string) (found bool, slug string, err error) {
	var items []struct {
		Locale string `json:"locale"`
		Path   string `json:"path"`
	}
	if err := callHost(sdk.FnEntitiesTranslations, map[string]string{"id": id}, &items); err != nil {
		var he *sdk.HostError
		if errors.As(err, &he) && he.Code == sdk.ErrNotFound {
			return false, "", nil
		}
		return false, "", fmt.Errorf("the translations could not be read: %w", hostReason(err))
	}
	for _, it := range items {
		if strings.EqualFold(it.Locale, target) {
			p, _, _ := strings.Cut(it.Path, "?")
			return true, p[strings.LastIndex(p, "/")+1:], nil
		}
	}
	return false, "", nil
}

// nextCandidates re-lists the page at cur.C, skips cur.I items and collects
// up to max candidates from the rest of that page. Skipped entities are
// counted by reason. It stops early, with a resumable cursor, when the step's
// read budget is used up or max candidates are found. When the page is
// exhausted the cursor moves to the next page, and done is true when there is
// none.
func nextCandidates(p params, cur cursor, max int) (cands []entity, next cursor, done bool, counts map[string]int, err error) {
	counts = map[string]int{}
	next = cur
	limit := cur.L
	if limit <= 0 {
		limit = listLimit
	}
	page, limit, err := listPage(p.Schema, cur.C, limit, cur.I)
	if err != nil {
		return nil, cur, false, counts, err
	}
	if limit != listLimit {
		next.L = limit
	}
	reads := 1
	for i := max0(cur.I); i < len(page.Items); i++ {
		if len(cands) >= max || reads >= readBudget {
			next.I = i
			return cands, next, false, counts, nil
		}
		e := page.Items[i]
		if ok, reason := translatable(e, p.Locale); !ok {
			counts[reason]++
			continue
		}
		if e.Group != "" {
			if reads+1 > readBudget {
				next.I = i
				return cands, next, false, counts, nil
			}
			reads++
			has, slug, err := targetCopy(e.ID, p.Locale)
			if err != nil {
				return nil, cur, false, counts, err
			}
			if has && p.OnlyMissing {
				counts["has_translation"]++
				continue
			}
			e.existingSlug = slug // a re-run keeps the published URL
		}
		e.idx, e.countsAt = i, maps.Clone(counts)
		cands = append(cands, e)
	}
	next.C, next.I = page.Next, 0
	return cands, next, page.Next == "", counts, nil
}

func max0(n int) int { return max(n, 0) }
