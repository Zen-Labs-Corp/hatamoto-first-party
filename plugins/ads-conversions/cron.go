package main

import (
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	// batchSize is how many rows one run claims.
	batchSize = 25
	// cronBudget is how long one run keeps starting deliveries; the hook's own
	// limit is 10 s (plugin.json).
	cronBudget = 8 * time.Second
	// minCall is the least time worth starting another call for.
	minCall = 1500 * time.Millisecond
	// pauseTime is how long rows of a stopped platform are left alone.
	pauseTime = 10 * time.Minute
)

// expiryWarning is how long before a LinkedIn token runs out it is refreshed,
// or the owner is told.
const expiryWarning = 7 * 24 * time.Hour

// onRefresh keeps the connections alive. It never returns an error.
func onRefresh(sdk.CronEvent) error {
	defer func() {
		if recover() != nil {
			logf("error", "ads-conversions: token refresh stopped by an internal error")
		}
	}()
	s, err := loadSettings()
	if err != nil {
		logf("warn", "ads-conversions: settings unreadable")
		return nil
	}
	for _, p := range []string{platGoogle, platLinkedIn} {
		if s.enabled(p) && !isBroken(s, p) {
			refreshPlatform(s, p)
		}
	}
	return nil
}

func refreshPlatform(s settings, platform string) {
	t, err := loadTokens(s, platform)
	switch err {
	case nil:
	case errNotConnected:
		return
	case errReconnect:
		markBroken(s, platform)
		return
	default:
		logf("warn", "ads-conversions: could not read the %s token", platform)
		return
	}
	if platform == platGoogle {
		// A refresh proves the grant still works; a revoked grant shows up here
		// instead of on the next form.
		if _, err := accessToken(s, platform, true); err != nil && err != errReconnect {
			logf("warn", "ads-conversions: Google token refresh failed")
		}
		return
	}
	if t.Expires.IsZero() || t.Expires.Sub(now()) >= expiryWarning {
		return
	}
	if t.Refresh != "" {
		if _, err := accessToken(s, platform, true); err != nil && err != errReconnect {
			logf("warn", "ads-conversions: LinkedIn token refresh failed")
		}
		return
	}
	if !now().Before(t.Expires) {
		markBroken(s, platform)
		return
	}
	notifyOnce(s, linkedInExpiryKey, "LinkedIn connection expires soon",
		"The LinkedIn connection of the server-side conversions plugin expires on "+t.Expires.UTC().Format("2006-01-02")+
			". Open the plugin page in the admin area and choose Reconnect before then, or conversions stop.\n")
}

func init() {
	guest.OnCron("deliver", onDeliver)
	guest.OnCron("retry", onRetry)
}

// onDeliver sends the first attempt of queued conversions. It never returns
// an error.
func onDeliver(sdk.CronEvent) error {
	defer func() {
		if recover() != nil {
			logf("error", "ads-conversions: delivery stopped by an internal error")
		}
	}()
	start := now()
	s, err := loadSettings()
	if err != nil {
		logf("warn", "ads-conversions: settings unreadable")
		return nil
	}
	jobs, err := claimFirst(start, batchSize)
	if err != nil {
		logf("warn", "ads-conversions: could not read the queue")
		return nil
	}
	drain(s, jobs, start)
	return nil
}

// onRetry expires old rows, purges old logs and clicks, then retries rows that
// are due again. It never returns an error.
func onRetry(sdk.CronEvent) error {
	defer func() {
		if recover() != nil {
			logf("error", "ads-conversions: retry stopped by an internal error")
		}
	}()
	start := now()
	expireOld(start)
	purge(start)
	s, err := loadSettings()
	if err != nil {
		logf("warn", "ads-conversions: settings unreadable")
		return nil
	}
	jobs, err := claimRetry(start, batchSize)
	if err != nil {
		logf("warn", "ads-conversions: could not read the queue")
		return nil
	}
	drain(s, jobs, start)
	return nil
}

// drain delivers the claimed rows until the time budget is spent and releases
// the rest for the next run.
func drain(s settings, jobs []job, start time.Time) {
	budgetEnd = start.Add(cronBudget)
	defer func() { budgetEnd = time.Time{} }()
	stalled := map[string]bool{}
	for i, j := range jobs {
		if budgetEnd.Sub(now()) < minCall {
			for _, rest := range jobs[i:] {
				release(rest)
			}
			return
		}
		// A platform that just timed out would eat the budget of the others:
		// its remaining rows wait for the next run.
		if stalled[j.Platform] {
			release(j)
			continue
		}
		if deliverRow(s, j) {
			stalled[j.Platform] = true
		}
	}
}

// deliverRow makes one attempt for one row and reports whether the platform
// could not be reached (a timeout or transport error), so the run leaves its
// other rows alone. A platform that is off, not
// configured or needs reconnecting is left alone (the row waits, then
// expires after 24 h) but still keeps the posted click IDs, which are purged
// after an hour; each row is independent of the others.
func deliverRow(s settings, j job) (unreachable bool) {
	p := j.Platform
	if err := joinClicks(&j); err != nil {
		logf("warn", "ads-conversions: could not read the click IDs")
		release(j)
		return false
	}
	if !s.enabled(p) || !s.configured(p) || isBroken(s, p) {
		pause(j)
		return false
	}
	if !hasIdentifier(j) {
		deleteJob(j)
		record(j.SubmissionID, j.Form, p, "skipped", "no_identifier", "", "")
		return false
	}
	o := attempt(s, j, false)
	if o.Kind == kindAuth && o.Reason != "token" && p != platTikTok {
		// The platform refused the access token: refresh once and try again.
		o = attempt(s, j, true)
	}
	switch o.Kind {
	case kindSent:
		settleSent(j, o.Code, "")
	case kindFailed:
		settleFailed(j, o.Reason, o.Code, o.Message)
	case kindRetry:
		settleRetry(j, o.Code, o.Message)
	case kindAuth:
		markBroken(s, p)
		release(j)
	}
	return o.Kind == kindRetry && (o.Reason == "network" || o.Code == "token")
}

// pause pushes a row back so a stopped platform's rows do not crowd out the
// others when the next run claims.
func pause(j job) {
	err := dbExec(`UPDATE queue SET next_attempt_at = $2::timestamptz, lease_until = NULL WHERE id = $1`, j.ID, ts(now().Add(pauseTime)))
	if err != nil {
		logf("warn", "ads-conversions: could not pause a conversion")
	}
}

func hasIdentifier(j job) bool {
	switch j.Platform {
	case platGoogle:
		return hasIdentifierGoogle(j)
	case platTikTok:
		return hasIdentifierTikTok(j)
	}
	return hasIdentifierLinkedIn(j)
}

// tokenOutcome turns a token error into an outcome: a connection that needs
// reconnecting is an auth outcome (marked "token" so it is not retried with a
// forced refresh), anything else is a retry.
func tokenOutcome(err error) outcome {
	switch err {
	case errReconnect:
		return outcome{Kind: kindAuth, Reason: "token", Code: "reconnect"}
	case errNotConnected:
		return outcome{Kind: kindRetry, Reason: "token", Code: "not_connected"}
	}
	return outcome{Kind: kindRetry, Reason: "token", Code: "token"}
}

// attempt builds and sends the request for the row's platform.
func attempt(s settings, j job, forceRefresh bool) outcome {
	switch j.Platform {
	case platTikTok:
		return execute(buildTikTok(s, j, false), classifyTikTok)
	case platGoogle:
		tok, err := accessToken(s, platGoogle, forceRefresh)
		if err != nil {
			return tokenOutcome(err)
		}
		return execute(buildGoogle(s, j, tok, false), classifyGoogle)
	}
	tok, err := accessToken(s, platLinkedIn, forceRefresh)
	if err != nil {
		return tokenOutcome(err)
	}
	return execute(buildLinkedIn(s, j, tok), classifyLinkedIn)
}
