package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	// budget is the share of the 10 s cron timeout a run may use; margin is
	// kept free behind every request.
	budget = 8 * time.Second
	margin = 500 * time.Millisecond
	// extractBudget is how long of the budget reading entities may take, so
	// checks always get the rest.
	extractBudget = 3 * time.Second
	// minRequest is the shortest timeout worth starting a request with.
	minRequest = time.Second
	// hostGap is the minimum time between two requests to one host in a run.
	hostGap = time.Second

	entitiesPerRun = 200
	listLimit      = 50
	internalBatch  = 200
	outboundBatch  = 100

	cursorKey   = "scan:cursor"
	lastScanKey = "scan:last"
)

func init() {
	guest.OnCron("scan", func(sdk.CronEvent) error {
		runScan()
		return nil
	})
}

// entityWire is the part of an entity the plugin reads (the SDK has no type
// for host results).
type entityWire struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Path       string          `json:"path"`
	URL        string          `json:"url"`
	Attributes json.RawMessage `json:"attributes"`
}

// scanState is the checkpoint of the pass over all published entities.
type scanState struct {
	Cursor    string `json:"cursor"`
	PassStart string `json:"pass_start"`
	PassTotal int    `json:"pass_total"`
}

// runScan is one bounded cron run: forget old data, read one batch of
// entities, then check the links that are due until the budget ends. It never
// returns an error: a host problem is logged and the run ends, so the next run
// starts clean (every step is idempotent).
func runScan() {
	deadline := now().Add(budget)
	s := loadSettings()
	// Without the database there is nothing to do and nothing to report to.
	if _, err := queueSize(); err != nil {
		logf("warn", "scan skipped, database not available: %v", err)
		return
	}
	if err := purge(now()); err != nil {
		logf("warn", "purge: %v", err)
	}
	extractBatch(s, now().Add(extractBudget))
	checkDue(s, deadline)
	markScan()
}

// markScan records the time of the last run for the admin page.
func markScan() {
	if err := kvSet(lastScanKey, ts(now())); err != nil {
		logf("warn", "last scan time: %v", err)
	}
}

// indexEntity stores the links of one entity and remembers its path (and its
// parents') as a known page path.
func indexEntity(e entityWire, exclude []string, origin string) error {
	refs := extractLinks(e.Attributes, origin, exclude)
	if err := replacePageLinks(pageRef{ID: e.ID, Name: e.Name, Path: e.Path}, refs); err != nil {
		return err
	}
	if e.Path == "" {
		return nil
	}
	return upsertKnownPaths(pathAndParents(e.Path))
}

// extractBatch reads up to entitiesPerRun published entities from the stored
// cursor. The cursor is saved after every page, so a failure repeats at most
// that page. When the list ends the cursor is cleared and the next run starts
// a new pass.
func extractBatch(s settings, until time.Time) {
	var st scanState
	if _, err := kvGet(cursorKey, &st); err != nil {
		logf("warn", "scan cursor unreadable, starting a pass: %v", err)
		st = scanState{}
	}
	if st.PassStart == "" {
		st.PassStart = ts(now())
	}
	limit, origin := listLimit, ""
	for done := 0; done < entitiesPerRun && now().Before(until); {
		args := map[string]any{"status": "published", "limit": limit}
		if st.Cursor != "" {
			args["cursor"] = st.Cursor
		}
		var res struct {
			Items      []entityWire `json:"items"`
			NextCursor string       `json:"next_cursor"`
		}
		if err := callHost(sdk.FnEntitiesList, args, &res); err != nil {
			var he *sdk.HostError
			if errors.As(err, &he) && he.Code == sdk.ErrInvalidArgument && limit > 1 {
				limit /= 2 // the answer was too big (2 MiB cap)
				continue
			}
			logf("warn", "listing entities: %v", err)
			return
		}
		for _, e := range res.Items {
			if o := siteOrigin(e.URL, e.Path); o != "" {
				origin = o
			}
			if err := indexEntity(e, s.Exclude, origin); err != nil {
				logf("warn", "storing links of one page: %v", err)
				return // the cursor stays: this page is read again next run
			}
		}
		done += len(res.Items)
		st.PassTotal += len(res.Items)
		st.Cursor = res.NextCursor
		if st.Cursor == "" {
			if err := kvDelete(cursorKey); err != nil {
				logf("warn", "clearing scan cursor: %v", err)
			}
			return
		}
		if err := kvSet(cursorKey, st); err != nil {
			logf("warn", "saving scan cursor: %v", err)
			return
		}
	}
}

// checkDue checks the links that are due: internal ones first (no network),
// then outbound ones until the budget ends.
func checkDue(s settings, deadline time.Time) {
	rc := &redirectCache{}
	internal, err := dueLinks(kindInternal, internalBatch)
	if err != nil {
		logf("warn", "due links: %v", err)
		return
	}
	for _, l := range internal {
		if deadline.Sub(now()) < margin {
			return
		}
		record(l, checkInternal(l.URL, rc), s)
	}
	outbound, err := dueLinks(kindOutbound, outboundBatch)
	if err != nil {
		logf("warn", "due links: %v", err)
		return
	}
	checkOutboundLinks(outbound, s, deadline)
}

// checkOutboundLinks sends one request per link, at most one per host per
// hostGap. A link whose host was used too recently is skipped (it stays due
// and goes first next run) and other hosts go first; skipped links are tried
// again while that still makes progress. No request starts when less than
// minRequest would be left for it after the margin.
func checkOutboundLinks(queue []dueLink, s settings, deadline time.Time) {
	lastUse := map[string]time.Time{}
	for len(queue) > 0 {
		var skipped []dueLink
		progressed := false
		for _, l := range queue {
			timeout := min(time.Duration(s.TimeoutMS)*time.Millisecond, deadline.Sub(now())-margin)
			if timeout < minRequest {
				return
			}
			host := hostOf(l.URL)
			if t, ok := lastUse[host]; ok && now().Sub(t) < hostGap {
				skipped = append(skipped, l)
				continue
			}
			record(l, checkOutbound(l.URL, timeout, l.FailCount), s)
			lastUse[host] = now()
			progressed = true
		}
		if !progressed {
			return
		}
		queue = skipped
	}
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	return strings.ToLower(u.Host)
}

// record stores a check result and schedules the next check: after the
// result's own delay, or recheck_days.
func record(l dueLink, r result, s settings) {
	if r.Keep {
		return
	}
	delay := r.Delay
	if delay == 0 {
		delay = time.Duration(s.RecheckDays) * 24 * time.Hour
	}
	if err := markResult(l.ID, r.Status, r.HTTPStatus, r.Err, r.FailCount, now().Add(delay)); err != nil {
		logf("warn", "storing a check result: %v", err)
	}
}
