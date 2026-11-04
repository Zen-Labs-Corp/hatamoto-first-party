package main

import (
	"sort"
	"time"
)

const googleLimit = 5

// selectReviews keeps the reviews rated min or higher, newest first, at most
// limit of them and never more than maxItemsCap. A limit below 1 means the cap.
func selectReviews(all []review, minRating, limit int) []review {
	if limit < 1 || limit > maxItemsCap {
		limit = maxItemsCap
	}
	out := make([]review, 0, min(len(all), limit))
	for _, r := range all {
		if r.Rating >= minRating {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Published.After(out[j].Published) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

var sourceLabels = map[string]string{sourceGoogle: "Google", sourceTrustpilot: "Trustpilot"}

type overviewSource struct {
	Source      string  `json:"source"`
	Label       string  `json:"label"`
	State       string  `json:"state"` // connected, pending, not_set_up or error
	Rating      float64 `json:"rating,omitempty"`
	Count       int     `json:"count,omitempty"`
	ProfileURL  string  `json:"profile_url,omitempty"`
	RefreshedAt string  `json:"refreshed_at,omitempty"`
	LastError   string  `json:"last_error,omitempty"`
	Missing     string  `json:"missing,omitempty"`
}

type overviewReview struct {
	Author      string `json:"author"`
	Source      string `json:"source"`
	SourceLabel string `json:"source_label"`
	Rating      int    `json:"rating"`
	Date        string `json:"date"`
	Text        string `json:"text"`
	URL         string `json:"url,omitempty"`
}

type overview struct {
	Sources     []overviewSource `json:"sources"`
	MinRating   int              `json:"min_rating"`
	MaxItems    int              `json:"max_items"`
	GoogleLimit int              `json:"google_limit"`
	Reviews     []overviewReview `json:"reviews"`
	Error       string           `json:"error,omitempty"`
}

// missingText names what a source still needs, "" when it has everything.
func missingText(src string, s settings) string {
	var key, id bool
	var idName string
	if src == sourceGoogle {
		key, id, idName = s.GoogleKey != "", s.GooglePlace != "", "place ID"
	} else {
		key, id, idName = s.TPKey != "", s.TPUnit != "", "business unit ID"
	}
	switch {
	case key && id:
		return ""
	case key:
		return idName
	case id:
		return "API key"
	}
	return "API key and " + idName
}

// buildOverview never fails: a settings or db problem gives an empty overview
// with a generic error text.
func buildOverview() overview {
	s, serr := loadSettings()
	o := overview{Sources: []overviewSource{}, Reviews: []overviewReview{}, MinRating: s.MinRating, MaxItems: s.MaxItems, GoogleLimit: googleLimit}
	known, err := loadSources()
	if err == nil {
		var all []review
		if all, err = loadReviews(); err == nil {
			for _, r := range selectReviews(all, s.MinRating, s.MaxItems) {
				o.Reviews = append(o.Reviews, overviewReview{Author: r.Author, Source: r.Source, SourceLabel: sourceLabels[r.Source],
					Rating: r.Rating, Date: r.Published.UTC().Format("2006-01-02"), Text: r.Body, URL: r.Link})
			}
		}
	}
	if err != nil || serr != nil {
		o.Error = "temporarily unavailable"
		return o
	}
	for _, src := range []string{sourceGoogle, sourceTrustpilot} {
		info := known[src]
		os := overviewSource{Source: src, Label: sourceLabels[src]}
		on := (src == sourceGoogle && s.googleOn()) || (src == sourceTrustpilot && s.tpOn())
		switch {
		case !on:
			os.State, os.Missing = "not_set_up", missingText(src, s)
		case info.LastError != "":
			os.State = "error"
		case info.OKAt.IsZero():
			os.State = "pending"
		default:
			os.State = "connected"
		}
		if on {
			os.Rating, os.Count, os.ProfileURL = info.Rating, info.Count, info.ProfileURL
			os.LastError = info.LastError
			if !info.OKAt.IsZero() {
				os.RefreshedAt = info.OKAt.UTC().Format(time.RFC3339)
			}
			if os.State == "error" && !info.AttemptedAt.IsZero() {
				os.RefreshedAt = info.AttemptedAt.UTC().Format(time.RFC3339)
			}
		}
		o.Sources = append(o.Sources, os)
	}
	return o
}
