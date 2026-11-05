package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	transformBatch  = 25
	transformBudget = 8 * time.Second
	bufferPage      = 500
)

// tcursor is the transform checkpoint: the position in the ordered record
// list, and for records whose slug had to be moved aside because a published
// entity holds it, the suffix chosen (position -> n, as in base-n). A resumed
// step replays the slug choices of the earlier records from their keys and
// these suffixes, without asking the host again. Conflicts is
// the number of records the host refused.
type tcursor struct {
	I         int            `json:"i"`
	Suffix    map[string]int `json:"s,omitempty"`
	Conflicts int            `json:"x,omitempty"`
}

// recKey is a buffer key split into its parts.
type recKey struct {
	key, id, parent, locale, base string
	existing                      bool
	depth                         int
}

// listKeys lists every buffer key under prefix.
func listKeys(prefix string) ([]string, error) {
	var keys []string
	after := ""
	for {
		page, next, err := guest.BufferList(callHost, prefix, after, bufferPage)
		if err != nil {
			return nil, fmt.Errorf("the run buffer could not be listed: %w", hostReason(err))
		}
		keys = append(keys, page...)
		if next == "" || len(page) == 0 {
			return keys, nil
		}
		after = next
	}
}

// orderKeys sorts the record keys so a parent comes before its children: by
// depth in the run's own parent chains, then by key. A parent outside the run,
// or a cycle, makes the entry a root.
func orderKeys(keys []string) []recKey {
	rks := make([]recKey, 0, len(keys))
	parentOf := map[string]string{}
	for _, k := range keys {
		parts := strings.Split(k, "/") // src, seq, id, parent, locale, flag+base
		if len(parts) != 6 || parts[5] == "" {
			continue
		}
		rk := recKey{key: k, id: parts[2], parent: parts[3], locale: parts[4],
			base: parts[5][1:], existing: parts[5][0] == 'e'}
		if rk.parent == "-" {
			rk.parent = ""
		}
		parentOf[rk.id] = rk.parent
		rks = append(rks, rk)
	}
	for i := range rks {
		d, id := 0, rks[i].id
		for {
			p, ok := parentOf[id]
			if _, inRun := parentOf[p]; !ok || p == "" || !inRun {
				break
			}
			if d++; d > len(rks) { // a cycle
				d = 0
				break
			}
			id = p
		}
		rks[i].depth = d
	}
	sort.SliceStable(rks, func(a, b int) bool {
		if rks[a].depth != rks[b].depth {
			return rks[a].depth < rks[b].depth
		}
		return rks[a].key < rks[b].key
	})
	return rks
}

func getRecord(key string) (record, error) {
	raw, found, err := guest.BufferGet(callHost, key)
	if err != nil {
		return record{}, fmt.Errorf("a run buffer record could not be read: %w", hostReason(err))
	}
	if !found {
		return record{}, errors.New("a run buffer record is missing")
	}
	var r record
	if err := json.Unmarshal(raw, &r); err != nil {
		return record{}, errors.New("a run buffer record is damaged")
	}
	return r, nil
}

// upsertFor builds the staged copy of a record.
func upsertFor(r record, locale, siteLocale, slug, parentKey string) (sdk.EntityUpsert, error) {
	attrs := map[string]json.RawMessage{}
	for k, v := range r.Attributes {
		attrs[k] = v
	}
	for k, v := range map[string]string{
		"content": r.T.Content, "excerpt": r.T.Excerpt, "description": r.T.Description,
		"meta_title": r.T.MetaTitle, "meta_description": r.T.MetaDescription,
		"og_title": r.T.OGTitle, "og_description": r.T.OGDescription,
	} {
		if v != "" {
			b, _ := json.Marshal(v)
			attrs[k] = b
		}
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return sdk.EntityUpsert{}, errors.New("a record could not be encoded")
	}
	up := sdk.EntityUpsert{
		SourceKey:     sourceKey(locale, r.ID),
		Schema:        r.SchemaSlug,
		Slug:          slug,
		Locale:        siteLocale,
		TranslationOf: r.ID,
		Name:          r.T.Name,
		Attributes:    raw,
		ParentKey:     parentKey,
	}
	if r.T.MetaTitle != "" || r.T.MetaDescription != "" {
		up.SEO = &sdk.SEO{MetaTitle: r.T.MetaTitle, MetaDescription: r.T.MetaDescription}
	}
	return up, nil
}

// sourceKey is stable per source entity and locale, so a re-run updates the
// copy staged before instead of adding another.
func sourceKey(locale, id string) string { return "tr:" + locale + ":" + id }

// transformStep stages up to 25 translated copies per step, parents first.
// The slug set and the set of staged parents are rebuilt each step from the
// record keys; see tcursor.
func transformStep(s sdk.RunStep) (sdk.RunStepResult, error) {
	start := time.Now()
	info, err := guest.RunInfo(callHost)
	if err != nil {
		return failRun("the run details could not be read")
	}
	p, err := parseParams(info.Params)
	if err != nil {
		return failRun(err.Error())
	}
	loc, err := normalizeLocale(p.Locale)
	if err != nil {
		return failRun(err.Error())
	}
	var cur tcursor
	if s.Cursor != "" {
		if err := json.Unmarshal([]byte(s.Cursor), &cur); err != nil {
			return failRun("the run cursor could not be read")
		}
	}
	site, err := guest.SiteInfo(callHost)
	if err != nil {
		return failRun(fmt.Sprintf("the site languages could not be read: %v", hostReason(err)))
	}
	siteLocale := ""
	for _, l := range site.Locales {
		if strings.EqualFold(l, loc) {
			siteLocale = l
		}
	}
	if siteLocale == "" || strings.EqualFold(siteLocale, site.Language) {
		return failRun(fmt.Sprintf("locale %s is not one of this site's locales", loc))
	}
	keys, err := listKeys("src/")
	if err != nil {
		return failRun(err.Error())
	}
	ordered := orderKeys(keys)

	slugs := newSlugSet()
	inRun := map[string]bool{} // ids staged earlier in the run
	valid, dropped := 0, 0
	for _, rk := range ordered {
		if strings.EqualFold(rk.locale, site.Language) {
			valid++
		} else {
			dropped++
		}
	}
	suffix := cur.Suffix
	newSuffix := map[string]int{}

	// Earlier records are replayed from their keys alone (slug base, parent,
	// language), so a resumed step costs no host call per record.
	for i := 0; i < cur.I && i < len(ordered); i++ {
		rk := ordered[i]
		if !strings.EqualFold(rk.locale, site.Language) {
			continue
		}
		if n, moved := suffix[strconv.Itoa(i)]; moved && !rk.existing {
			slugs.add(p.Schema, siteLocale, candidate(rk.base, n))
		} else if rk.existing {
			slugs.add(p.Schema, siteLocale, rk.base)
		} else {
			slug, _ := slugs.freeLocally(p.Schema, siteLocale, rk.base)
			slugs.add(p.Schema, siteLocale, slug)
		}
		inRun[rk.id] = true
	}

	end := min(cur.I+transformBatch, len(ordered))
	i := cur.I
	for ; i < len(ordered); i++ {
		if i >= end || (i > cur.I && time.Since(start) > transformBudget) {
			break
		}
		rk := ordered[i]
		if !strings.EqualFold(rk.locale, site.Language) {
			continue
		}
		rec, err := getRecord(rk.key)
		if err != nil {
			return failRun(err.Error())
		}
		var slug string
		if rk.existing {
			// The published copy holds this slug itself: no check, no suffix.
			slug = rec.ExistingSlug
			slugs.add(p.Schema, siteLocale, rk.base)
		} else {
			local, _ := slugs.freeLocally(p.Schema, siteLocale, rk.base)
			if slug, err = slugs.pick(p.Schema, siteLocale, rk.base); err != nil {
				return failRun(err.Error())
			}
			if slug != local {
				newSuffix[strconv.Itoa(i)] = suffixOf(rk.base, slug)
			}
		}
		parentKey := ""
		if rec.ParentID != "" && inRun[rec.ParentID] {
			parentKey = sourceKey(loc, rec.ParentID)
		}
		inRun[rec.ID] = true
		up, err := upsertFor(rec, loc, siteLocale, slug, parentKey)
		if err != nil {
			return failRun(err.Error())
		}
		if _, err := guest.EntityUpsert(callHost, up); err != nil {
			var he *sdk.HostError
			if errors.As(err, &he) && (he.Code == sdk.ErrNotOwned || he.Code == sdk.ErrInvalidArgument) {
				cur.Conflicts++
				continue
			}
			return failRun(fmt.Sprintf("a translated copy could not be staged: %v", hostReason(err)))
		}
	}

	if i < len(ordered) {
		next := tcursor{I: i, Suffix: suffix, Conflicts: cur.Conflicts}
		if next.Suffix == nil {
			next.Suffix = map[string]int{}
		}
		for k, v := range newSuffix {
			next.Suffix[k] = v
		}
		cp, err := json.Marshal(next)
		if err != nil {
			return failRun("the run cursor could not be encoded")
		}
		if len(cp) > maxCheckpoint {
			return failRun("too many slug collisions to resume")
		}
		if err := guest.RunCheckpoint(callHost, string(cp)); err != nil {
			return failRun(fmt.Sprintf("the checkpoint was refused: %v", hostReason(err)))
		}
		msg := fmt.Sprintf("staged %d of %d", i, len(ordered))
		if err := guest.RunProgress(callHost, i, len(ordered), msg); err != nil {
			return failRun(fmt.Sprintf("the progress report was refused: %v", hostReason(err)))
		}
		return sdk.RunStepResult{}, nil
	}

	// The last step: report.
	total, err := sumStats()
	if err != nil {
		return failRun(err.Error())
	}
	total.Skipped["other_language"] = dropped
	total.Skipped["conflict"] = cur.Conflicts
	for _, k := range []string{"builder", "same_locale", "has_translation", "too_large", "no_name"} {
		total.Skipped[k] += 0 // always present in the summary
	}
	summary := map[string]any{
		"locale":              loc,
		"schema":              p.Schema,
		"staged":              valid - cur.Conflicts,
		"skipped":             total.Skipped,
		"glossary_terms_lost": total.GlossaryLost,
	}
	msg := fmt.Sprintf("staged %d of %d", len(ordered), len(ordered))
	if err := guest.RunProgress(callHost, len(ordered), len(ordered), msg); err != nil {
		return failRun(fmt.Sprintf("the progress report was refused: %v", hostReason(err)))
	}
	if err := guest.RunFinish(callHost, summary); err != nil {
		return failRun(fmt.Sprintf("the phase could not be finished: %v", hostReason(err)))
	}
	return sdk.RunStepResult{}, nil
}

// sumStats adds up the statistics the fetch steps stored.
func sumStats() (fetchStat, error) {
	total := fetchStat{Skipped: map[string]int{}}
	keys, err := listKeys("stat/")
	if err != nil {
		return total, err
	}
	for _, k := range keys {
		raw, found, err := guest.BufferGet(callHost, k)
		if err != nil {
			return total, fmt.Errorf("a statistics record could not be read: %w", hostReason(err))
		}
		var st fetchStat
		if !found || json.Unmarshal(raw, &st) != nil {
			continue
		}
		total.Translated += st.Translated
		total.GlossaryLost += st.GlossaryLost
		for r, n := range st.Skipped {
			total.Skipped[r] += n
		}
	}
	return total, nil
}
