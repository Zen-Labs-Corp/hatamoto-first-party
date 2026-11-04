package main

import "time"

// cacheTTL is how long the selected reviews are reused, so one page render
// that calls several template funcs does a single db read.
var cacheTTL = 60 * time.Second

// snapshot is what the builder source and the template funcs show: the
// reviews already filtered by min_rating, newest first and capped by
// max_items, plus the per-source summary.
type snapshot struct {
	Reviews []review
	Sources map[string]sourceInfo
}

var (
	cached   snapshot
	cachedAt time.Time
	cacheOK  bool
)

// current returns the snapshot. Any error gives an empty snapshot (and is not
// cached), so every caller fails open.
func current() snapshot {
	if cacheOK && now().Sub(cachedAt) < cacheTTL {
		return cached
	}
	empty := snapshot{Sources: map[string]sourceInfo{}}
	s, err := loadSettings()
	if err != nil {
		return empty
	}
	all, err := loadReviews()
	if err != nil {
		return empty
	}
	src, err := loadSources()
	if err != nil {
		return empty
	}
	// A source that is not set up is hidden at once; its rows stay for retention.
	var shown []review
	for _, r := range all {
		if s.sourceOn(r.Source) {
			shown = append(shown, r)
		}
	}
	for name := range src {
		if !s.sourceOn(name) {
			delete(src, name)
		}
	}
	cached = snapshot{Reviews: selectReviews(shown, s.MinRating, s.MaxItems), Sources: src}
	cachedAt, cacheOK = now(), true
	return cached
}

// invalidateCache drops the snapshot; a refresh calls it after writing.
func invalidateCache() { cacheOK = false }
