package main

import (
	"encoding/json"
	"time"
)

// Every statement is a single statement (the host runs one per call) and every
// value reaches it as a $n parameter.

const (
	statusPending = "pending"
	statusUp      = "up"
	statusSlow    = "slow"
	statusDown    = "down"
	statusConfig  = "config"
)

// monitor is one monitored address with its current state; the json tags are
// the column names.
type monitor struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	URL         string     `json:"url"`
	IntervalS   int        `json:"interval_s"`
	Keyword     string     `json:"keyword"`
	MaxMS       int        `json:"max_ms"`
	IsSite      bool       `json:"is_site"`
	CreatedAt   time.Time  `json:"created_at"`
	Status      string     `json:"status"`
	LastCheck   *time.Time `json:"last_check"`
	NextCheck   time.Time  `json:"next_check"`
	LastMS      *int       `json:"last_ms"`
	LastStatus  *int       `json:"last_status"`
	LastError   string     `json:"last_error"`
	FailCount   int        `json:"fail_count"`
	DownSince   *time.Time `json:"down_since"`
	AlertedDown bool       `json:"alerted_down"`
}

// listMonitors returns every monitor, oldest first.
func listMonitors() ([]monitor, error) {
	var out []monitor
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(m) ORDER BY m.id), '[]'::jsonb) FROM monitors m`, &out)
	return out, err
}

// getMonitor reports false, with no error, when id does not exist.
func getMonitor(id int64) (monitor, bool, error) {
	var out []monitor
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(m)), '[]'::jsonb) FROM monitors m WHERE m.id = $1::bigint`, &out, id)
	if err != nil || len(out) == 0 {
		return monitor{}, false, err
	}
	return out[0], true, nil
}

func countMonitors() (int, error) {
	var n int
	err := dbJSON(`SELECT to_jsonb(count(*)) FROM monitors`, &n)
	return n, err
}

// insertMonitor stores a new monitor, due now, and returns its id.
func insertMonitor(m monitor) (int64, error) {
	var id int64
	err := dbJSON(`WITH i AS (
		INSERT INTO monitors (name, url, interval_s, keyword, max_ms, is_site)
		VALUES ($1::text, $2::text, $3::integer, $4::text, $5::integer, $6::boolean)
		RETURNING id) SELECT to_jsonb(i.id) FROM i`,
		&id, m.Name, m.URL, m.IntervalS, m.Keyword, m.MaxMS, m.IsSite)
	return id, err
}

// updateMonitor changes the owner-editable fields and makes the monitor due
// now, so an edit is checked on the next tick.
func updateMonitor(m monitor) error {
	return dbExec(`UPDATE monitors SET name = $2::text, url = $3::text, interval_s = $4::integer,
		  keyword = $5::text, max_ms = $6::integer, is_site = $7::boolean, next_check = now()
		WHERE id = $1::bigint`,
		m.ID, m.Name, m.URL, m.IntervalS, m.Keyword, m.MaxMS, m.IsSite)
}

// deleteMonitor removes the monitor and everything recorded for it.
func deleteMonitor(id int64) error {
	for _, sql := range []string{
		`DELETE FROM checks WHERE monitor_id = $1::bigint`,
		`DELETE FROM incidents WHERE monitor_id = $1::bigint`,
		`DELETE FROM daily WHERE monitor_id = $1::bigint`,
		`DELETE FROM monitors WHERE id = $1::bigint`,
	} {
		if err := dbExec(sql, id); err != nil {
			return err
		}
	}
	return nil
}

// recordCheck stores one check. ok is nil for a configuration problem, which
// counts neither as up nor as down.
func recordCheck(monitorID int64, at time.Time, httpStatus, ms int, ok *bool, cause string) error {
	return dbExec(`INSERT INTO checks (monitor_id, at, http_status, ms, ok, cause)
		VALUES ($1::bigint, $2::timestamptz, nullif($3::integer, 0), $4::integer, $5::boolean, $6::text)`,
		monitorID, ts(at), httpStatus, ms, ok, cause)
}

// cert is what is known about the certificate of one host.
type cert struct {
	Host        string     `json:"host"`
	NotAfter    *time.Time `json:"not_after"`
	Issuer      string     `json:"issuer"`
	LookedUpAt  *time.Time `json:"looked_up_at"`
	LookupError string     `json:"lookup_error"`
	Warned      []int      `json:"warned"`
}

// getCert reports false, with no error, for a host never looked up.
func getCert(host string) (cert, bool, error) {
	var out []cert
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(c)), '[]'::jsonb) FROM certs c WHERE c.host = $1::text`, &out, host)
	if err != nil || len(out) == 0 {
		return cert{}, false, err
	}
	return out[0], true, nil
}

// upsertCert stores c, replacing any earlier row of the host.
func upsertCert(c cert) error {
	warned, _ := json.Marshal(append([]int{}, c.Warned...))
	return dbExec(`INSERT INTO certs (host, not_after, issuer, looked_up_at, lookup_error, warned)
		VALUES ($1::text, $2::timestamptz, $3::text, $4::timestamptz, $5::text,
		  ARRAY(SELECT jsonb_array_elements_text($6::jsonb)::integer))
		ON CONFLICT (host) DO UPDATE SET not_after = EXCLUDED.not_after, issuer = EXCLUDED.issuer,
		  looked_up_at = EXCLUDED.looked_up_at, lookup_error = EXCLUDED.lookup_error, warned = EXCLUDED.warned`,
		c.Host, tsPtr(c.NotAfter), c.Issuer, tsPtr(c.LookedUpAt), c.LookupError, string(warned))
}

// saveState writes the state a check changes: status, timing, failure count
// and the down marker. Owner-editable fields are left alone.
func saveState(m monitor) error {
	return dbExec(`UPDATE monitors SET status = $2::text, last_check = $3::timestamptz,
		  next_check = $4::timestamptz, last_ms = $5::integer, last_status = nullif($6::integer, 0),
		  last_error = $7::text, fail_count = $8::integer, down_since = $9::timestamptz,
		  alerted_down = $10::boolean
		WHERE id = $1::bigint`,
		m.ID, m.Status, tsPtr(m.LastCheck), ts(m.NextCheck), m.LastMS, intOrZero(m.LastStatus),
		m.LastError, m.FailCount, tsPtr(m.DownSince), m.AlertedDown)
}

func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// incident is an outage; EndedAt is nil while it lasts.
type incident struct {
	ID        int64      `json:"id"`
	MonitorID int64      `json:"monitor_id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Cause     string     `json:"cause"`
}

// openIncident returns the monitor's ongoing incident, if any.
func openIncident(monitorID int64) (incident, bool, error) {
	var out []incident
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(i)), '[]'::jsonb) FROM
		(SELECT id, monitor_id, started_at, ended_at, cause FROM incidents
		 WHERE monitor_id = $1::bigint AND ended_at IS NULL ORDER BY id DESC LIMIT 1) i`, &out, monitorID)
	if err != nil || len(out) == 0 {
		return incident{}, false, err
	}
	return out[0], true, nil
}

// insertIncident opens an incident that has already crossed the alert point.
// alerted says whether a down mail is queued with it.
func insertIncident(monitorID int64, started time.Time, cause string, alerted bool) error {
	return dbExec(`INSERT INTO incidents (monitor_id, started_at, cause, alerted_down)
		VALUES ($1::bigint, $2::timestamptz, $3::text, $4::boolean)`, monitorID, ts(started), cause, alerted)
}

// closeIncidents ends the monitor's ongoing incidents. alertedUp records
// whether a recovery mail was queued.
func closeIncidents(monitorID int64, ended time.Time, alertedUp bool) error {
	return dbExec(`UPDATE incidents SET ended_at = $2::timestamptz, alerted_up = $3::boolean
		WHERE monitor_id = $1::bigint AND ended_at IS NULL`, monitorID, ts(ended), alertedUp)
}

// outboxRow is a mail waiting to be sent.
type outboxRow struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Subject    string    `json:"subject"`
	Body       string    `json:"body"`
	Recipients []string  `json:"recipients"`
	Attempts   int       `json:"attempts"`
}

func insertOutbox(subject, body string, to []string) error {
	recipients, _ := json.Marshal(append([]string{}, to...))
	return dbExec(`INSERT INTO outbox (subject, body, recipients)
		VALUES ($1::text, $2::text, ARRAY(SELECT jsonb_array_elements_text($3::jsonb)))`,
		subject, body, string(recipients))
}

// listOutbox returns up to limit unsent mails, oldest first.
func listOutbox(limit int) ([]outboxRow, error) {
	var out []outboxRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.id), '[]'::jsonb) FROM
		(SELECT id, created_at, subject, body, recipients, attempts FROM outbox ORDER BY id LIMIT $1::integer) r`,
		&out, limit)
	return out, err
}

func deleteOutbox(id int64) error {
	return dbExec(`DELETE FROM outbox WHERE id = $1::bigint`, id)
}

// claimOutbox takes the mail for sending and reports whether this run got it:
// false when another run holds a claim younger than outboxClaimFor.
func claimOutbox(id int64, at time.Time) (bool, error) {
	n, err := dbExecN(`UPDATE outbox SET claimed_at = $2::timestamptz
		WHERE id = $1::bigint AND (claimed_at IS NULL OR claimed_at < $3::timestamptz)`,
		id, ts(at), ts(at.Add(-outboxClaimFor)))
	return n == 1, err
}

// releaseOutbox gives a claimed mail back without counting an attempt.
func releaseOutbox(id int64) error {
	return dbExec(`UPDATE outbox SET claimed_at = NULL WHERE id = $1::bigint`, id)
}

// bumpOutboxAttempts counts a failed attempt and releases the claim.
func bumpOutboxAttempts(id int64) error {
	return dbExec(`UPDATE outbox SET attempts = attempts + 1, claimed_at = NULL WHERE id = $1::bigint`, id)
}

// dropOldOutbox deletes mail queued before cutoff and returns how many.
func dropOldOutbox(cutoff time.Time) (int, error) {
	var n int
	err := dbJSON(`WITH d AS (DELETE FROM outbox WHERE created_at < $1::timestamptz RETURNING 1)
		SELECT to_jsonb(count(*)) FROM d`, &n, ts(cutoff))
	return n, err
}

// dueMonitors returns the monitors whose next check is at or before at, the
// most overdue first.
func dueMonitors(at time.Time) ([]monitor, error) {
	var out []monitor
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(m) ORDER BY m.next_check, m.id), '[]'::jsonb)
		FROM monitors m WHERE m.next_check <= $1::timestamptz`, &out, ts(at))
	return out, err
}
