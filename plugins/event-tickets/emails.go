package main

import (
	"errors"
	"strconv"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	emailsBudget = 9 * time.Second // the cron call may run 10 s
	defaultQuota = 360             // mail.send recipients per hour when the host does not say
	fanoutLimit  = 1000            // rows one fan-out statement adds per run
)

func init() {
	guest.OnCron("emails", func(sdk.CronEvent) error {
		runEmails(emailsBudget)
		return nil
	})
}

// automations are the owner's switches for the mails the plugin sends on its
// own. They live in meta so the settings schema stays as it is.
type automations struct {
	Changed, Reminder, Thanks bool
	ReminderHours             int
}

const (
	metaChanged       = "email.changed"
	metaReminder      = "email.reminder"
	metaThanks        = "email.thanks"
	metaReminderHours = "email.reminder_hours"
)

func defaultAutomations() automations {
	return automations{Changed: true, Reminder: true, ReminderHours: 24}
}

// loadAutomations reads the switches; a missing value means its default. A
// failed read is an error: the caller must not guess, as the defaults could
// turn on a mail the owner switched off.
func loadAutomations() (automations, error) {
	a := defaultAutomations()
	var m map[string]string
	if err := dbJSON(`SELECT coalesce(jsonb_object_agg(key, value), '{}'::jsonb) FROM meta WHERE key LIKE 'email.%'`, &m); err != nil {
		return a, err
	}
	flag := func(key string, def bool) bool {
		switch m[key] {
		case "true":
			return true
		case "false":
			return false
		}
		return def
	}
	a.Changed, a.Reminder, a.Thanks = flag(metaChanged, a.Changed), flag(metaReminder, a.Reminder), flag(metaThanks, a.Thanks)
	if h, err := strconv.Atoi(m[metaReminderHours]); err == nil {
		a.ReminderHours = min(max(h, 1), 168)
	}
	return a, nil
}

// mailQuota is how many recipients per hour the owner allows this plugin;
// the default when the host does not report one.
func mailQuota() int {
	var p sdk.PermissionsResult
	if err := callHost("permissions", struct{}{}, &p); err != nil || p.Mail == nil || p.Mail.RecipientsPerHour <= 0 {
		return defaultQuota
	}
	return p.Mail.RecipientsPerHour
}

// bulkBudget is how many bulk mails one 5-minute run may send: three quarters
// of the hourly quota spread over the 12 runs, so transactional mail (tickets,
// refunds, waitlist) keeps the rest.
func bulkBudget(quota int) int { return max(1, quota*3/4/12) }

// fanouts turn due work (changes, reminders, thank-yous, campaigns) into
// email_sends rows; finishers close what the drain completed. Other files add
// to them in init.
var (
	fanouts   []func(a automations, over func() bool)
	finishers []func()
	composers = map[string]func(s sendRow) (outMail, bool, error){}
)

// runEmails is the 5-minute job: fan out what is due, send within the quota
// budget, close finished campaigns. It never fails the cron.
func runEmails(budget time.Duration) {
	deadline := now().Add(budget)
	over := func() bool { return !now().Before(deadline) }
	a, err := loadAutomations()
	if err != nil {
		return // fail closed: without the switches nothing is fanned out or sent
	}
	for _, f := range fanouts {
		if over() {
			return
		}
		f(a, over)
	}
	if over() {
		return
	}
	drainSends(a, over, bulkBudget(mailQuota()))
	for _, f := range finishers {
		f()
	}
}

// sendRow is a claimed email_sends row.
type sendRow struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	RefID     int64  `json:"ref_id"`
	OrderID   int64  `json:"order_id"`
	EventID   string `json:"event_id"`
	EmailHash string `json:"email_hash"`
	Code      string `json:"code"`
	Attempts  int    `json:"attempts"`
}

// claimSend takes the next due send in one statement; the lease hides it from
// a run that overlaps. Changes go first, campaigns last. nil when none is due.
func claimSend() (*sendRow, error) {
	var s *sendRow
	err := dbJSON(`WITH c AS (SELECT id FROM email_sends WHERE status = 'queued' AND next_at <= now()
    ORDER BY CASE kind WHEN 'changed' THEN 0 WHEN 'reminder' THEN 1 WHEN 'thanks' THEN 2 ELSE 3 END, id
    LIMIT 1 FOR UPDATE SKIP LOCKED),
 u AS (UPDATE email_sends s SET next_at = now() + interval '`+mailLease+`' FROM c WHERE s.id = c.id
    RETURNING s.id, s.kind, s.ref_id, s.order_id, s.event_id, s.email_hash, s.code, s.attempts)
SELECT coalesce((SELECT to_jsonb(u) FROM u), 'null'::jsonb)`, &s)
	return s, err
}

// setSend ends a send: status skipped or failed, with no mail.
func setSend(id int64, status string) {
	_ = dbExec(`UPDATE email_sends SET status = $2 WHERE id = $1`, id, status)
}

func (a automations) allows(kind string) bool {
	switch kind {
	case "changed":
		return a.Changed
	case "reminder":
		return a.Reminder
	case "thanks":
		return a.Thanks
	}
	return true
}

func unsubscribed(hash string) (bool, error) {
	var yes bool
	err := dbJSON(`SELECT to_jsonb(EXISTS(SELECT 1 FROM unsubscribes WHERE email_hash = $1))`, &yes, hash)
	return yes, err
}

// drainSends sends queued bulk mail, at most limit mails before the deadline.
// A send whose switch is off, whose person unsubscribed since the fan-out or
// that has nothing left to say is skipped. Rate-limit and unavailable answers
// stop the run without counting an attempt; any other error backs that send off.
func drainSends(a automations, over func() bool, limit int) (sent int) {
	for sent < limit && !over() {
		s, err := claimSend()
		if err != nil || s == nil {
			return sent
		}
		if !a.allows(s.Kind) {
			setSend(s.ID, "skipped")
			continue
		}
		if gone, err := unsubscribed(s.EmailHash); err != nil {
			return sent
		} else if gone {
			setSend(s.ID, "skipped")
			continue
		}
		compose := composers[s.Kind]
		if compose == nil {
			setSend(s.ID, "skipped")
			continue
		}
		m, ok, err := compose(*s)
		if err == nil && !ok {
			setSend(s.ID, "skipped")
			continue
		}
		if err == nil {
			err = sendMail(m)
		}
		var he *sdk.HostError
		switch {
		case err == nil:
			_ = dbExec(`UPDATE email_sends SET status = 'sent', sent_at = now() WHERE id = $1`, s.ID)
			sent++
		case errors.As(err, &he) && (he.Code == sdk.ErrRateLimited || he.Code == sdk.ErrUnavailable):
			_ = dbExec(`UPDATE email_sends SET next_at = now() WHERE id = $1`, s.ID) // no attempt used, the rest waits for the next run
			return sent
		case s.Attempts+1 >= maxMailAttempts:
			setSend(s.ID, "failed")
		default:
			delay := min(mailBackoff<<s.Attempts, maxMailBackoff)
			_ = dbExec(`UPDATE email_sends SET attempts = attempts + 1, next_at = now() + $2 * interval '1 second' WHERE id = $1`,
				s.ID, int64(delay/time.Second))
		}
	}
	return sent
}
