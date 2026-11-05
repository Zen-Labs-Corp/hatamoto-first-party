package main

import "hatamoto.dev/sdk"

// onFormSubmitted validates a submission and queues one row for the deliver
// cron. It never calls a provider and never returns an error: a problem with
// settings or the queue is logged and the form carries on.
func onFormSubmitted(ev sdk.FormSubmittedEvent) error {
	c, err := loadCfg()
	if err != nil {
		logf("warn", "settings unreadable")
		return nil
	}
	// An unwatched form or an unfinished setup is not this plugin's business:
	// nothing is queued and nothing is logged.
	if !c.configured() || !c.watched(ev.FormSlug) {
		return nil
	}
	if c.ConsentField != "" && !truthy(ev.Fields[c.ConsentField]) {
		recordRow(ev.SubmissionID, ev.FormSlug, c.Provider, "skipped", "", "", 0, "no consent")
		return nil
	}
	email := fieldValue(ev.Fields, c.emailField())
	if !validEmail(email) {
		recordRow(ev.SubmissionID, ev.FormSlug, c.Provider, "skipped", "", "", 0, "invalid email")
		return nil
	}
	submitted := ev.SubmittedAt
	if submitted.IsZero() {
		submitted = now()
	}
	j := job{
		SubmissionID: ev.SubmissionID,
		FormSlug:     ev.FormSlug,
		Provider:     c.Provider,
		Email:        email,
		Fields:       flattenFields(ev.Fields),
		SourcePath:   cut(ev.SourcePath, 300),
		SubmittedAt:  submitted,
	}
	if _, err := enqueue(j); err != nil {
		logf("warn", "could not queue a submission")
	}
	return nil
}
