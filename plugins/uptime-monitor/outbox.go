package main

import (
	"time"

	"hatamoto.dev/sdk"
)

const (
	mailWindow    = 60 * time.Second
	mailWindowMax = 6 // recipients per sliding window, as the host's limiter
	mailMaxAge    = 24 * time.Hour
	mailWindowKey = "mail_window"
	outboxBatch   = 50
)

// enqueueMail queues one mail for all recipients. Nothing is queued without
// recipients.
func enqueueMail(subject, body string, to []string) error {
	if len(to) == 0 {
		return nil
	}
	return insertOutbox(subject, body, to)
}

// windowEntry is one send: when (unix milliseconds) and to how many.
type windowEntry struct {
	T int64 `json:"t"`
	N int   `json:"n"`
}

// loadWindow returns the sends of the last minute. An unreadable window counts
// as empty; the host's own limiter still applies.
func loadWindow() []windowEntry {
	var all []windowEntry
	if _, err := kvGet(mailWindowKey, &all); err != nil {
		logf("warn", "uptime-monitor: mail window unreadable code=%s", hostErrCode(err))
		return nil
	}
	cut := now().Add(-mailWindow).UnixMilli()
	var live []windowEntry
	for _, e := range all {
		if e.T > cut {
			live = append(live, e)
		}
	}
	return live
}

func windowCount(w []windowEntry) int {
	n := 0
	for _, e := range w {
		n += e.N
	}
	return n
}

type sendOutcome int

const (
	sentOK sendOutcome = iota
	windowFull
	hostLimited
	mailUnavailable
	sendFailed
)

// sendWindowed sends one mail unless it would put more than mailWindowMax
// recipients in the last minute.
func sendWindowed(to []string, subject, body string) (sendOutcome, error) {
	w := loadWindow()
	if windowCount(w)+len(to) > mailWindowMax {
		return windowFull, nil
	}
	if err := mailSend(to, subject, body); err != nil {
		switch hostErrCode(err) {
		case sdk.ErrRateLimited:
			return hostLimited, err
		case sdk.ErrUnavailable:
			if err := kvSet(mailUnavailableKey, true); err != nil {
				logf("warn", "uptime-monitor: mail state not saved code=%s", hostErrCode(err))
			}
			return mailUnavailable, err
		}
		return sendFailed, err
	}
	if !mailConfigured() {
		_ = kvDelete(mailUnavailableKey)
	}
	w = append(w, windowEntry{T: now().UnixMilli(), N: len(to)})
	if err := kvSet(mailWindowKey, w); err != nil {
		logf("warn", "uptime-monitor: mail window not saved code=%s", hostErrCode(err))
	}
	return sentOK, nil
}

// flushOutbox sends queued mail, oldest first, until deadline. Mail older than
// 24 hours is dropped. A row that does not fit the minute's window waits; a
// rate limit or missing mail service stops the run and keeps every row.
func flushOutbox(deadline time.Time) error {
	if n, err := dropOldOutbox(now().Add(-mailMaxAge)); err != nil {
		return err
	} else if n > 0 {
		logf("warn", "uptime-monitor: dropped %d alert mails unsent after 24 hours", n)
	}
	rows, err := listOutbox(outboxBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !now().Before(deadline) {
			return nil
		}
		outcome, sendErr := sendWindowed(row.Recipients, row.Subject, row.Body)
		switch outcome {
		case sentOK:
			if err := deleteOutbox(row.ID); err != nil {
				return err
			}
		case windowFull:
			// Later, smaller mail may still fit.
		case hostLimited, mailUnavailable:
			logf("warn", "uptime-monitor: alert mail kept code=%s", hostErrCode(sendErr))
			if outcome == hostLimited {
				_ = bumpOutboxAttempts(row.ID)
			}
			return nil
		default:
			logf("warn", "uptime-monitor: alert mail failed code=%s", hostErrCode(sendErr))
			if err := bumpOutboxAttempts(row.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// sendTestAlert sends one test mail to every address in to through the same
// limiter. It returns the number of addresses mailed, or why nothing was sent.
func sendTestAlert(to []string) (int, string) {
	if len(to) == 0 {
		return 0, "no alert emails are set"
	}
	outcome, _ := sendWindowed(to, "[Test] Uptime monitor alert test",
		"This is a test alert from the uptime monitor. If you can read it, alert mail works.\n")
	switch outcome {
	case sentOK:
		return len(to), ""
	case windowFull, hostLimited:
		return 0, "mail limit reached (6 recipients per minute), try again in a minute"
	case mailUnavailable:
		return 0, "mail is not configured on this site"
	}
	return 0, "the mail could not be sent"
}
