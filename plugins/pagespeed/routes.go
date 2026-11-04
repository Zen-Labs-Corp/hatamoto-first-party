package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"hatamoto.dev/sdk"
)

const (
	seriesDays   = 90
	maxBodyBytes = 4 << 10
)

// serve answers the admin routes (owner only, set in plugin.json).
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /overview":
		return overview()
	case "POST /measure-now":
		return measureNow(r)
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

func reply(status int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

// fail logs err and answers 500 without details.
func fail(err error) (sdk.HTTPResponse, error) {
	logf("error", "pagespeed: %v", err)
	return reply(http.StatusInternalServerError, map[string]string{"error": "the request failed; see the server log"})
}

type value struct {
	Value float64 `json:"value"`
	Cat   string  `json:"cat"`
}

// metricValue is nil (JSON null) when the metric was not reported.
func metricValue(v *float64, cat string) *value {
	if v == nil {
		return nil
	}
	return &value{*v, cat}
}

type point struct {
	Day string  `json:"day"`
	V   float64 `json:"v"`
}

type deviceRow struct {
	Day         string             `json:"day"`
	LCP         *value             `json:"lcp"`
	INP         *value             `json:"inp"`
	CLS         *value             `json:"cls"`
	FieldStatus string             `json:"field_status"`
	LabScore    *int               `json:"lab_score"`
	LabStatus   string             `json:"lab_status"`
	MeasuredAt  string             `json:"measured_at"`
	Series      map[string][]point `json:"series"`
}

// urlEntry is {"path": ..., "<strategy>": deviceRow...}.
func urlEntry(path string, strategies []string, rows []metricRow, all []series) map[string]any {
	entry := map[string]any{"path": path}
	for _, s := range strategies {
		d := deviceRow{FieldStatus: "pending", LabStatus: labPending, Series: map[string][]point{"lcp": {}, "inp": {}, "cls": {}}}
		for _, r := range rows {
			if r.URL == path && r.Strategy == s {
				d.Day, d.FieldStatus, d.LabScore, d.LabStatus, d.MeasuredAt = r.Day, r.FieldStatus, r.LabScore, r.LabStatus, r.MeasuredAt
				d.LCP, d.INP, d.CLS = metricValue(r.LCP, r.LCPCat), metricValue(r.INP, r.INPCat), metricValue(r.CLS, r.CLSCat)
			}
		}
		for _, se := range all {
			if se.URL != path || se.Strategy != s {
				continue
			}
			for _, p := range se.Points {
				for name, v := range map[string]*float64{"lcp": p.LCP, "inp": p.INP, "cls": p.CLS} {
					if v != nil {
						d.Series[name] = append(d.Series[name], point{p.Day, *v})
					}
				}
			}
		}
		entry[s] = d
	}
	return entry
}

func overview() (sdk.HTTPResponse, error) {
	defer startBudget()()
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	targets, _, originOK, unavailable := resolveTargets(s)
	day := today()
	since := now().UTC().AddDate(0, 0, -seriesDays).Format(dateFmt)
	rows, err := latestRows()
	if err != nil {
		return fail(err)
	}
	all, err := seriesFor(targets, since)
	if err != nil {
		return fail(err)
	}
	origins, err := latestOrigin()
	if err != nil {
		return fail(err)
	}
	done, at, err := dayCounts(day)
	if err != nil {
		return fail(err)
	}
	strategies := s.strategies()
	notices := []string{}
	if s.APIKey == "" {
		notices = append(notices, "no_api_key")
	}
	if !originOK {
		notices = append(notices, "no_site_address")
	}
	if unavailable {
		notices = append(notices, "entities_unavailable")
	}
	urls := make([]map[string]any, 0, len(targets))
	stored := false
	for _, p := range targets {
		urls = append(urls, urlEntry(p, strategies, rows, all))
	}
	for _, r := range rows {
		for _, p := range targets {
			stored = stored || r.URL == p
		}
	}
	if !stored && len(notices) == 0 {
		notices = append(notices, "no_data")
	}
	origin := map[string]any{}
	for _, o := range origins {
		origin[o.Strategy] = map[string]any{
			"status": o.Status,
			"lcp":    metricValue(o.LCP, o.LCPCat), "inp": metricValue(o.INP, o.INPCat), "cls": metricValue(o.CLS, o.CLSCat),
		}
	}
	th := map[string][2]float64{}
	for k, v := range thresholds {
		th[k] = v
	}
	return reply(http.StatusOK, map[string]any{
		"strategies":       strategies,
		"default_strategy": defaultStrategy(strategies),
		"notices":          notices,
		"thresholds":       th,
		"origin":           origin,
		"urls":             urls,
		"last_run":         map[string]any{"day": day, "done": done, "total": len(targets) * len(strategies), "at": at},
	})
}

func defaultStrategy(strategies []string) string {
	for _, s := range strategies {
		if s == strategyMobile {
			return s
		}
	}
	return strategies[0]
}

func measureNow(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	if len(r.Body) > maxBodyBytes {
		return reply(http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
	}
	var in struct {
		URL string `json:"url"`
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(r.Body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.More() || !strings.HasPrefix(in.URL, "/") {
		return reply(http.StatusBadRequest, map[string]string{"error": `send {"url": "/path"}`})
	}
	defer startBudget()()
	p, err := loadPlan()
	if errors.Is(err, errNoKey) || errors.Is(err, errNoOrigin) {
		return reply(http.StatusConflict, map[string]string{"error": err.Error()})
	}
	if err != nil {
		return fail(err)
	}
	tracked := false
	for _, t := range p.targets {
		tracked = tracked || t == in.URL
	}
	if !tracked {
		return reply(http.StatusBadRequest, map[string]string{"error": "this page is not tracked"})
	}
	if err := measureOne(p, in.URL); err != nil {
		return fail(err)
	}
	rows, err := dayRows(today(), in.URL)
	if err != nil {
		return fail(err)
	}
	all, err := seriesFor([]string{in.URL}, now().UTC().AddDate(0, 0, -seriesDays).Format(dateFmt))
	if err != nil {
		return fail(err)
	}
	return reply(http.StatusOK, urlEntry(in.URL, p.strategies, rows, all))
}
