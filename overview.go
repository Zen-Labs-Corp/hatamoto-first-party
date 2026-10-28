package main

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
}

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
	}
	trend, err := buildTrend(today)
	if err != nil {
		return overview{}, err
	}
	o.Trend = trend
	if len(head) > 0 {
		o.Active, o.Today, o.New = head[0].Active, head[0].Today, head[0].New
	}
	return o, nil
}
