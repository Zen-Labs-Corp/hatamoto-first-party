package main

import (
	"time"
)

const (
	refreshBudget = 9 * time.Second
	maxStored     = 50
)

// runRefresh refreshes every configured source on its own: one failing never
// touches the other's rows. It fetches first and replaces the rows only on
// success, so a failure keeps the old copy and records an error. The result
// maps each source to "ok", "skipped" (not set up) or the redacted error.
func runRefresh(budget time.Duration) map[string]string {
	budgetEnd = now().Add(budget)
	defer func() { budgetEnd = time.Time{} }()
	s := loadSettings()
	res := map[string]string{}

	tok, err := loadToken()
	switch {
	case err != nil:
		res["instagram"] = recordFailure("instagram", "could not read the connection", s)
	case tok == nil:
		res["instagram"] = "skipped"
	default:
		tok = renewToken(tok)
		res["instagram"] = refreshSource("instagram", s, func() ([]item, error) { return fetchInstagram(tok) }, tok.AccessToken)
	}

	if s.YouTubeKey == "" || s.ChannelID == "" {
		res["youtube"] = "skipped"
	} else {
		res["youtube"] = refreshSource("youtube", s, func() ([]item, error) { return fetchYouTube(s) })
	}
	return res
}

func refreshSource(source string, s settings, get func() ([]item, error), secrets ...string) string {
	items, err := get()
	if err == nil {
		if len(items) > maxStored {
			items = items[:maxStored]
		}
		if err = replaceItems(source, items); err == nil {
			if err = setStatus(source, "", true); err != nil {
				logf("warn", "%s: recording the status: %s", source, redact(err.Error(), append(secrets, s.AppSecret, s.YouTubeKey)...))
			}
			return "ok"
		}
	}
	return recordFailure(source, err.Error(), s, secrets...)
}

// recordFailure stores and logs a redacted error and returns it.
func recordFailure(source, msg string, s settings, secrets ...string) string {
	msg = redact(msg, append(secrets, s.AppSecret, s.YouTubeKey)...)
	logf("warn", "%s refresh failed: %s", source, msg)
	if err := setStatus(source, msg, false); err != nil {
		logf("warn", "%s: recording the status: %s", source, redact(err.Error()))
	}
	return msg
}
