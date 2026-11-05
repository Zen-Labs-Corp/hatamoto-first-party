package main

import (
	"time"

	"hatamoto.dev/sdk"
)

// cronBudget is how long one run keeps starting deliveries; the hook's own
// limit is 10 s (plugin.json).
const cronBudget = 8 * time.Second

// classify turns an answer into an outcome: any 2xx is sent (Teams answers
// 202, which only means accepted); 408, 429 and 5xx and transient host errors
// are tried again later; every other answer is a rejection retrying cannot fix.
func classify(r fetchResp) result {
	switch {
	case r.Code != "":
		return hostResult(r.Code)
	case r.Status >= 200 && r.Status <= 299:
		return result{kind: kindSent, status: r.Status}
	case r.Status == 408 || r.Status == 429 || r.Status >= 500:
		return result{kind: kindRetry, status: r.Status, retryAfter: r.RetryAfter}
	}
	return result{kind: kindFail, status: r.Status, reason: "rejected"}
}

// deliverOne sends one queued message.
func deliverOne(c cfg, j job) result {
	if !c.enabled(j.Channel) || !c.isSet(j.Channel) {
		return result{kind: kindFail, reason: "not set up"}
	}
	return classify(post(c, j.Channel, j.Body))
}

// drain claims due rows and delivers them until the batch or the time budget
// is used up. Rows are independent: one failing never stops the others.
func drain(t time.Time) {
	c, err := loadCfg()
	if err != nil {
		logf("warn", "form-alerts: settings unreadable")
		return
	}
	jobs, err := claim(t)
	if err != nil {
		logf("warn", "form-alerts: could not read the queue")
		return
	}
	budgetEnd = t.Add(cronBudget)
	defer func() { budgetEnd = time.Time{} }()
	for i, j := range jobs {
		left := budgetEnd.Sub(now())
		if left < minCall {
			for _, rest := range jobs[i:] {
				settle(rest, result{kind: kindBudget})
			}
			return
		}
		settle(j, slowCut(deliverOne(c, j), left, cronBudget))
	}
}

// slowCut turns a budget cut into a normal retry when the message started
// with (nearly) the whole budget: it will not fit next time either, so it must
// be counted and backed off instead of heading the queue every run.
func slowCut(r result, left, total time.Duration) result {
	if r.kind == kindBudget && left >= total-minCall {
		return result{kind: kindRetry, reason: "too slow"}
	}
	return r
}

// onDeliver is the every-minute cron. It never returns an error.
func onDeliver(sdk.CronEvent) error {
	drain(now())
	return nil
}

// onRetry is the every-five-minutes cron: it drops what is too old, prunes the
// log, then drains like onDeliver. It never returns an error.
func onRetry(sdk.CronEvent) error {
	start := now()
	expire(start)
	pruneLog(start)
	drain(start)
	return nil
}
