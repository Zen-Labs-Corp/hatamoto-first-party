package main

import (
	"encoding/json"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// run delivers ev through the registered form.submitted action, as the host does.
func run(t *testing.T, ev sdk.FormSubmittedEvent) error {
	t.Helper()
	in, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	_, err = guest.Handle(sdk.FormSubmitted.Export(), in)
	return err
}

func TestHookIsRegisteredAndFailsOpen(t *testing.T) {
	h := newHost(t)
	h.settingsErr = errHostDown
	if err := run(t, sdk.FormSubmittedEvent{SubmissionID: "s1", SubmittedAt: time.Unix(1700000000, 0)}); err != nil {
		t.Fatalf("hook returned %v", err)
	}
	if h.count("http.fetch") != 0 {
		t.Fatal("no fetch expected when settings are unreadable")
	}
	if h.count("log") != 1 {
		t.Fatalf("log lines = %d", h.count("log"))
	}
}

func TestHookSurvivesAPanic(t *testing.T) {
	h := newHost(t)
	h.configured()
	h.panicOn = "db.exec"
	if err := run(t, sample("s1")); err != nil {
		t.Fatalf("hook returned %v", err)
	}
}
