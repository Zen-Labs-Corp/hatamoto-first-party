package main

import (
	"time"

	"hatamoto.dev/sdk"
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
		if s.enabled(p) && !isBroken(p) {
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
