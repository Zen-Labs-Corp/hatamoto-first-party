package main

// settings are the owner's options with the defaults the host does not merge
// for us applied.
type settings struct {
	AlertEmails   []string `json:"alert_emails"`
	FailThreshold int      `json:"fail_threshold"`
	SSLWarnDays   []int    `json:"ssl_warn_days"`
	HeartbeatURL  string   `json:"heartbeat_url"`
}

const defaultFailThreshold = 2

var defaultSSLWarnDays = []int{21, 7, 1}

func defaultSettings() settings {
	return settings{FailThreshold: defaultFailThreshold, SSLWarnDays: append([]int(nil), defaultSSLWarnDays...)}
}

// loadSettings never fails: a host error or an out of range value gives the
// default, so a job keeps working with a broken settings store.
func loadSettings() settings {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		logf("warn", "uptime-monitor: settings unavailable, using defaults code=%s", hostErrCode(err))
		return defaultSettings()
	}
	if s.FailThreshold < 1 || s.FailThreshold > 10 {
		s.FailThreshold = defaultFailThreshold
	}
	if s.SSLWarnDays == nil {
		s.SSLWarnDays = append([]int(nil), defaultSSLWarnDays...)
	}
	return s
}
