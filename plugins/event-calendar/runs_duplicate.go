package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	maxShiftDays = 3650
	maxCopySlugs = 50
)

// duplicateEvent stages a copy of one event, its dates moved by shift_days. The
// copy gets a source key of its own, written into its source_uid, so the page
// can change its single dates later.
func duplicateEvent(runID string, p map[string]any) (sdk.RunStepResult, error) {
	shift := 0
	if v := paramString(p, "shift_days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < -maxShiftDays || n > maxShiftDays {
			return failRun("The shift must be a whole number of days between -3650 and 3650.")
		}
		shift = n
	}
	ev, err := loadRunEvent(paramString(p, "event"))
	if err != nil {
		return failRun(err.Error())
	}
	key := keyPrefix + runID

	attrs := cloneAttrs(ev.Attrs)
	for _, k := range []string{"start_at", "end_at", "repeat_until"} {
		if v := ev.attrText(k); v != "" {
			attrs[k], _ = json.Marshal(shiftText(v, shift))
		}
	}
	for _, k := range []string{"repeat_extra_dates", "repeat_skip_dates"} {
		list := ev.attrList(k)
		for i := range list {
			list[i] = shiftText(list[i], shift)
		}
		setList(attrs, k, list)
	}
	lines := ev.attrList("repeat_overrides")
	for i, line := range lines {
		parts := strings.Split(line, "|")
		for j := range parts {
			parts[j] = shiftText(parts[j], shift)
		}
		lines[i] = strings.Join(parts, "|")
	}
	setList(attrs, "repeat_overrides", lines)
	// provenance is not copied: the copy is this plugin's own
	attrs["source_uid"], _ = json.Marshal(key)
	attrs["source_kind"], _ = json.Marshal("manual")
	delete(attrs, "source_url")

	up, err := ev.upsert(key, attrs)
	if err != nil {
		return failRun(err.Error())
	}
	up.Name = ev.Name + " (copy)"
	if up.Slug, err = freeCopySlug(ev); err != nil {
		return failRun("The copy's slug could not be checked. Try again.")
	}
	if up.Slug == "" {
		return failRun("No free slug was found for the copy. Rename or delete the earlier copies first.")
	}
	if _, err := guest.EntityUpsert(callHost, up); err != nil {
		return failRun("The copy could not be staged. Try again.")
	}
	return sdk.RunStepResult{}, guest.RunFinish(callHost, map[string]any{"staged": 1, "slug": up.Slug, "shift_days": shift})
}

// freeCopySlug is the first of <slug>-copy, <slug>-copy-2, ... not taken by a
// published event; "" when none of the first few is free.
func freeCopySlug(ev runEvent) (string, error) {
	for i := 1; i <= maxCopySlugs; i++ {
		slug := ev.Slug + "-copy"
		if i > 1 {
			slug += "-" + strconv.Itoa(i)
		}
		taken, err := guest.SlugTaken(callHost, ev.Schema, slug, ev.Locale)
		if err != nil {
			return "", err
		}
		if !taken {
			return slug, nil
		}
	}
	return "", nil
}

// shiftText moves a stored date or date and time by n calendar days and keeps
// its layout and time of day; text that is not a date is returned as is.
func shiftText(s string, n int) string {
	s = strings.TrimSpace(s)
	if n == 0 {
		return s
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.AddDate(0, 0, n).Format(layout)
		}
	}
	return s
}
