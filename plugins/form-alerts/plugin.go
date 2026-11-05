// Command form-alerts is a Hatamoto plugin. Posts each form submission to Slack, Microsoft Teams, Google Chat or Telegram the moment it arrives, with the fields you choose and a link to the submission in your admin.
//
// The form hook only renders the message and queues it, one row per channel;
// it makes no network call. The deliver cron sends what is queued and the
// retry cron expires old rows and sends the ones whose wait is over. Problems
// end up in the delivery log, which holds no field values, URLs or tokens.
package main

import (
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// now is the clock; tests replace it.
var now = time.Now

func init() {
	guest.OnAction(sdk.FormSubmitted, onFormSubmitted)
	guest.OnCron("deliver", onDeliver)
	guest.OnCron("retry", onRetry)
}

// onFormSubmitted never returns an error: a problem with settings or the
// queue is logged and the submission carries on.
func onFormSubmitted(ev sdk.FormSubmittedEvent) error {
	c, err := loadCfg()
	if err != nil {
		logf("warn", "form-alerts: settings unreadable")
		return nil
	}
	if !c.watched(ev.FormSlug) {
		return nil
	}
	sub := submission{FormSlug: ev.FormSlug, ID: ev.SubmissionID, Fields: ev.Fields, Order: fieldOrder(ev.FormSlug)}
	v := buildView(sub, c)
	for _, ch := range []string{chanSlack, chanTeams, chanGChat, chanTelegram} {
		if !c.enabled(ch) {
			continue
		}
		if !c.isSet(ch) {
			logf("warn", "form-alerts: channel not set up: %s", ch)
			continue
		}
		_, err := enqueue(job{
			SubmissionID: ev.SubmissionID,
			FormSlug:     ev.FormSlug,
			Channel:      ch,
			Body:         string(renderFor(ch, v, c)),
		})
		if err != nil {
			logf("warn", "form-alerts: could not queue a message: %s", ch)
		}
	}
	return nil
}

// renderFor renders the view for one channel.
func renderFor(channel string, v view, c cfg) []byte {
	switch channel {
	case chanSlack:
		return renderSlack(v)
	case chanTeams:
		return renderTeams(v)
	case chanGChat:
		return renderGChat(v)
	}
	return renderTelegram(v, c.TelegramChatID)
}

// fieldOrder returns the form's field keys in definition order, or nil when
// the form list cannot be read.
func fieldOrder(slug string) []string {
	var res struct {
		Items []struct {
			Slug   string `json:"slug"`
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"items"`
	}
	if callHost("forms.list", struct{}{}, &res) != nil {
		return nil
	}
	for _, f := range res.Items {
		if f.Slug != slug {
			continue
		}
		keys := make([]string, 0, len(f.Fields))
		for _, fd := range f.Fields {
			keys = append(keys, fd.Name)
		}
		return keys
	}
	return nil
}
