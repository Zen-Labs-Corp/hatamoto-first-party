package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const hookSettings = `{"provider":"hubspot","access_token":"tok","forms":["contact"]}`

func submission(fields map[string]any) sdk.FormSubmittedEvent {
	return sdk.FormSubmittedEvent{
		FormSlug:     "contact",
		SubmissionID: "sub-1",
		Fields:       fields,
		SourcePath:   "/contact",
		SubmittedAt:  time.Date(2026, 11, 5, 9, 59, 0, 0, time.UTC),
	}
}

func TestConsentRequired(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value any
		queue bool
	}{
		{"absent", false, nil, false},
		{"false", true, false, false},
		{"zero", true, 0.0, false},
		{"no", true, "no", false},
		{"empty", true, "", false},
		{"empty list", true, []any{}, false},
		{"empty text in list", true, []any{""}, false},
		{"true", true, true, true},
		{"one", true, 1.0, true},
		{"Yes", true, "Yes", true},
		{"list", true, []any{"yes"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHost{settings: `{"provider":"hubspot","access_token":"tok","forms":["contact"],"consent_field":"ok"}`}
			h.install(t)
			f := map[string]any{"email": "ann@example.com"}
			if tc.set {
				f["ok"] = tc.value
			}
			if err := onFormSubmitted(submission(f)); err != nil {
				t.Fatal(err)
			}
			queued := len(h.execs("INSERT INTO queue"))
			if tc.queue != (queued == 1) {
				t.Fatalf("queued = %d", queued)
			}
			rows := h.logRows()
			if !tc.queue && (len(rows) != 1 || rows[0] != [2]string{"skipped", "no consent"}) {
				t.Fatalf("log = %v", rows)
			}
			if tc.queue && len(rows) != 0 {
				t.Fatalf("log = %v", rows)
			}
		})
	}
}

func TestFieldNamesAreTrimmed(t *testing.T) {
	h := &fakeHost{settings: `{"provider":"hubspot","access_token":"tok","forms":["contact"],"consent_field":" ok ","email_field":" mail "}`}
	h.install(t)
	if err := onFormSubmitted(submission(map[string]any{"mail": "ann@example.com", "ok": "yes"})); err != nil {
		t.Fatal(err)
	}
	if n := len(h.execs("INSERT INTO queue")); n != 1 {
		t.Fatalf("queued = %d, log = %v", n, h.logRows())
	}
}

func TestNoProviderQueuesNothing(t *testing.T) {
	h := &fakeHost{settings: `{"forms":["contact"],"access_token":"tok"}`}
	h.install(t)
	onFormSubmitted(submission(map[string]any{"email": "ann@example.com"}))
	if len(h.calls) != 0 {
		t.Fatalf("calls = %+v", h.calls)
	}
}

func TestIncompleteCredentialsQueueNothing(t *testing.T) {
	for _, s := range []string{
		`{"provider":"hubspot","access_token":"  "}`,
		`{"provider":"hubspot"}`,
		`{"provider":"pipedrive","api_token":" ","company_domain":"acme"}`,
		`{"provider":"pipedrive","api_token":"x"}`,
		`{"provider":"salesforce","client_id":"a","client_secret":" ","my_domain":"acme.my.salesforce.com"}`,
		`{"provider":"salesforce","client_id":"a","client_secret":"b"}`,
		`{"provider":"salesforce","client_id":"a","client_secret":"b","my_domain":"evil.example.com"}`,
	} {
		h := &fakeHost{settings: strings.TrimSuffix(s, "}") + `,"forms":["contact"]}`}
		h.install(t)
		onFormSubmitted(submission(map[string]any{"email": "ann@example.com"}))
		if len(h.calls) != 0 {
			t.Errorf("%s: calls = %+v", s, h.calls)
		}
	}
}

func TestUnwatchedFormQueuesNothing(t *testing.T) {
	h := &fakeHost{settings: `{"provider":"hubspot","access_token":"tok","forms":["other"],"consent_field":"ok"}`}
	h.install(t)
	ev := submission(map[string]any{"email": "ann@example.com"})
	onFormSubmitted(ev)
	if len(h.calls) != 0 {
		t.Fatalf("calls = %+v", h.calls)
	}
}

func TestHookDoesNoFetch(t *testing.T) {
	h := &fakeHost{settings: hookSettings}
	h.install(t)
	onFormSubmitted(submission(map[string]any{"email": "ann@example.com"}))
	if h.fetchCalls() != 0 || len(h.execs("INSERT INTO queue")) != 1 {
		t.Fatalf("calls = %+v", h.calls)
	}
}

func TestInvalidEmailSkipped(t *testing.T) {
	for _, e := range []any{"", "nope", "a b@example.com", nil, 5.0} {
		h := &fakeHost{settings: hookSettings}
		h.install(t)
		onFormSubmitted(submission(map[string]any{"email": e}))
		rows := h.logRows()
		if len(h.execs("INSERT INTO queue")) != 0 || len(rows) != 1 || rows[0] != [2]string{"skipped", "invalid email"} {
			t.Errorf("%v: calls = %+v", e, h.calls)
		}
		for _, a := range h.execs("INSERT INTO log")[0].args {
			if s, ok := a.(string); ok && strings.Contains(s, "example.com") {
				t.Errorf("log arg %q holds the email", s)
			}
		}
	}
}

func TestHookKeepsLongEmailWhole(t *testing.T) {
	h := &fakeHost{settings: hookSettings}
	h.install(t)
	email := strings.Repeat("a", 200) + "@example.com"
	onFormSubmitted(submission(map[string]any{"email": email}))
	ins := h.execs("INSERT INTO queue")
	if len(ins) != 1 || ins[0].args[3] != email {
		t.Fatalf("calls = %+v", h.calls)
	}
}

func TestQueuedRowCarriesFlattenedFields(t *testing.T) {
	h := &fakeHost{settings: hookSettings}
	h.install(t)
	onFormSubmitted(submission(map[string]any{"email": " ann@example.com ", "name": "Ann", "n": 3.0}))
	ins := h.execs("INSERT INTO queue")
	if len(ins) != 1 {
		t.Fatalf("calls = %+v", h.calls)
	}
	a := ins[0].args
	fields, ok := a[4].(string)
	if !ok || !strings.Contains(fields, `"name":"Ann"`) || !strings.Contains(fields, `"n":"3"`) {
		t.Errorf("fields arg = %#v", a[4])
	}
	if a[0] != "sub-1" || a[1] != "contact" || a[2] != "hubspot" || a[3] != "ann@example.com" || a[5] != "/contact" || a[6] != "2026-11-05T09:59:00Z" {
		t.Errorf("args = %v", a)
	}
}

func TestDuplicateSubmissionIsIgnored(t *testing.T) {
	h := &fakeHost{settings: hookSettings, dup: true}
	h.install(t)
	if err := onFormSubmitted(submission(map[string]any{"email": "ann@example.com"})); err != nil {
		t.Fatal(err)
	}
	if len(h.logRows()) != 0 || len(h.hostLogs()) != 0 {
		t.Fatalf("calls = %+v", h.calls)
	}
}

func TestHookFailsOpenOnDBError(t *testing.T) {
	h := &fakeHost{settings: hookSettings}
	h.install(t)
	inner := callHost
	callHost = func(fn string, a, out any) error {
		if fn == "db.exec" {
			return errors.New("db down")
		}
		return inner(fn, a, out)
	}
	if err := onFormSubmitted(submission(map[string]any{"email": "ann@example.com"})); err != nil {
		t.Fatal(err)
	}
	if logs := h.hostLogs(); len(logs) != 1 || strings.Contains(logs[0], "example.com") {
		t.Errorf("logs = %v", logs)
	}
}

func TestHookFailsOpenOnSettingsError(t *testing.T) {
	h := &fakeHost{settings: `not json`}
	h.install(t)
	if err := onFormSubmitted(submission(map[string]any{"email": "ann@example.com"})); err != nil {
		t.Fatal(err)
	}
	if len(h.execs("INSERT")) != 0 {
		t.Fatalf("calls = %+v", h.calls)
	}
}
