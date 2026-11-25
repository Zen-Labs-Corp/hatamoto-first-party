package main

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// emailsHost fakes the bulk-mail statements: meta flags, the permissions
// answer, the send queue with its lease, the unsubscribe list and mail.send.
type emailsHost struct {
	meta     string // jsonb object of the email.* flags
	perms    string // permissions answer, "" = host error
	queue    []sendRow
	leased   map[int64]bool
	status   map[int64]string
	unsub    map[string]bool
	mailErrs []error // answers of mail.send in turn; nil = sent
	mails    []map[string]any
	writes   []call
}

func newEmailsHost(t *testing.T, n int) *emailsHost {
	h := &emailsHost{meta: `{}`, leased: map[int64]bool{}, status: map[int64]string{}, unsub: map[string]bool{}}
	for i := 1; i <= n; i++ {
		h.queue = append(h.queue, sendRow{ID: int64(i), Kind: "campaign", RefID: 1, OrderID: int64(i), EventID: "e1",
			EmailHash: strings.Repeat(strconv.Itoa(i%10), 64), Code: strings.Repeat("a", 32)})
	}
	oldComposers, oldFanouts := composers, fanouts
	fanouts = nil // the fan-outs have their own tests
	composers = map[string]func(s sendRow) (outMail, bool, error){
		"campaign": func(s sendRow) (outMail, bool, error) {
			return outMail{To: "ana@example.com", Subject: "Hi"}, true, nil
		},
		"reminder": func(s sendRow) (outMail, bool, error) {
			return outMail{To: "ana@example.com", Subject: "Hi"}, true, nil
		},
	}
	t.Cleanup(func() { composers, fanouts = oldComposers, oldFanouts })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		switch {
		case fn == "permissions":
			if h.perms == "" {
				return &sdk.HostError{Code: sdk.ErrUnavailable}
			}
			return jsonInto(h.perms, out)
		case fn == sdk.FnMailSend:
			h.mails = append(h.mails, a)
			if len(h.mailErrs) == 0 {
				return nil
			}
			err := h.mailErrs[0]
			h.mailErrs = h.mailErrs[1:]
			return err
		case strings.HasPrefix(sql, "UPDATE campaigns c SET status = 'sent'"):
			return nil // finishCampaigns
		case strings.Contains(sql, "FROM meta WHERE key LIKE"):
			return jsonInto(`{"rows":[[`+h.meta+`]]}`, out)
		case strings.Contains(sql, "FOR UPDATE SKIP LOCKED"):
			for _, q := range h.queue {
				if !h.leased[q.ID] && h.status[q.ID] == "" {
					h.leased[q.ID] = true
					return jsonInto(`{"rows":[[`+jsonText(q)+`]]}`, out)
				}
			}
			return jsonInto(`{"rows":[[null]]}`, out)
		case strings.Contains(sql, "FROM unsubscribes WHERE email_hash"):
			return jsonInto(`{"rows":[[`+jsonText(h.unsub[ar[0].(string)])+`]]}`, out)
		case strings.HasPrefix(sql, "UPDATE email_sends"):
			h.writes = append(h.writes, call{fn, sql, ar})
			id := ar[0].(int64)
			switch {
			case strings.Contains(sql, "status = 'sent'"):
				h.status[id] = "sent"
			case strings.Contains(sql, "SET status = $2"):
				h.status[id] = ar[1].(string)
			case strings.Contains(sql, "SET next_at = now() WHERE"):
				h.leased[id] = false // the lease is released
			}
			return nil
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *emailsHost) count(status string) int {
	n := 0
	for _, s := range h.status {
		if s == status {
			n++
		}
	}
	return n
}

func TestBulkBudget(t *testing.T) {
	for quota, want := range map[int]int{360: 22, 120: 7, 12: 1, 1: 1, 0: 1} {
		if got := bulkBudget(quota); got != want {
			t.Errorf("bulkBudget(%d) = %d, want %d", quota, got, want)
		}
	}
}

func TestMailQuotaDefaults(t *testing.T) {
	h := newEmailsHost(t, 0)
	for name, perms := range map[string]string{
		"no mail":    `{"granted":[],"requested":[]}`,
		"zero":       `{"mail":{"recipients_per_hour":0}}`,
		"host error": "",
	} {
		h.perms = perms
		if q := mailQuota(); q != 360 || bulkBudget(q) != 22 {
			t.Errorf("%s: quota %d budget %d, want 360 and 22", name, q, bulkBudget(q))
		}
	}
	h.perms = `{"mail":{"recipients_per_hour":120}}`
	if q := mailQuota(); q != 120 {
		t.Errorf("quota %d, want 120", q)
	}
}

func TestRunEmailsPacesWithinTheQuota(t *testing.T) {
	h := newEmailsHost(t, 20)
	h.perms = `{"mail":{"recipients_per_hour":120}}`
	runEmails(emailsBudget)
	if len(h.mails) != 7 || h.count("sent") != 7 {
		t.Fatalf("%d mails, %d sent, want 7", len(h.mails), h.count("sent"))
	}
	runEmails(emailsBudget) // the next run continues with the remaining sends
	if len(h.mails) != 14 || h.status[8] != "sent" || h.status[14] != "sent" || h.status[15] != "" {
		t.Errorf("second run: %d mails, status of 8/14/15 = %q %q %q", len(h.mails), h.status[8], h.status[14], h.status[15])
	}
}

func TestRunEmailsDefaultQuotaSends22(t *testing.T) {
	h := newEmailsHost(t, 30)
	runEmails(emailsBudget) // permissions fail: 360 per hour
	if len(h.mails) != 22 {
		t.Errorf("%d mails, want 22", len(h.mails))
	}
}

func TestDrainStopsAtRateLimitWithoutAnAttempt(t *testing.T) {
	h := newEmailsHost(t, 5)
	h.mailErrs = []error{nil, nil, &sdk.HostError{Code: sdk.ErrRateLimited}}
	sent := drainSends(defaultAutomations(), func() bool { return false }, 10)
	if sent != 2 || len(h.mails) != 3 {
		t.Fatalf("sent %d with %d attempts, want 2 and 3", sent, len(h.mails))
	}
	if h.status[3] != "" || h.leased[3] {
		t.Errorf("the rate-limited send must be released: status %q leased %v", h.status[3], h.leased[3])
	}
	for _, w := range h.writes {
		if strings.Contains(w.sql, "attempts = attempts + 1") {
			t.Errorf("a rate-limited send must not count an attempt: %s", w.sql)
		}
	}
	// the next run picks up from the released send
	if sent := drainSends(defaultAutomations(), func() bool { return false }, 10); sent != 3 || h.status[3] != "sent" || h.status[5] != "sent" {
		t.Errorf("next run sent %d, status of 3 = %q", sent, h.status[3])
	}
}

func TestDrainOtherErrorBacksOff(t *testing.T) {
	h := newEmailsHost(t, 2)
	h.mailErrs = []error{errors.New("smtp down")}
	sent := drainSends(defaultAutomations(), func() bool { return false }, 10)
	if sent != 1 || h.status[2] != "sent" {
		t.Errorf("sent %d, status of 2 = %q", sent, h.status[2])
	}
	var backoff *call
	for i, w := range h.writes {
		if strings.Contains(w.sql, "attempts = attempts + 1") {
			backoff = &h.writes[i]
		}
	}
	if backoff == nil || backoff.args[0].(int64) != 1 || backoff.args[1].(int64) != int64(mailBackoff.Seconds()) {
		t.Errorf("backoff write: %+v", backoff)
	}
}

func TestDrainSkipsUnsubscribedAndSwitchedOff(t *testing.T) {
	h := newEmailsHost(t, 3)
	h.queue[0].Kind = "reminder" // switched off below
	h.unsub[h.queue[1].EmailHash] = true
	a := defaultAutomations()
	a.Reminder = false
	sent := drainSends(a, func() bool { return false }, 10)
	if sent != 1 || len(h.mails) != 1 {
		t.Fatalf("sent %d, mails %d, want 1", sent, len(h.mails))
	}
	if h.status[1] != "skipped" || h.status[2] != "skipped" || h.status[3] != "sent" {
		t.Errorf("status %v", h.status)
	}
}

func TestDrainSkipsWhenNothingToSay(t *testing.T) {
	h := newEmailsHost(t, 1)
	composers["campaign"] = func(sendRow) (outMail, bool, error) { return outMail{}, false, nil }
	drainSends(defaultAutomations(), func() bool { return false }, 10)
	if h.status[1] != "skipped" || len(h.mails) != 0 {
		t.Errorf("status %q mails %d", h.status[1], len(h.mails))
	}
}

func TestLoadAutomations(t *testing.T) {
	h := newEmailsHost(t, 0)
	if a := loadAutomations(); a != (automations{true, true, false, 24}) {
		t.Errorf("defaults %+v", a)
	}
	h.meta = `{"email.changed":"false","email.thanks":"true","email.reminder_hours":"48"}`
	if a := loadAutomations(); a != (automations{false, true, true, 48}) {
		t.Errorf("stored %+v", a)
	}
	h.meta = `{"email.reminder_hours":"9999","email.changed":"maybe"}`
	if a := loadAutomations(); a.ReminderHours != 168 || !a.Changed {
		t.Errorf("clamped %+v", a)
	}
	h.meta = `{"email.reminder_hours":"x"}`
	if a := loadAutomations(); a.ReminderHours != 24 {
		t.Errorf("bad hours %+v", a)
	}
}

func TestRunEmailsFansOutBeforeSending(t *testing.T) {
	h := newEmailsHost(t, 1)
	var order []string
	fanouts = []func(automations, func() bool){func(automations, func() bool) { order = append(order, "fanout") }}
	composers["campaign"] = func(sendRow) (outMail, bool, error) {
		order = append(order, "send")
		return outMail{To: "a@example.com"}, true, nil
	}
	runEmails(emailsBudget)
	if strings.Join(order, ",") != "fanout,send" || len(h.mails) != 1 {
		t.Errorf("order %v", order)
	}
}
