package main

import (
	"fmt"
	"strings"
	"time"
)

// applyResult records one check and moves the monitor through the alert state
// machine. It returns the updated monitor. Mail is only queued here (see
// outbox.go): one down mail when the failures reach the threshold, one
// recovery mail after an alerted incident. A skipped check changes nothing.
func applyResult(m monitor, r result, st settings) (monitor, error) {
	if r.Skip {
		return m, nil
	}
	at := now()
	m.LastCheck = &at
	m.NextCheck = at.Add(time.Duration(m.IntervalS) * time.Second)
	m.LastMS = &r.MS
	m.LastStatus = &r.HTTP
	m.LastError = r.Cause
	m.Status = r.Status

	var ok *bool
	var err error
	switch r.Status {
	case statusUp, statusSlow:
		t := true
		ok = &t
		m, err = recovered(m, at, st)
	case statusDown:
		f := false
		ok = &f
		m, err = failed(m, r, at, st)
	}
	if err != nil {
		return m, err
	}
	if err := recordCheck(m.ID, at, r.HTTP, r.MS, ok, r.Cause); err != nil {
		return m, err
	}
	return m, saveState(m)
}

// recovered handles a good check.
func recovered(m monitor, at time.Time, st settings) (monitor, error) {
	wasAlerted := m.AlertedDown
	since := m.DownSince
	m.FailCount, m.DownSince, m.AlertedDown = 0, nil, false
	m.LastError = ""
	if !wasAlerted {
		// An incident can be open without an alert (no emails were set): end it.
		if since != nil {
			return m, closeIncidents(m.ID, at, false)
		}
		return m, nil
	}
	cause := ""
	inc, found, err := openIncident(m.ID)
	if err != nil {
		return m, err
	}
	if found {
		since, cause = &inc.StartedAt, inc.Cause
	}
	queued := false
	if len(st.AlertEmails) > 0 {
		if err := enqueueMail(upSubject(m), upBody(m, since, at, cause), st.AlertEmails); err != nil {
			return m, err
		}
		queued = true
	}
	return m, closeIncidents(m.ID, at, queued)
}

// failed handles a failed check. The incident and the mail are written before
// the monitor is marked alerted, so a crash in between alerts again instead of
// never. The incident is opened once per outage, so that retry does not repeat
// it. Without alert emails nothing is queued and the monitor stays unalerted,
// so the mail is queued by the first check after an address exists.
func failed(m monitor, r result, at time.Time, st settings) (monitor, error) {
	m.FailCount++
	if m.DownSince == nil {
		m.DownSince = &at
	}
	expired := strings.HasPrefix(r.Cause, "certificate expired")
	if m.AlertedDown || (m.FailCount < st.FailThreshold && !expired) {
		return m, nil
	}
	if _, found, err := openIncident(m.ID); err != nil {
		return m, err
	} else if !found {
		if err := insertIncident(m.ID, *m.DownSince, r.Cause, len(st.AlertEmails) > 0); err != nil {
			return m, err
		}
	}
	if len(st.AlertEmails) == 0 {
		return m, nil
	}
	if err := enqueueMail(downSubject(m), downBody(m, r.Cause), st.AlertEmails); err != nil {
		return m, err
	}
	m.AlertedDown = true
	return m, nil
}

func downSubject(m monitor) string { return mailSubject("[Down] ", m.Name, " is down") }
func upSubject(m monitor) string   { return mailSubject("[Up] ", m.Name, " is back up") }

func downBody(m monitor, cause string) string {
	return fmt.Sprintf("%s is down.\n\nAddress: %s\nCause: %s\nSince: %s\nChecks failed: %d\n",
		m.Name, m.URL, cause, mailTime(*m.DownSince), m.FailCount)
}

func upBody(m monitor, since *time.Time, at time.Time, cause string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is back up.\n\nAddress: %s\n", m.Name, m.URL)
	if since != nil {
		fmt.Fprintf(&b, "Downtime: %s (since %s)\n", duration(at.Sub(*since)), mailTime(*since))
	}
	if cause != "" {
		fmt.Fprintf(&b, "Cause: %s\n", cause)
	}
	return b.String()
}

// mailSubject joins the parts into one line of at most 200 characters: line
// breaks in the name are dropped, as the host refuses them.
func mailSubject(prefix, name, suffix string) string {
	name = strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(name)), " ")
	if room := 200 - len(prefix) - len(suffix); len(name) > room {
		name = strings.ToValidUTF8(name[:room], "")
	}
	return prefix + name + suffix
}

func mailTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// duration renders d as days, hours and minutes, at least one minute.
func duration(d time.Duration) string {
	mins := max(int(d/time.Minute), 1)
	days, hours, mins := mins/1440, mins/60%24, mins%60
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d d", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d h", hours))
	}
	if mins > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d min", mins))
	}
	return strings.Join(parts, " ")
}
