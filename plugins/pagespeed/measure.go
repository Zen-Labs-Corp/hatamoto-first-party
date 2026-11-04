package main

import (
	"errors"
	"time"
)

const (
	budget     = 8 * time.Second // of the 10 s a call may take
	minForCrux = 2 * time.Second // least time left to start a field lookup
	minForPSI  = 6 * time.Second // least time left to start a lab run
)

var (
	errNoKey    = errors.New("add an API key in Settings to load field data")
	errNoOrigin = errors.New("set the site address in Settings > General or the Public URL setting")
)

// plan is what one call needs: the settings, the tracked paths and the origin.
type plan struct {
	s          settings
	targets    []string
	origin     string
	strategies []string
}

// loadPlan reads the settings and resolves the pages and the site origin.
// errNoKey and errNoOrigin mean there is nothing to measure yet.
func loadPlan() (plan, error) {
	s, err := loadSettings()
	if err != nil {
		return plan{}, err
	}
	if s.APIKey == "" {
		return plan{}, errNoKey
	}
	targets, origin, ok, _ := resolveTargets(s)
	if !ok {
		return plan{}, errNoOrigin
	}
	return plan{s, targets, origin, s.strategies()}, nil
}

// startBudget sets the deadline of the running call; the returned func clears it.
func startBudget() func() {
	budgetEnd = now().Add(budget)
	return func() { budgetEnd = time.Time{} }
}

func left() time.Duration { return budgetEnd.Sub(now()) }

func today() string { return now().UTC().Format(dateFmt) }

// measureStep does as much of today's work as the budget allows, with at most
// one lab run, and reports whether this call completed the day (a day already
// complete returns false at once). It resumes from the
// checkpoint in the db, so every call continues where the last one stopped.
func measureStep() (bool, error) {
	defer startBudget()()
	day := today() // once per call: a day rollover mid-run changes nothing
	if done, err := dayDone(day); done || err != nil {
		return false, err
	}
	p, err := loadPlan()
	if err != nil {
		return false, err
	}
	key := p.s.APIKey
	for left() >= minForCrux {
		done, err := originDone(day)
		if err != nil {
			return false, err
		}
		missing := ""
		for _, s := range p.strategies {
			if !done[s] {
				missing = s
				break
			}
		}
		if missing == "" {
			break
		}
		f, err := queryCrux(key, p.origin, formFactor(missing), true)
		if err != nil {
			logf("warn", "pagespeed: origin %s: %v", missing, redact(err, key))
		}
		if err := upsertOrigin(day, missing, f); err != nil {
			return false, err
		}
	}
	for left() >= minForCrux {
		w, ok, err := nextWork(day, p.targets, p.strategies)
		if err != nil {
			return false, err
		}
		if !ok {
			done, err := originDone(day)
			if err != nil || len(done) < len(p.strategies) {
				return false, err
			}
			return true, markDayDone(day)
		}
		if w.Step == stepCrux {
			if err := measureField(p, day, w.URL, w.Strategy); err != nil {
				return false, err
			}
			if err := markCrux(day, w.URL, w.Strategy); err != nil {
				return false, err
			}
			continue
		}
		if left() < minForPSI {
			return false, nil
		}
		state, err := measureLab(p, day, w.URL, w.Strategy)
		if err != nil {
			return false, err
		}
		return false, markLab(day, w.URL, w.Strategy, state)
	}
	return false, nil
}

// measureField looks up and stores the field data of one page and device. A
// failed lookup is stored as such: the row exists and the next day tries again.
func measureField(p plan, day, path, strategy string) error {
	f, err := queryCrux(p.s.APIKey, p.origin+path, formFactor(strategy), false)
	if err != nil {
		logf("warn", "pagespeed: %s %s field: %v", path, strategy, redact(err, p.s.APIKey))
	}
	return upsertField(day, path, strategy, f)
}

// measureLab runs and stores one lab measurement and returns its state.
func measureLab(p plan, day, path, strategy string) (string, error) {
	l, err := runPSI(p.s.APIKey, p.origin+path, strategy)
	if err != nil {
		logf("warn", "pagespeed: %s %s lab: %v", path, strategy, redact(err, p.s.APIKey))
	} else if l.Status == labTimeout {
		logf("info", "pagespeed: %s %s lab timed out", path, strategy)
	}
	return l.Status, upsertLab(day, path, strategy, l)
}

// measureOne is "Measure now" for one tracked page: the field data of every
// configured device, then one lab run for strategy if the budget still allows
// it. It overwrites today's field rows and never a finished lab score. The caller holds the budget (startBudget).
func measureOne(p plan, path, strategy string) error {
	day := today()
	for _, s := range p.strategies {
		if err := measureField(p, day, path, s); err != nil {
			return err
		}
		if err := markCrux(day, path, s); err != nil {
			return err
		}
	}
	if left() < minForPSI {
		return nil
	}
	state, err := measureLab(p, day, path, strategy)
	if err != nil {
		return err
	}
	return markLab(day, path, strategy, state)
}

// runCron is the cron job: one budgeted step, then retention on the call that
// completes the day. It never fails the host: a missing key or origin is a notice in the
// admin page, and a host error is logged.
func runCron() error {
	complete, err := measureStep()
	if err != nil {
		if !errors.Is(err, errNoKey) && !errors.Is(err, errNoOrigin) {
			logf("error", "pagespeed: measure: %v", err)
		}
		return nil
	}
	if complete {
		if err := runRetention(now()); err != nil {
			logf("error", "pagespeed: retention: %v", err)
		}
	}
	return nil
}
