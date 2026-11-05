package main

import (
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
)

// requiredPermissions are the grants the plugin needs to do its job.
var requiredPermissions = []string{"settings", "http.fetch", "http.fetch.any", "db", "kv", "mail.send", "log"}

const (
	incidentLimit      = 50
	mailUnavailableKey = "mail_unavailable"
)

// monitorView is one monitor as the admin page shows it. It never carries a
// setting value.
type monitorView struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	URL          string     `json:"url"`
	Interval     string     `json:"interval"`
	Keyword      string     `json:"keyword"`
	MaxMS        int        `json:"max_ms"`
	IsSite       bool       `json:"is_site"`
	Status       string     `json:"status"`
	Uptime24h    *float64   `json:"uptime_24h"`
	Uptime30d    *float64   `json:"uptime_30d"`
	AvgMS        *int       `json:"avg_ms"`
	LastCheck    *time.Time `json:"last_check"`
	CertDaysLeft *int       `json:"cert_days_left"`
	CertUnknown  bool       `json:"cert_unknown"` // the lookup failed and no date is known
	Bar          string     `json:"bar"`
	Cause        string     `json:"cause"`
	Since        *time.Time `json:"since"`
}

type incidentView struct {
	ID        int64      `json:"id"`
	MonitorID int64      `json:"monitor_id"`
	Name      string     `json:"name"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Cause     string     `json:"cause"`
}

type settingsView struct {
	AlertEmails    int      `json:"alert_emails"` // valid addresses
	InvalidEmails  []string `json:"invalid_emails"`
	Heartbeat      bool     `json:"heartbeat"`
	FailThreshold  int      `json:"fail_threshold"`
	SSLWarnDays    []int    `json:"ssl_warn_days"`
	MailConfigured bool     `json:"mail_configured"`
}

type overview struct {
	Monitors  []monitorView  `json:"monitors"`
	Incidents []incidentView `json:"incidents"`
	Settings  settingsView   `json:"settings"`
	Missing   []string       `json:"missing"`
	// DBReady is false when the database cannot be read; the lists are empty.
	DBReady bool `json:"db_ready"`
}

// viewOf is the part of a monitor's view that comes from its own row.
func viewOf(m monitor) monitorView {
	v := monitorView{
		ID: m.ID, Name: m.Name, URL: m.URL, Interval: intervalName(m.IntervalS), Keyword: m.Keyword,
		MaxMS: m.MaxMS, IsSite: m.IsSite, Status: m.Status, LastCheck: m.LastCheck,
	}
	if m.Status == statusDown || m.Status == statusConfig {
		v.Cause = m.LastError
	}
	if m.Status == statusDown {
		v.Since = m.DownSince
	}
	return v
}

func intervalName(secs int) string {
	for name, s := range intervals {
		if s == secs {
			return name
		}
	}
	return defaultEvery
}

// checkStats counts a monitor's checks (configuration rows excluded) over the
// last 24 hours, 30 days and the current UTC day.
type checkStats struct {
	MonitorID int64 `json:"monitor_id"`
	N24       int   `json:"n24"`
	Up24      int   `json:"up24"`
	N30       int   `json:"n30"`
	Up30      int   `json:"up30"`
	NToday    int   `json:"n_today"`
	UpToday   int   `json:"up_today"`
	AvgMS     *int  `json:"avg_ms"`
}

type dailyPoint struct {
	MonitorID int64   `json:"monitor_id"`
	Day       string  `json:"day"`
	Pct       float64 `json:"pct"`
}

func loadCheckStats(at time.Time) ([]checkStats, error) {
	var out []checkStats
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM (
		SELECT monitor_id,
		  (count(*) FILTER (WHERE at >= $1::timestamptz))::integer AS n24,
		  (count(*) FILTER (WHERE ok AND at >= $1::timestamptz))::integer AS up24,
		  count(*)::integer AS n30,
		  (count(*) FILTER (WHERE ok))::integer AS up30,
		  (count(*) FILTER (WHERE at >= $3::timestamptz))::integer AS n_today,
		  (count(*) FILTER (WHERE ok AND at >= $3::timestamptz))::integer AS up_today,
		  round(avg(ms) FILTER (WHERE ok AND at >= $1::timestamptz))::integer AS avg_ms
		FROM checks WHERE at >= $2::timestamptz AND ok IS NOT NULL GROUP BY monitor_id) s`,
		&out, ts(at.Add(-24*time.Hour)), ts(at.AddDate(0, 0, -checkKeepDays)), ts(dayStart(at)))
	return out, err
}

func loadDaily(from time.Time) ([]dailyPoint, error) {
	var out []dailyPoint
	err := dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('monitor_id', monitor_id, 'day', day, 'pct', uptime_pct)), '[]'::jsonb)
		FROM daily WHERE day >= $1::date`, &out, from.Format(dayLayout))
	return out, err
}

func loadCerts() ([]cert, error) {
	var out []cert
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(c)), '[]'::jsonb) FROM certs c`, &out)
	return out, err
}

func loadIncidents() ([]incidentView, error) {
	var out []incidentView
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(r)), '[]'::jsonb) FROM (
		SELECT i.id, i.monitor_id, coalesce(m.name, '') AS name, i.started_at, i.ended_at, i.cause
		FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id
		ORDER BY i.started_at DESC, i.id DESC LIMIT $1::integer) r`, &out, incidentLimit)
	return out, err
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func percent(up, n int) *float64 {
	if n == 0 {
		return nil
	}
	p := math.Round(float64(up)*10000/float64(n)) / 100
	return &p
}

// missingPermissions lists the required grants the owner has not given. When
// the host cannot say, nothing is reported.
func missingPermissions() []string {
	var p struct {
		Granted []string `json:"granted"`
	}
	if err := callHost("permissions", struct{}{}, &p); err != nil {
		logf("warn", "uptime-monitor: permissions unreadable code=%s", hostErrCode(err))
		return []string{}
	}
	out := []string{}
	for _, want := range requiredPermissions {
		if !slices.Contains(p.Granted, want) {
			out = append(out, want)
		}
	}
	return out
}

// mailConfigured is false once a send has found the site's mail service
// missing, until a mail goes out again.
func mailConfigured() bool {
	var flagged bool
	if _, err := kvGet(mailUnavailableKey, &flagged); err != nil {
		return true
	}
	return !flagged
}

// buildOverview gathers everything the admin page shows. A database that
// cannot be read gives an empty page with DBReady false, so the permission
// notices still show.
func buildOverview() overview {
	st := loadSettings()
	out := overview{
		Monitors: []monitorView{}, Incidents: []incidentView{}, Missing: missingPermissions(),
		Settings: settingsView{
			AlertEmails: len(st.AlertEmails), InvalidEmails: append([]string{}, st.InvalidEmails...), Heartbeat: st.HeartbeatURL != "", FailThreshold: st.FailThreshold,
			SSLWarnDays: st.SSLWarnDays, MailConfigured: mailConfigured(),
		},
	}
	monitors, stats, daily, certs, incidents, err := loadOverviewData(now().UTC())
	if err != nil {
		logf("warn", "uptime-monitor: overview unavailable code=%s", hostErrCode(err))
		return out
	}
	out.DBReady = true
	out.Incidents = incidents
	out.Monitors = monitorViews(now().UTC(), monitors, stats, daily, certs)
	return out
}

func loadOverviewData(at time.Time) (monitors []monitor, stats []checkStats, daily []dailyPoint, certs []cert, incidents []incidentView, err error) {
	if monitors, err = listMonitors(); err != nil {
		return
	}
	if stats, err = loadCheckStats(at); err != nil {
		return
	}
	if daily, err = loadDaily(dayStart(at).AddDate(0, 0, -(barDays - 1))); err != nil {
		return
	}
	if certs, err = loadCerts(); err != nil {
		return
	}
	incidents, err = loadIncidents()
	return
}

func monitorViews(at time.Time, monitors []monitor, stats []checkStats, daily []dailyPoint, certs []cert) []monitorView {
	statOf := map[int64]checkStats{}
	for _, s := range stats {
		statOf[s.MonitorID] = s
	}
	certOf := map[string]cert{}
	for _, c := range certs {
		certOf[c.Host] = c
	}
	today := dayStart(at)
	out := make([]monitorView, 0, len(monitors))
	for _, m := range monitors {
		v := viewOf(m)
		s := statOf[m.ID]
		v.Uptime24h, v.Uptime30d, v.AvgMS = percent(s.Up24, s.N24), percent(s.Up30, s.N30), s.AvgMS

		var cells [barDays]dayCell
		for _, d := range daily {
			day, err := time.Parse(dayLayout, d.Day)
			if err != nil || d.MonitorID != m.ID {
				continue
			}
			if i := barDays - 1 - int(today.Sub(day)/(24*time.Hour)); i >= 0 && i < barDays {
				cells[i] = dayCell{Has: true, Pct: d.Pct}
			}
		}
		if s.NToday > 0 { // today is not rolled up yet
			cells[barDays-1] = dayCell{Has: true, Pct: float64(s.UpToday) * 100 / float64(s.NToday)}
		}
		v.Bar = uptimeBar(cells)

		if u, err := url.Parse(m.URL); err == nil && u.Scheme == "https" {
			if c, ok := certOf[strings.ToLower(u.Hostname())]; ok && c.NotAfter != nil {
				d := int(math.Floor(c.NotAfter.Sub(at).Hours() / 24))
				v.CertDaysLeft = &d
			} else if ok && c.LookupError != "" {
				v.CertUnknown = true
			}
		}
		out = append(out, v)
	}
	return out
}
