package main

import (
	"slices"
	"strings"
	"time"
)

// Form kinds and result visibility values, as in the settings schema.
const (
	kindPoll   = "poll"
	kindSurvey = "survey"
	kindQuiz   = "quiz"

	showAlways     = "always"
	showAfterClose = "after_close"
	showNever      = "never"
)

// keyPair is one answer key entry of a quiz: a field name and its correct value.
type keyPair struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// formCfg is one entry of the "forms" setting.
type formCfg struct {
	Slug        string    `json:"form_slug"`
	Kind        string    `json:"kind"`
	ShowResults string    `json:"show_results"`
	ClosesAt    string    `json:"closes_at"`
	Key         []keyPair `json:"answer_key"`
	EmailScore  bool      `json:"email_score"`
}

// warnedClose remembers the forms whose closes_at was already reported.
var warnedClose = map[string]bool{}

// now is replaced by tests.
var now = time.Now

// loadConfig reads and normalises the forms setting: slugs are trimmed, empty
// and duplicate slugs dropped, unknown kinds become poll, unknown visibility
// becomes never (the safe value), answer key rows without a field are dropped
// and an unreadable closes_at is treated as not set.
func loadConfig() ([]formCfg, error) {
	var raw struct {
		Forms []formCfg `json:"forms"`
	}
	if err := callHost("settings.get", struct{}{}, &raw); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []formCfg
	for _, c := range raw.Forms {
		c.Slug = strings.TrimSpace(c.Slug)
		if c.Slug == "" || seen[c.Slug] {
			continue
		}
		seen[c.Slug] = true
		switch c.Kind {
		case kindPoll, kindSurvey, kindQuiz:
		default:
			c.Kind = kindPoll
		}
		switch c.ShowResults {
		case showAlways, showAfterClose, showNever:
		default:
			c.ShowResults = showNever
		}
		c.ClosesAt = strings.TrimSpace(c.ClosesAt)
		if _, ok := parseTime(c.ClosesAt); c.ClosesAt != "" && !ok {
			if !warnedClose[c.Slug] {
				warnedClose[c.Slug] = true
				_ = logf("warn", "polls: closes_at of %q is not a date and time, treated as not set", c.Slug)
			}
			c.ClosesAt = ""
		}
		c.Key = slices.DeleteFunc(slices.Clone(c.Key), func(k keyPair) bool { return strings.TrimSpace(k.Field) == "" })
		out = append(out, c)
	}
	return out, nil
}

// timeLayouts are the forms closes_at may take, matching the pattern in
// plugin.json: a date, or a date and time with a T or space separator,
// optional seconds and an optional Z or numeric offset.
var timeLayouts = []string{
	"2006-01-02",
	"2006-01-02T15:04", "2006-01-02 15:04",
	"2006-01-02T15:04:05", "2006-01-02 15:04:05",
	"2006-01-02T15:04Z07:00", "2006-01-02 15:04Z07:00",
	"2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05Z07:00",
}

// parseTime reads a timestamp tolerantly; a value without a zone is UTC.
func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// closes returns the close time, if one is set and readable.
func (c formCfg) closes() (time.Time, bool) {
	if c.ClosesAt == "" {
		return time.Time{}, false
	}
	return parseTime(c.ClosesAt)
}

// open reports whether answers at time t are still counted.
func (c formCfg) open(t time.Time) bool {
	end, ok := c.closes()
	return !ok || t.Before(end)
}

// public reports whether visitors may see the results at time t.
func (c formCfg) public(t time.Time) bool {
	switch c.ShowResults {
	case showAlways:
		return true
	case showAfterClose:
		end, ok := c.closes()
		return ok && !t.Before(end)
	}
	return false
}
