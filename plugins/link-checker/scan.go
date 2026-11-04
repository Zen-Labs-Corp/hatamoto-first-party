package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
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

	// maxListRetries is how many runs in a row may fail to list one entity
	// before the pass starts over.
	maxListRetries = 3
	// maxRestarts is how many times the list may start over because of the
	// same trouble before the pass is ended as completed, with what is
	// stuck counted as skipped: otherwise "no page" would never be final.
	maxRestarts = 3
	// passInterval is the least time between the end of a pass and the start
	// of the next: a published page is read at once by its hook, so reading
	// every page again right away only churns the database.
	passInterval = time.Hour

	entitiesPerRun = 200
	listLimit      = 50
	internalBatch  = 200
	outboundBatch  = 100

	cursorKey   = "scan:cursor"
	lastScanKey = "scan:last"
	doneKey     = "scan:done"
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
	Locale     string          `json:"locale"`
	Attributes json.RawMessage `json:"attributes"`
}

// scanState is the checkpoint of the pass over all published entities.
type scanState struct {
	Cursor    string `json:"cursor"`
	PassStart string `json:"pass_start"`
	PassTotal int    `json:"pass_total"`
	// Locales are the locale tags of the entities this pass has seen.
	Locales []string `json:"locales"`
	// Retries counts consecutive runs where listing failed with the smallest
	// batch; the pass restarts after maxListRetries.
	Retries int `json:"retries"`
	// Restarts counts the restarts caused by Retries in this pass.
	Restarts int `json:"restarts"`
}

// passDone is kept in kv when a pass has indexed every published page: from
// then on "no page" is a final answer, and the locale segments are known.
type passDone struct {
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at"`
	Locales    []string `json:"locales"`
	// Partial marks a pass that was given up before every page was listed.
	// It is not a completed pass: it drops no paths, does not allow purge
	// and "no page" stays undecided.
	Partial bool `json:"partial,omitempty"`
	// Skipped is the number of entities the pass could not list and gave up on.
	Skipped int `json:"skipped,omitempty"`
}

// loadPass reads the result of the last completed pass; a read error counts as
// "no pass yet", which only delays verdicts.
func loadPass() passInfo {
	var d passDone
	ok, err := kvGet(doneKey, &d)
	if err != nil {
		logf("warn", "pass state unreadable: %v", err)
		return passInfo{}
	}
	return passInfo{Done: ok && d.FinishedAt != "", StartedAt: d.StartedAt, FinishedAt: d.FinishedAt, Locales: d.Locales, Partial: d.Partial}
}

// recent reports whether the last completed pass started within the
// retention period before at.
func (p passInfo) recent(at time.Time) bool {
	started, err := time.Parse(time.RFC3339, p.StartedAt)
	return p.Done && !p.Partial && err == nil && started.After(at.Add(-retention))
}

// addLocale remembers a locale tag and its language part, lowercased.
func (st *scanState) addLocale(tag string) {
	tag = strings.ToLower(tag)
	for _, l := range []string{tag, strings.SplitN(tag, "-", 2)[0]} {
		if l != "" && !slices.Contains(st.Locales, l) {
			st.Locales = append(st.Locales, l)
		}
	}
}

// localeSegment matches a URL segment that can be a locale prefix: "fr",
// "en-gb".
var localeSegment = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]{2,4})?$`)

// addSegment remembers the first path segment of a localised entity when it
// looks like a locale prefix: the URL segment can differ from the locale tag
// (tag "en-XX", segment "xx").
func (st *scanState) addSegment(locale, p string) {
	seg, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	seg = strings.ToLower(seg)
	if locale != "" && localeSegment.MatchString(seg) && !slices.Contains(st.Locales, seg) {
		st.Locales = append(st.Locales, seg)
	}
}

// finish records the end of a pass that listed every page and clears the
// cursor.
func (st scanState) finish() {
	if err := dropStaleKnownPaths(st.PassStart); err != nil {
		logf("warn", "dropping stale known paths: %v", err)
	}
	st.record(passDone{StartedAt: st.PassStart, FinishedAt: ts(now()), Locales: st.Locales})
}

// finishPartial ends a pass that was given up. The pages after the one that
// cannot be listed were not seen, so their paths must not be dropped.
func (st scanState) finishPartial(skipped int) {
	st.record(passDone{StartedAt: st.PassStart, FinishedAt: ts(now()), Locales: st.Locales, Partial: true, Skipped: skipped})
}

func (st scanState) record(d passDone) {
	if err := kvSet(doneKey, d); err != nil {
		logf("warn", "saving pass result: %v", err)
	}
	if err := kvDelete(cursorKey); err != nil {
		logf("warn", "clearing scan cursor: %v", err)
	}
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
	if err := purge(now(), loadPass().recent(now())); err != nil {
		logf("warn", "purge: %v", err)
	}
	if err := removeExcluded(s.Exclude); err != nil {
		logf("warn", "removing excluded links: %v", err)
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

// indexEntity stores the links of one entity and remembers the parents of its
// path as known listing paths; its own path is kept apart, so a link to it
// resolves even where entities.get does not take a locale prefix.
func indexEntity(e entityWire, exclude []string, origin string) error {
	refs := extractLinks(e.Attributes, origin, exclude)
	if err := replacePageLinks(pageRef{ID: e.ID, Name: e.Name, Path: e.Path}, refs); err != nil {
		return err
	}
	if e.Path != "" {
		if err := upsertPagePath(e.Path); err != nil {
			return err
		}
	}
	parents := parentPaths(e.Path)
	if len(parents) == 0 {
		return nil
	}
	return upsertKnownPaths(parents)
}

// extractBatch reads up to entitiesPerRun published entities from the stored
// cursor. The cursor is saved after every page, so a failure repeats at most
// that page. When the list ends the cursor is cleared and the next run starts
// a new pass.
func extractBatch(s settings, until time.Time) {
	var st scanState
	found, err := kvGet(cursorKey, &st)
	if err != nil {
		logf("warn", "scan cursor unreadable, starting a pass: %v", err)
		st, found = scanState{}, true
	}
	if !found {
		// No pass is running: start the next one only after passInterval.
		if fin, err := time.Parse(time.RFC3339, loadPass().FinishedAt); err == nil && now().Sub(fin) < passInterval {
			return
		}
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
			if errors.As(err, &he) && he.Code == sdk.ErrInvalidArgument {
				if limit > 1 {
					limit /= 2 // the answer was too big (2 MiB cap)
					continue
				}
				// Even one entity fails (too big, or a stale cursor): retry a
				// few runs, then start the pass over.
				if st.Retries++; st.Retries >= maxListRetries {
					if st.Restarts++; st.Restarts >= maxRestarts {
						logf("error", "listing entities keeps failing at one entity, ending the pass as partial: %v", err)
						st.finishPartial(1)
						return
					}
					logf("error", "listing entities keeps failing at one entity, restarting the pass: %v", err)
					// Keep the start and the locales: what the attempts
					// refreshed must survive the stale-path cleanup.
					st = scanState{PassStart: st.PassStart, Locales: st.Locales, Restarts: st.Restarts}
				}
				if err := kvSet(cursorKey, st); err != nil {
					logf("warn", "saving scan cursor: %v", err)
				}
				return
			}
			logf("warn", "listing entities: %v", err)
			return
		}
		failed := 0
		for _, e := range res.Items {
			st.addLocale(e.Locale)
			st.addSegment(e.Locale, e.Path)
			if o := siteOrigin(e.URL, e.Path); o != "" {
				origin = o
			}
			if err := indexEntity(e, s.Exclude, origin); err != nil {
				// Skip it: one bad page must not stop the pass. The next
				// pass tries it again.
				if failed++; failed == 1 {
					logf("warn", "storing links of page %s skipped: %v", e.ID, err)
				}
			}
		}
		if failed > 1 {
			logf("warn", "%d more pages skipped in this batch", failed-1)
		}
		done += len(res.Items)
		st.PassTotal += len(res.Items)
		st.Retries = 0
		st.Cursor = res.NextCursor
		if st.Cursor == "" {
			st.finish()
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
	rc, pass := &redirectCache{}, loadPass()
	internal, err := dueLinks(kindInternal, internalBatch)
	if err != nil {
		logf("warn", "due links: %v", err)
		return
	}
	for _, l := range internal {
		if deadline.Sub(now()) < margin {
			return
		}
		record(l, checkInternal(l.URL, rc, pass), s)
	}
	if !outboundGranted() {
		logf("warn", "outbound checks skipped: http.fetch and http.fetch.any must both be granted")
		return
	}
	outbound, err := dueLinks(kindOutbound, outboundBatch)
	if err != nil {
		logf("warn", "due links: %v", err)
		return
	}
	checkOutboundLinks(outbound, s, deadline)
}

// checkOutboundLinks sends one request per link, at most one per host per
// hostGap. A link whose host was used too recently is skipped for now and
// other hosts go first; when only such links are left the run ends and the
// next run (5 minutes later) takes them. It never sleeps: the host does not
// really suspend a guest, so a wait would only spin a CPU core. No request
// starts when less than minRequest would be left for it after the margin.
func checkOutboundLinks(queue []dueLink, s settings, deadline time.Time) {
	lastUse := map[string]time.Time{}
	for len(queue) > 0 {
		var skipped []dueLink
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
			record(l, checkOutbound(l.URL, timeout, deadline, l.FailCount), s)
			lastUse[host] = now()
		}
		if len(skipped) == len(queue) {
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
// result's own delay, else after a day for a broken or unknown link (it may
// have been fixed, or the failure may have been a blip) and recheck_days for
// the rest.
func record(l dueLink, r result, s settings) {
	if r.Keep {
		return
	}
	delay := r.Delay
	if delay == 0 && (r.Status == statusBroken || r.Status == statusUnknown) {
		delay = retryLater
	}
	if delay == 0 {
		delay = time.Duration(s.RecheckDays) * 24 * time.Hour
	}
	if err := markResult(l.ID, r.Status, r.HTTPStatus, r.Err, r.FailCount, now().Add(delay)); err != nil {
		logf("warn", "storing a check result: %v", err)
	}
}
