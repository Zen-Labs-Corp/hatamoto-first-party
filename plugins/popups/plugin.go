// Command popups is a Hatamoto plugin: announcement bars and popups with page
// targeting, start and end dates and frequency caps.
//
// The render.head filter publishes the campaigns that can still apply to the
// page as one <meta name="hatamoto-popups"> tag; assets/plugin.js (linked on
// every public page by the core) re-checks everything in the browser and shows
// them. The plugin never uses render.page, so pages stay cacheable.
package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const maxCampaigns = 10

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// now is the clock, replaced in tests.
var now = time.Now

// campaign is both the settings item and the published shape.
type campaign struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Message       string   `json:"message"`
	LinkLabel     string   `json:"link_label,omitempty"`
	LinkURL       string   `json:"link_url,omitempty"`
	IncludePaths  []string `json:"include_paths,omitempty"`
	ExcludePaths  []string `json:"exclude_paths,omitempty"`
	Start         string   `json:"start,omitempty"`
	End           string   `json:"end,omitempty"`
	Frequency     string   `json:"frequency"`
	Trigger       string   `json:"trigger"`
	DelaySeconds  int      `json:"delay_seconds,omitempty"`
	ScrollPercent int      `json:"scroll_percent,omitempty"`
	Position      string   `json:"position"`
}

func init() {
	guest.OnFilter(sdk.RenderHead, publishCampaigns)
}

// publishCampaigns never fails the render: unreadable, empty or invalid
// settings just mean no tag.
func publishCampaigns(c sdk.HeadContext) (sdk.HeadContext, error) {
	var s struct {
		Campaigns []campaign `json:"campaigns"`
	}
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return c, nil
	}
	var keep []campaign
	seen := map[string]bool{}
	for _, cp := range s.Campaigns {
		if len(keep) == maxCampaigns {
			break
		}
		cp, ok := normalize(cp)
		if !ok || seen[cp.ID] || !activeOn(cp, c.Path, now()) {
			continue
		}
		seen[cp.ID] = true
		keep = append(keep, cp)
	}
	if len(keep) == 0 {
		return c, nil
	}
	b, err := json.Marshal(keep)
	if err != nil {
		return c, nil
	}
	c.Tags = append(c.Tags, sdk.HeadTag{
		Kind:  "meta",
		Attrs: map[string]string{"name": "hatamoto-popups", "content": string(b)},
	})
	return c, nil
}

// normalize validates a campaign and fills defaults. ok is false when it
// cannot be shown at all.
func normalize(cp campaign) (campaign, bool) {
	cp.Message = strings.TrimSpace(cp.Message)
	if !idRe.MatchString(cp.ID) || cp.Message == "" || (cp.Kind != "bar" && cp.Kind != "popup") {
		return cp, false
	}
	if _, ok := parseUTC(cp.Start, false); !ok {
		return cp, false
	}
	if _, ok := parseUTC(cp.End, true); !ok {
		return cp, false
	}
	if !safeLink(cp.LinkURL) || strings.TrimSpace(cp.LinkLabel) == "" {
		cp.LinkURL, cp.LinkLabel = "", ""
	}
	if !oneOf(cp.Frequency, "session", "day", "week", "once") {
		cp.Frequency = "session"
	}
	if !oneOf(cp.Trigger, "load", "delay", "scroll", "exit") {
		cp.Trigger = "load"
	}
	if !oneOf(cp.Position, "top", "bottom") {
		cp.Position = "top"
	}
	cp.DelaySeconds = clamp(cp.DelaySeconds, 0, 600)
	if cp.Trigger == "scroll" {
		cp.ScrollPercent = clamp(cp.ScrollPercent, 1, 100)
	} else {
		cp.ScrollPercent = 0
	}
	return cp, true
}

// activeOn reports whether the campaign can still apply to path at t: not
// ended, and path-matching. The start is left to the browser, because the
// cached head outlives it.
func activeOn(cp campaign, path string, t time.Time) bool {
	if end, ok := parseUTC(cp.End, true); ok && !end.IsZero() && end.Before(t) {
		return false
	}
	return matchPath(path, cp.IncludePaths, cp.ExcludePaths)
}

// parseUTC reads YYYY-MM-DD[ T]HH:MM as UTC. A date alone is 00:00:00 for a
// start and 23:59:59 for an end. Empty is ok with the zero time (open).
func parseUTC(s string, isEnd bool) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, true
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	if isEnd {
		t = t.Add(24*time.Hour - time.Second)
	}
	return t, true
}

// safeLink accepts http, https and site paths ("/x", not "//host").
func safeLink(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	if strings.HasPrefix(u, "/") {
		return !strings.HasPrefix(u, "//") && !strings.HasPrefix(u, `/\`)
	}
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// matchPath: the path must match an include glob (none = every page) and no
// exclude glob.
func matchPath(path string, include, exclude []string) bool {
	if len(include) > 0 && !anyGlob(include, path) {
		return false
	}
	return !anyGlob(exclude, path)
}

func anyGlob(globs []string, path string) bool {
	for _, g := range globs {
		if g = strings.TrimSpace(g); g != "" && globRegexp(g).MatchString(path) {
			return true
		}
	}
	return false
}

// globRegexp turns a glob into an anchored regexp: * stays inside one
// segment, ** crosses segments, and a trailing /** also matches the folder
// itself. Same rules as assets/plugin.js.
func globRegexp(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		switch {
		case strings.HasPrefix(g[i:], "/**") && i+3 == len(g):
			b.WriteString("(?:/.*)?")
			i += 2
		case strings.HasPrefix(g[i:], "**"):
			b.WriteString(".*")
			i++
		case g[i] == '*':
			b.WriteString("[^/]*")
		default:
			b.WriteString(regexp.QuoteMeta(g[i : i+1]))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func oneOf(s string, set ...string) bool {
	for _, v := range set {
		if s == v {
			return true
		}
	}
	return false
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
