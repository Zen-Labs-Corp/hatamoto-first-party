// Command subscriber-sync is a Hatamoto plugin. Adds people who submit your forms to Mailchimp, Brevo, MailerLite or Kit, with field mapping, an optional consent field and retries.
//
// A form submission is queued first and delivered once inline; the retry cron
// delivers whatever is left. The hooks never fail the form: problems end up in
// the delivery log, which holds no personal data.
package main

import (
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// now is the clock; tests replace it.
var now = time.Now

// hookBudget is how long the inline attempt may keep starting requests; the
// hook's own limit is 9 s (plugin.json).
const hookBudget = 6 * time.Second

// deliverFn sends one queued submission; tests replace it.
var deliverFn = deliver

func init() {
	guest.OnAction(sdk.FormSubmitted, onFormSubmitted)
	guest.OnCron("retry", onRetry)
}

// onFormSubmitted never returns an error: a problem with settings, the queue
// or the provider is logged and the submission carries on.
func onFormSubmitted(ev sdk.FormSubmittedEvent) error {
	c, err := loadCfg()
	if err != nil {
		logf("warn", "subscriber-sync: settings unreadable")
		return nil
	}
	if !c.watched(ev.FormSlug) {
		return nil
	}
	if !c.configured() {
		record(ev.FormSlug, "skipped", "not configured", 0)
		return nil
	}
	if c.ConsentField != "" && !truthy(ev.Fields[c.ConsentField]) {
		record(ev.FormSlug, "skipped", "no consent", 0)
		return nil
	}
	email := fieldValue(ev.Fields, c.emailField())
	if !validEmail(email) {
		record(ev.FormSlug, "skipped", "invalid email", 0)
		return nil
	}
	j := job{
		SubmissionID: ev.SubmissionID,
		FormSlug:     ev.FormSlug,
		Email:        email,
		FirstName:    fieldText(ev.Fields, c.FirstNameField),
		LastName:     fieldText(ev.Fields, c.LastNameField),
	}
	fresh, err := enqueue(j)
	if err != nil {
		logf("warn", "subscriber-sync: could not queue a submission")
		return nil
	}
	if !fresh {
		return nil
	}
	budgetEnd = now().Add(hookBudget)
	defer func() { budgetEnd = time.Time{} }()
	settle(j, slowCut(deliverFn(c, j), hookBudget, hookBudget))
	return nil
}
