package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestLoadSettingsDefaultsAndFailsOpen(t *testing.T) {
	newFakeHost(t, func(_ call, out any) error { return json.Unmarshal([]byte(`{}`), out) })
	s := loadSettings()
	if s.FailThreshold != 2 || !slices.Equal(s.SSLWarnDays, []int{21, 7, 1}) || len(s.AlertEmails) != 0 {
		t.Fatalf("defaults: %+v", s)
	}

	newFakeHost(t, func(_ call, out any) error {
		return json.Unmarshal([]byte(`{"alert_emails":["a@example.org"],"fail_threshold":4,"ssl_warn_days":[30],"heartbeat_url":"https://hb.example/secret"}`), out)
	})
	s = loadSettings()
	if s.FailThreshold != 4 || !slices.Equal(s.SSLWarnDays, []int{30}) || s.AlertEmails[0] != "a@example.org" || s.HeartbeatURL == "" {
		t.Fatalf("stored: %+v", s)
	}

	newFakeHost(t, func(_ call, out any) error { return json.Unmarshal([]byte(`{"fail_threshold":99}`), out) })
	if s := loadSettings(); s.FailThreshold != 2 {
		t.Fatalf("out of range: %+v", s)
	}

	f := newFakeHost(t, func(call, any) error { return errors.New("down") })
	s = loadSettings()
	if s.FailThreshold != 2 || !slices.Equal(s.SSLWarnDays, []int{21, 7, 1}) {
		t.Fatalf("host error must give defaults: %+v", s)
	}
	if logs := f.logs(); len(logs) != 1 || !strings.Contains(logs[0], "settings unavailable") {
		t.Fatalf("logs %v", logs)
	}
}
