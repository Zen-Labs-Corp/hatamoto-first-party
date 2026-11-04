package main

import "time"

const (
	// refreshEvery is how long after its last attempt a feed is due again.
	refreshEvery = 30 * time.Minute
	// startCutoff is the run time after which no new feed is started, and
	// hardBudget the time by which every request must have finished: both
	// leave room for the database writes inside the 10 s cron limit.
	startCutoff = 7 * time.Second
	hardBudget  = 9 * time.Second
)

// feedResult is the outcome of one feed in a run, for the fetch-now route.
type feedResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// statusDeferred is reported for a feed the time budget left for the next run.
const statusDeferred = "waiting for the next run"

// refresh fetches the due feeds (every configured feed when force is set),
// oldest attempt first, within the time budget; feeds it had no time for are
// marked due and go first next time. A failing feed only affects itself: its
// failure is stored as a fixed phrase and the others carry on. refresh never
// returns an error, so neither the cron job nor the route can fail on it.
func refresh(force bool) []feedResult {
	start := now()
	cfg, err := loadFeeds()
	if err != nil {
		logf("error", "feed-reader: settings unavailable")
		return nil
	}
	if err := syncFeeds(cfg); err != nil {
		logf("error", "feed-reader: cannot sync the feed list")
		return nil
	}
	rows, err := feedRows()
	if err != nil {
		logf("error", "feed-reader: cannot read the feed state")
		return nil
	}
	byKey := make(map[string]feedRow, len(rows))
	for _, r := range rows {
		byKey[r.Key] = r
	}
	every := refreshEvery
	if force {
		every = 0
	}
	due := dueFeeds(cfg, rows, start, every)

	var results []feedResult
	deadline := start.Add(hardBudget)
	for i, f := range due {
		if now().Sub(start) >= startCutoff || deadline.Sub(now()) < minCall {
			var rest []string
			for _, d := range due[i:] {
				rest = append(rest, d.Key)
				results = append(results, feedResult{Name: d.Name, Status: statusDeferred})
			}
			if err := markDue(rest); err != nil {
				logf("error", "feed-reader: cannot reschedule feeds")
			}
			break
		}
		results = append(results, feedResult{Name: f.Name, Status: refreshOne(f, byKey[f.Key], deadline)})
	}
	return results
}

// refreshOne fetches one feed and stores the result, returning the status.
// The attempt time is saved on every outcome, so a failing feed is retried
// after the normal interval and not on every tick.
func refreshOne(f feedCfg, st feedRow, deadline time.Time) string {
	out, err := fetchFeed(f, st, deadline)
	status, etag, lastMod, siteURL, count := statusOK, st.ETag, st.LastModified, st.SiteURL, st.ItemCount
	switch {
	case err != nil:
		status = err.Error()
		logf("warn", "feed-reader: feed %q: %s", f.Name, status)
	case out.NotModified:
		status = statusNotModified
	default:
		etag, lastMod = out.ETag, out.LastModified
		if out.Feed.SiteURL != "" {
			siteURL = out.Feed.SiteURL
		}
		if n, serr := storeItems(f, out.Feed.Items); serr != nil {
			status = string(statusFailed)
			logf("error", "feed-reader: cannot store the items of feed %q", f.Name)
		} else {
			count = n
		}
	}
	if err := saveState(f.Key, status, etag, lastMod, siteURL, count, now()); err != nil {
		logf("error", "feed-reader: cannot store the state of feed %q", f.Name)
	}
	return status
}

// storeItems upserts the items, applies retention and returns the number of
// items the feed now has. Upserting first means a failure half way loses
// nothing; the next run repeats it.
func storeItems(f feedCfg, items []item) (int, error) {
	if err := upsertItems(f.Key, items); err != nil {
		return 0, err
	}
	if err := prune(f.Key, f.MaxItems, now()); err != nil {
		return 0, err
	}
	return countItems(f.Key)
}
