package main

import (
	"fmt"
	"net/http"
	"time"
)

const (
	heartbeatEvery     = time.Minute
	heartbeatTimeoutMS = 3000
	heartbeatKey       = "heartbeat_at"
)

// maybeHeartbeat pings the owner's heartbeat URL after a good check of the
// site's own monitor, at most once a minute and only when remaining time
// allows a useful request. A failure is logged by code and changes nothing.
// The URL is a secret: it is used for the request and appears nowhere else,
// not in a log line and not in an error text.
func maybeHeartbeat(site monitor, r result, st settings, remaining time.Duration) {
	if !site.IsSite || st.HeartbeatURL == "" || (r.Status != statusUp && r.Status != statusSlow) {
		return
	}
	var last int64
	if _, err := kvGet(heartbeatKey, &last); err != nil {
		logf("warn", "uptime-monitor: heartbeat time unreadable code=%s", hostErrCode(err))
	} else if last > 0 && now().Sub(time.UnixMilli(last)) < heartbeatEvery {
		return
	}
	timeout := min(heartbeatTimeoutMS, int((remaining-tickMargin)/time.Millisecond))
	if timeout < 1000 {
		return
	}
	// Recorded before the request, so a failing endpoint is not hammered.
	if err := kvSet(heartbeatKey, now().UnixMilli()); err != nil {
		logf("warn", "uptime-monitor: heartbeat time not saved code=%s", hostErrCode(err))
	}
	res, err := fetch(fetchReq{Method: http.MethodGet, URL: st.HeartbeatURL, TimeoutMS: timeout})
	switch {
	case err != nil:
		code := hostErrCode(err)
		if code == "" {
			code = "unknown"
		}
		logf("warn", "uptime-monitor: heartbeat failed code=%s", code)
	case res.Status >= 400:
		logf("warn", "uptime-monitor: heartbeat failed code=%s", fmt.Sprintf("http_%d", res.Status))
	}
}
