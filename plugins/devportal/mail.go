package main

import (
	"errors"
	"strings"

	"hatamoto.dev/sdk"
)

// Mail is queued first and sent second: a failed or rate limited send never
// loses a notice and never fails the action that caused it.

const mailMaxAttempts = 5

type queuedMail struct {
	ID      int64  `json:"id"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// mailAddrOK is a light check that keeps header injection out: the host does
// the real address validation.
func mailAddrOK(addr string) bool {
	return strings.Contains(addr, "@") && !strings.ContainsAny(addr, "\r\n\t ,;<>")
}

// oneLine turns line breaks into spaces so a subject cannot carry headers.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// queueMail stores one mail for the cron (or notify) to send. Errors are
// logged without the address and never returned.
func queueMail(to, subject, body string) {
	to = strings.TrimSpace(to)
	if !mailAddrOK(to) {
		return
	}
	if err := dbExec(`INSERT INTO mail_queue (to_addr, subject, body) VALUES ($1, $2, $3)`, to, oneLine(subject), body); err != nil {
		logf("warn", "mail queue failed")
	}
}

// drain sends up to max pending mails, oldest first. A rate limit or an
// unconfigured mail service stops the loop and leaves the row; any other send
// error counts one attempt. The error is only from reading the queue.
func drain(max int) (int, error) {
	var rows []queuedMail
	err := dbJSON(`SELECT COALESCE(jsonb_agg(jsonb_build_object('id', q.id, 'to', q.to_addr, 'subject', q.subject, 'body', q.body) ORDER BY q.id), '[]'::jsonb)
FROM (SELECT id, to_addr, subject, body FROM mail_queue WHERE sent_at IS NULL AND attempts < $1 ORDER BY id LIMIT $2) q`, &rows, mailMaxAttempts, max)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, m := range rows {
		var res any
		err := callHost("mail.send", map[string]any{"to": []string{m.To}, "subject": m.Subject, "text": m.Body}, &res)
		if err == nil {
			if dbExec(`UPDATE mail_queue SET sent_at = now() WHERE id = $1`, m.ID) != nil {
				logf("warn", "mail sent but not marked")
			}
			sent++
			continue
		}
		var he *sdk.HostError
		if errors.As(err, &he) && (he.Code == sdk.ErrRateLimited || he.Code == sdk.ErrUnavailable) {
			break
		}
		_ = dbExec(`UPDATE mail_queue SET attempts = attempts + 1 WHERE id = $1`, m.ID)
	}
	return sent, nil
}

// drainMail sends up to max pending mails and reports how many went out.
func drainMail(max int) int {
	sent, _ := drain(max)
	return sent
}

// mailCron drains the queue, at most 5 per run (core allows 6 recipients a
// minute). Mail failures are not errors; only an unreadable queue is.
func mailCron(sdk.CronEvent) error {
	_, err := drain(5)
	return err
}

// notify queues a mail for the mail cron. It never sends: mail.send waits on
// the mail provider, and a slow one would push a click past its deadline.
func notify(to, subject, body string) {
	queueMail(to, subject, body)
}
