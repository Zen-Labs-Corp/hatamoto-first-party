package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const hookSettings = `{"slack_enabled":true,"slack_webhook_url":"https://hooks.slack.com/services/T/B/X",` +
	`"telegram_enabled":true,"telegram_bot_token":"123456:abcdefghijklmnopqrstuv","telegram_chat_id":"-100123",` +
	`"teams_enabled":true}`

const hookForms = `{"items":[{"slug":"contact","name":"Contact","fields":[{"name":"email"},{"name":"name"}]}]}`

func fire(slug string) error {
	return onFormSubmitted(sdk.FormSubmittedEvent{
		FormSlug: slug, SubmissionID: "42",
		Fields: map[string]any{"name": "Ana", "email": "ana@example.com"},
	})
}

func count(h *fakeHost, fn string) int {
	n := 0
	for _, c := range h.calls {
		if c.fn == fn {
			n++
		}
	}
	return n
}

func TestHookQueuesOneRowPerReadyChannelWithoutNetwork(t *testing.T) {
	h := &fakeHost{settings: hookSettings, forms: hookForms}
	h.install(t)
	if err := fire("contact"); err != nil {
		t.Fatal(err)
	}
	if got := count(h, "db.exec"); got != 2 {
		t.Fatalf("inserts = %d, want 2 (slack, telegram; teams has no URL)", got)
	}
	if count(h, "http.fetch") != 0 {
		t.Fatal("the hook made a network call")
	}
	for _, c := range h.calls {
		if c.fn == "db.exec" && strings.Contains(c.args[3].(string), "123456:abcdefghijklmnopqrstuv") {
			t.Fatal("bot token stored in the queue body")
		}
	}
	warned := false
	for _, c := range h.calls {
		if c.fn == "log" && c.msg == "form-alerts: channel not set up: teams" {
			warned = true
		}
	}
	if !warned {
		t.Fatal("no warning for the enabled channel without a URL")
	}
}

func TestHookWatchedFormFilter(t *testing.T) {
	h := &fakeHost{settings: `{"forms":["quote"],"slack_enabled":true,"slack_webhook_url":"https://hooks.slack.com/x"}`, forms: hookForms}
	h.install(t)
	if err := fire("contact"); err != nil {
		t.Fatal(err)
	}
	if count(h, "db.exec") != 0 {
		t.Fatal("unwatched form was queued")
	}
	if err := fire("quote"); err != nil {
		t.Fatal(err)
	}
	if count(h, "db.exec") != 1 {
		t.Fatal("watched form was not queued")
	}
}

func TestHookFailsOpen(t *testing.T) {
	h := &fakeHost{settings: `{"slack_enabled":true,"slack_webhook_url":"https://hooks.slack.com/x"}`}
	h.install(t)
	if err := fire("contact"); err != nil {
		t.Fatalf("forms.list failure: %v", err)
	}
	if count(h, "db.exec") != 1 {
		t.Fatal("forms.list failure must still queue")
	}
	h.dup = true
	if err := fire("contact"); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	callHost = func(string, any, any) error { return errHost }
	if err := fire("contact"); err != nil {
		t.Fatalf("settings failure: %v", err)
	}
}
