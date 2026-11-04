package main

import (
	"time"
)

const (
	// refreshAfter is how old a source's last attempt must be before the
	// hourly cron fetches it again.
	refreshAfter = 23 * time.Hour
	// keepFor is how long a source's reviews outlive its last successful refresh.
	keepFor = 30 * 24 * time.Hour
	// runBudget is the time a refresh plans for inside the 10 s cron limit.
	runBudget = 9 * time.Second
	// minForFetch is the least time left for which a source is started.
	minForFetch = 4 * time.Second
)

// invalidate drops the cached view of the data after a change.
var invalidate = func() {}

type sourceFetcher struct {
	name string
	on   func(settings) bool
	get  func(settings, time.Time) (sourceInfo, []review, error)
	keys func(settings) []string
}

var fetchers = []sourceFetcher{
	{sourceGoogle, settings.googleOn, fetchGoogle, func(s settings) []string { return []string{s.GoogleKey, s.GooglePlace} }},
	{sourceTrustpilot, settings.tpOn, fetchTrustpilot, func(s settings) []string { return []string{s.TPKey, s.TPUnit} }},
}

// refreshAll fetches every configured source that is due (all of them when
// force is set), stores the result per source and then applies retention. One
// source failing never touches the other's data. Errors are stored redacted
// and returned only for the caller to ignore: nothing here may fail a request.
func refreshAll(force bool) error {
	defer invalidate()
	s, err := loadSettings()
	if err != nil {
		logf("error", "reviews: settings unavailable: %v", err)
		return err
	}
	known, err := loadSources()
	if err != nil {
		logf("error", "reviews: cannot read the source state: %v", err)
		return err
	}
	deadline := now().Add(runBudget)
	for _, f := range fetchers {
		if !f.on(s) {
			continue
		}
		prev, seen := known[f.name]
		if !force && seen && !prev.AttemptedAt.IsZero() && now().Sub(prev.AttemptedAt) < refreshAfter {
			continue
		}
		if deadline.Sub(now()) < minForFetch {
			break
		}
		info, rs, err := f.get(s, deadline)
		if err != nil {
			msg := redact(err.Error(), f.keys(s)...)
			logf("error", "reviews: %s refresh failed: %s", f.name, msg)
			if err := recordFailure(f.name, msg); err != nil {
				logf("error", "reviews: cannot store the %s error: %v", f.name, err)
			}
			continue
		}
		if err := replaceSource(f.name, info, rs); err != nil {
			logf("error", "reviews: cannot store %s reviews: %v", f.name, err)
		}
	}
	return enforceRetention()
}

// enforceRetention deletes the data of a source whose last successful refresh
// is older than keepFor, configured or not.
func enforceRetention() error {
	known, err := loadSources()
	if err != nil {
		return err
	}
	for name, info := range known {
		if info.OKAt.IsZero() || now().Sub(info.OKAt) <= keepFor {
			continue
		}
		if err := deleteSource(name); err != nil {
			logf("error", "reviews: cannot delete old %s data: %v", name, err)
			return err
		}
	}
	return nil
}
