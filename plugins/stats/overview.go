package main

import (
	"cmp"
	"slices"
	"strings"
)

// point is one trend sample: the installs seen in the 30 days ending that day.
type point struct {
	Date   string `json:"date"`
	Label  string `json:"label"`
	Active int    `json:"active"`
	Kind   string `json:"kind"` // always "week"
}

type overview struct {
	Days     int      `json:"days"`
	Active   int      `json:"active"`
	Today    int      `json:"today"`
	New      int      `json:"new"`
	Channels []Bucket `json:"channels"`
	Versions []Bucket `json:"versions"`
	Trend    []point  `json:"trend"`

	// Detailed reports (empty slices, never null, when there are none).
	Detailed          detailedInfo `json:"detailed"`
	Plugins           []pluginUse  `json:"plugins"`
	SelfBuiltInstalls int          `json:"self_built_installs"`
	Themes            []themeUse   `json:"themes"`
	Health            healthSums   `json:"health"`
	Errors            errorsTable  `json:"errors"`
}

// detailedInfo: installs with a Detailed report in the window, of the active ones.
type detailedInfo struct {
	Reports int `json:"reports"`
	Active  int `json:"active"`
}

type pluginUse struct {
	Name         string   `json:"name"`
	Installs     int      `json:"installs"`
	Versions     []Bucket `json:"versions"`
	ErrorsPerDay float64  `json:"errors_per_day"`
}

type themeUse struct {
	Name     string   `json:"name"`
	Installs int      `json:"installs"`
	Versions []Bucket `json:"versions"`
}

// healthSums: panics, 5xx and render errors per install-day with a report; the
// update and migration counts are raw sums over the window.
type healthSums struct {
	Panics           float64 `json:"panics"`
	HTTP5xx          float64 `json:"http_5xx"`
	RenderErrors     float64 `json:"render_errors"`
	UpdateFailed     int     `json:"update_failed"`
	UpdateRolledBack int     `json:"update_rolled_back"`
	MigrationFailed  int     `json:"migration_failed"`
}

type errorsTable struct {
	Cores   []string       `json:"cores"`
	Plugins []pluginErrors `json:"plugins"`
}

type pluginErrors struct {
	Name  string      `json:"name"`
	Cells []errorCell `json:"cells"`
}

// errorCell is one plugin on one core version: errors per install-day, how many
// installs it covers, and the jump flag against the next lower core version.
type errorCell struct {
	Core     string  `json:"core"`
	Rate     float64 `json:"rate"`
	Installs int     `json:"installs"`
	Jump     bool    `json:"jump"`
	Ratio    float64 `json:"ratio"`
}

// errRow is the SQL aggregate behind one cell.
type errRow struct {
	Name     string `json:"name"`
	Core     string `json:"core"`
	Errors   int    `json:"errors"`
	Days     int    `json:"days"`
	Installs int    `json:"installs"`
}

const (
	shownCores   = 4 // core versions in the errors table
	jumpInstalls = 3 // fewest installs on the new core for a jump
	jumpFactor   = 2 // rate on the new core vs the previous one
)

// The window is (start, today], both UTC days passed as $1 and $2. Aggregation
// is in SQL, bucketing in Go; every query returns one jsonb document.

// qHeadline: active installs in the window, installs seen today, and New =
// first ever seen inside the window (no daily row at or before the window start
// and no monthly row).
const qHeadline = `WITH win AS (SELECT DISTINCT install FROM installs_daily WHERE day > $1::date AND day <= $2::date) SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (SELECT
  (SELECT count(*) FROM win) AS active,
  (SELECT count(DISTINCT install) FROM installs_daily WHERE day = $2::date) AS today,
  (SELECT count(*) FROM win w
    WHERE NOT EXISTS (SELECT 1 FROM installs_daily o WHERE o.install = w.install AND o.day <= $1::date)
      AND NOT EXISTS (SELECT 1 FROM installs_monthly m WHERE m.install = w.install)) AS new
) t`

// latestInWindow is each active install's newest row in the window: one index
// probe per distinct install instead of sorting every row of the window.
const latestInWindow = `SELECT w.install, l.version, l.channel FROM (SELECT DISTINCT install FROM installs_daily WHERE day > $1::date AND day <= $2::date) w CROSS JOIN LATERAL (SELECT d.version, d.channel FROM installs_daily d WHERE d.install = w.install AND d.day > $1::date AND d.day <= $2::date ORDER BY d.day DESC LIMIT 1) l`

const qChannels = `SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (SELECT channel AS label, count(*) AS count FROM (` + latestInWindow + `) l GROUP BY channel ORDER BY channel) t`

// qVersions groups the newest versions by install; every version past the
// 1,000 biggest is summed into one "other" row, so the rows always add up to
// the active installs (a real version always starts with "v").
const qVersions = `SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.count DESC, t.label DESC), '[]'::jsonb) FROM (SELECT CASE WHEN g.rn <= 1000 THEN g.label ELSE 'other' END AS label, sum(g.count)::int AS count FROM (SELECT version AS label, count(*) AS count, row_number() OVER (ORDER BY count(*) DESC, version DESC) AS rn FROM (` + latestInWindow + `) l GROUP BY version) g GROUP BY 1) t`

// latestDetail is each install's newest Detailed report in the window, one
// index probe per install like latestInWindow.
const latestDetail = `SELECT w.install, l.day, l.theme_name, l.theme_version, l.other_plugins FROM (SELECT DISTINCT install FROM detail_daily WHERE day > $1::date AND day <= $2::date) w CROSS JOIN LATERAL (SELECT d.day, d.theme_name, d.theme_version, d.other_plugins FROM detail_daily d WHERE d.install = w.install AND d.day > $1::date AND d.day <= $2::date ORDER BY d.day DESC LIMIT 1) l`

// qDetailed: installs with a report and, of those, installs whose newest report
// counts self-built plugins.
const qDetailed = `SELECT jsonb_build_object('reports', count(*), 'self_built', count(*) FILTER (WHERE other_plugins > 0)) FROM (` + latestDetail + `) l`

// qPlugins and qThemes: installs per name and version in each install's newest
// report, capped at 1,000 rows.
const qPlugins = `SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.installs DESC, t.name, t.version), '[]'::jsonb) FROM (SELECT p.name, p.version, count(*)::int AS installs FROM (` + latestDetail + `) l JOIN plugins_daily p ON p.install = l.install AND p.day = l.day GROUP BY p.name, p.version ORDER BY count(*) DESC, p.name, p.version LIMIT 1000) t`

const qThemes = `SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.installs DESC, t.name, t.version), '[]'::jsonb) FROM (SELECT theme_name AS name, theme_version AS version, count(*)::int AS installs FROM (` + latestDetail + `) l GROUP BY theme_name, theme_version ORDER BY count(*) DESC, theme_name, theme_version LIMIT 1000) t`

// qHealth: sums over every report in the window, with the install-day count.
const qHealth = `SELECT jsonb_build_object('days', count(*), 'panics', coalesce(sum(panics), 0), 'http_5xx', coalesce(sum(http_5xx), 0), 'render_errors', coalesce(sum(render_errors), 0), 'update_failed', coalesce(sum(update_failed), 0), 'update_rolled_back', coalesce(sum(update_rolled_back), 0), 'migration_failed', coalesce(sum(migration_failed), 0)) FROM detail_daily WHERE day > $1::date AND day <= $2::date`

// qErrors: per plugin and core version, its error count over the install-days
// that listed it, capped at 1,000 rows.
const qErrors = `SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.name, t.core), '[]'::jsonb) FROM (SELECT p.name, d.core_version AS core, sum(p.errors)::int AS errors, count(*)::int AS days, count(DISTINCT p.install)::int AS installs FROM plugins_daily p JOIN detail_daily d ON d.install = p.install AND d.day = p.day WHERE p.day > $1::date AND p.day <= $2::date GROUP BY p.name, d.core_version ORDER BY p.name, d.core_version LIMIT 1000) t`

// nameVersion is one qPlugins / qThemes row.
type nameVersion struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Installs int    `json:"installs"`
}

// usageByName groups rows by name: total installs and the version split
// (labels get the v prefix groupVersions expects, for display only), biggest
// first, ties by name.
func usageByName(rows []nameVersion) (names []string, installs map[string]int, versions map[string][]Bucket) {
	installs, byName := map[string]int{}, map[string][]Bucket{}
	for _, r := range rows {
		if _, ok := installs[r.Name]; !ok {
			names = append(names, r.Name)
		}
		installs[r.Name] += r.Installs
		byName[r.Name] = append(byName[r.Name], Bucket{"v" + r.Version, r.Installs})
	}
	slices.SortFunc(names, func(a, b string) int { return cmp.Or(cmp.Compare(installs[b], installs[a]), strings.Compare(a, b)) })
	versions = map[string][]Bucket{}
	for n, b := range byName {
		versions[n] = groupVersions(b)
	}
	return names, installs, versions
}

// buildErrors turns the per-plugin, per-core rows into the table: the highest
// shownCores core versions, and per plugin the rate and jump flag of each core
// it was reported on. A jump compares a core with the next lower core that has
// data for that plugin: at least jumpInstalls installs, a rate above zero and
// at least jumpFactor times the previous rate.
func buildErrors(rows []errRow) errorsTable {
	out := errorsTable{Cores: []string{}, Plugins: []pluginErrors{}}
	seen := map[string]bool{}
	byPlugin := map[string][]errRow{}
	for _, r := range rows {
		if r.Days <= 0 {
			continue
		}
		if !seen[r.Core] {
			seen[r.Core] = true
			out.Cores = append(out.Cores, r.Core)
		}
		byPlugin[r.Name] = append(byPlugin[r.Name], r)
	}
	slices.SortFunc(out.Cores, func(a, b string) int { return compareVersions(b, a) })
	shown := map[string]bool{}
	for i, c := range out.Cores {
		if i < shownCores {
			shown[c] = true
		}
	}
	out.Cores = out.Cores[:min(len(out.Cores), shownCores)]
	names := make([]string, 0, len(byPlugin))
	for n := range byPlugin {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		rs := byPlugin[n]
		slices.SortFunc(rs, func(a, b errRow) int { return compareVersions(a.Core, b.Core) })
		cells := []errorCell{}
		prevRate := -1.0
		for _, r := range rs {
			rate := float64(r.Errors) / float64(r.Days)
			cell := errorCell{Core: r.Core, Rate: rate, Installs: r.Installs}
			if prevRate >= 0 {
				cell.Jump = r.Installs >= jumpInstalls && rate > 0 && rate >= jumpFactor*prevRate
				if prevRate > 0 {
					cell.Ratio = rate / prevRate
				}
			}
			prevRate = rate
			if shown[r.Core] {
				cells = append(cells, cell)
			}
		}
		slices.Reverse(cells) // newest core first
		if len(cells) > 0 {
			out.Plugins = append(out.Plugins, pluginErrors{Name: n, Cells: cells})
		}
	}
	return out
}

// buildDetailed fills the Detailed fields of o for the window.
func buildDetailed(o *overview, start, end string) error {
	var det struct {
		Reports   int `json:"reports"`
		SelfBuilt int `json:"self_built"`
	}
	if err := dbJSON(qDetailed, &det, start, end); err != nil {
		return err
	}
	var plugins, themes []nameVersion
	if err := dbJSON(qPlugins, &plugins, start, end); err != nil {
		return err
	}
	if err := dbJSON(qThemes, &themes, start, end); err != nil {
		return err
	}
	var h struct {
		Days             int `json:"days"`
		Panics           int `json:"panics"`
		HTTP5xx          int `json:"http_5xx"`
		RenderErrors     int `json:"render_errors"`
		UpdateFailed     int `json:"update_failed"`
		UpdateRolledBack int `json:"update_rolled_back"`
		MigrationFailed  int `json:"migration_failed"`
	}
	if err := dbJSON(qHealth, &h, start, end); err != nil {
		return err
	}
	var errRows []errRow
	if err := dbJSON(qErrors, &errRows, start, end); err != nil {
		return err
	}

	o.Detailed = detailedInfo{Reports: det.Reports, Active: o.Active}
	o.SelfBuiltInstalls = det.SelfBuilt
	o.Errors = buildErrors(errRows)

	errs, days := map[string]int{}, map[string]int{}
	for _, r := range errRows {
		errs[r.Name] += r.Errors
		days[r.Name] += r.Days
	}
	names, installs, versions := usageByName(plugins)
	for _, n := range names {
		u := pluginUse{Name: n, Installs: installs[n], Versions: versions[n]}
		if days[n] > 0 {
			u.ErrorsPerDay = float64(errs[n]) / float64(days[n])
		}
		o.Plugins = append(o.Plugins, u)
	}
	names, installs, versions = usageByName(themes)
	for _, n := range names {
		o.Themes = append(o.Themes, themeUse{Name: n, Installs: installs[n], Versions: versions[n]})
	}
	o.Health = healthSums{UpdateFailed: h.UpdateFailed, UpdateRolledBack: h.UpdateRolledBack, MigrationFailed: h.MigrationFailed}
	if h.Days > 0 {
		d := float64(h.Days)
		o.Health.Panics, o.Health.HTTP5xx, o.Health.RenderErrors = float64(h.Panics)/d, float64(h.HTTP5xx)/d, float64(h.RenderErrors)/d
	}
	return nil
}

func buildOverview(days int) (overview, error) {
	today := now().UTC()
	end := today.Format("2006-01-02")
	start := today.AddDate(0, 0, -days).Format("2006-01-02")

	var head []struct{ Active, Today, New int }
	if err := dbJSON(qHeadline, &head, start, end); err != nil {
		return overview{}, err
	}
	var channels, versions []Bucket
	if err := dbJSON(qChannels, &channels, start, end); err != nil {
		return overview{}, err
	}
	if err := dbJSON(qVersions, &versions, start, end); err != nil {
		return overview{}, err
	}
	o := overview{
		Days:     days,
		Channels: channelBuckets(channels),
		Versions: groupVersions(versions),
		Trend:    []point{},
		Plugins:  []pluginUse{},
		Themes:   []themeUse{},
		Errors:   errorsTable{Cores: []string{}, Plugins: []pluginErrors{}},
	}
	trend, err := buildTrend(today)
	if err != nil {
		return overview{}, err
	}
	o.Trend = trend
	if len(head) > 0 {
		o.Active, o.Today, o.New = head[0].Active, head[0].Today, head[0].New
	}
	if err := buildDetailed(&o, start, end); err != nil {
		return overview{}, err
	}
	return o, nil
}
