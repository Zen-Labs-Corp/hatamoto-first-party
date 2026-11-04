package main

import (
	"encoding/json"
	"time"
)

const (
	logLimit    = 50
	maxErrChars = 200
)

// logRow is one purge request batch as kept in the purge log.
type logRow struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Trigger  string    `json:"trigger"`
	Provider string    `json:"provider"`
	URLs     []string  `json:"urls"`
	Status   int       `json:"status"`
	Error    string    `json:"error"`
}

// insertLog records one batch and drops rows older than 14 days. A database
// failure is only logged: a purge never fails because its log could not be
// written.
func insertLog(trigger, provider string, urls []string, status int, errText string) {
	if urls == nil {
		urls = []string{}
	}
	b, _ := json.Marshal(urls)
	if r := []rune(errText); len(r) > maxErrChars {
		errText = string(r[:maxErrChars])
	}
	if err := dbExec("INSERT INTO purge_log (trigger, provider, urls, status, error) VALUES ($1, $2, $3::jsonb, $4, $5)",
		trigger, provider, string(b), status, errText); err != nil {
		logf("error", "cdn-purge: purge log insert failed: %v", err)
		return
	}
	if err := dbExec("DELETE FROM purge_log WHERE at < now() - interval '14 days'"); err != nil {
		logf("warn", "cdn-purge: purge log cleanup failed: %v", err)
	}
}

// listLog returns the newest rows first.
func listLog(limit int) ([]logRow, error) {
	if limit < 1 || limit > logLimit {
		limit = logLimit
	}
	rows := []logRow{}
	err := dbJSON("SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.at DESC, r.id DESC), '[]'::jsonb) FROM "+
		"(SELECT id, at, trigger, provider, urls, status, error FROM purge_log ORDER BY at DESC, id DESC LIMIT $1) r",
		&rows, limit)
	return rows, err
}
