package main

import (
	"sort"
	"time"

	"hatamoto.dev/sdk"
)

const (
	// tickBudget is how much of the 10 s hook timeout a tick plans to use.
	tickBudget = 8 * time.Second
	// tickMargin stays unspent so a late answer cannot cost the whole tick.
	tickMargin = 500 * time.Millisecond
	// maxRequestMS caps one check's timeout.
	maxRequestMS = 8000
	// minRequestMS: with less time than this a check would report a timeout
	// that says nothing about the site, so the tick ends instead.
	minRequestMS = 2000

	// dueSlack: the host's ticker fires every period, but a check ends some
	// seconds after its tick began and next_check is set from that end. Without
	// slack a 1m monitor would miss every second tick.
	dueSlack = 30 * time.Second

	cursorKey = "check_cursor"
)

// runCheckTick is the "check" cron job. It never returns an error: a problem
// is logged and the next tick tries again, so the host does not count it as a
// plugin failure.
func runCheckTick(sdk.CronEvent) error {
	checkTick()
	return nil
}

func checkTick() {
	start := now()
	deadline := start.Add(tickBudget)
	st := loadSettings()
	due, err := dueMonitors(start.Add(dueSlack))
	if err != nil {
		logf("warn", "uptime-monitor: monitors unavailable code=%s", hostErrCode(err))
		return
	}
	due = fromCursor(due, loadCursor())

	var site *monitor
	var siteResult result
	for _, m := range due {
		timeout := min(maxRequestMS, int(deadline.Sub(now())/time.Millisecond)-int(tickMargin/time.Millisecond))
		if timeout < minRequestMS {
			break
		}
		r := checkMonitor(m, timeout)
		updated, err := applyResult(m, r, st)
		if err != nil {
			logf("warn", "uptime-monitor: check not recorded code=%s", hostErrCode(err))
			break
		}
		if err := kvSet(cursorKey, m.ID); err != nil {
			logf("warn", "uptime-monitor: cursor not saved code=%s", hostErrCode(err))
		}
		if updated.IsSite && !r.Skip {
			site, siteResult = &updated, r
		}
	}

	// The heartbeat has a bounded timeout and is the owner's dead-man signal:
	// it goes first. Mail only starts with time to spare (see flushOutbox).
	if site != nil {
		maybeHeartbeat(*site, siteResult, st, deadline.Sub(now()))
	}
	if deadline.Sub(now()) < mailMinRemaining {
		return
	}
	if err := flushOutbox(deadline); err != nil {
		logf("warn", "uptime-monitor: mail queue failed code=%s", hostErrCode(err))
	}
}

// loadCursor returns the id of the monitor checked last, 0 when unknown.
func loadCursor() int64 {
	var id int64
	if _, err := kvGet(cursorKey, &id); err != nil {
		logf("warn", "uptime-monitor: cursor unreadable code=%s", hostErrCode(err))
		return 0
	}
	return id
}

// fromCursor orders due monitors most overdue first. Monitors due at the same
// moment go round robin: those after the cursor id come first.
func fromCursor(due []monitor, cursor int64) []monitor {
	out := append([]monitor(nil), due...)
	rank := func(m monitor) int64 {
		if m.ID > cursor {
			return m.ID
		}
		return m.ID + 1<<40
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].NextCheck.Equal(out[j].NextCheck) {
			return out[i].NextCheck.Before(out[j].NextCheck)
		}
		return rank(out[i]) < rank(out[j])
	})
	return out
}
