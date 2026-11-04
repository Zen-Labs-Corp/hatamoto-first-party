package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

// callHost runs a host function. The wasm build sets it to guest.Call; unit
// tests replace it with a fake.
var callHost func(fn string, args, out any) error

// fetchTimeoutMS stays under the 10 s the host allows an action hook.
const fetchTimeoutMS = 8000

// dbExecRows runs one statement that changes rows and returns how many.
func dbExecRows(sql string, args ...any) (int64, error) {
	var res struct {
		RowsAffected int64 `json:"rows_affected"`
	}
	err := callHost("db.exec", map[string]any{"sql": sql, "args": args}, &res)
	return res.RowsAffected, err
}

// dbExec runs one statement that changes rows.
func dbExec(sql string, args ...any) error {
	_, err := dbExecRows(sql, args...)
	return err
}

// dbJSON runs a query that returns a single jsonb (or json) cell and decodes
// it into out. Selecting one document keeps column typing out of the guest.
func dbJSON(sql string, out any, args ...any) error {
	var res struct {
		Rows [][]json.RawMessage `json:"rows"`
	}
	if err := callHost("db.query", map[string]any{"sql": sql, "args": args}, &res); err != nil {
		return err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return errors.New("query returned no rows")
	}
	cell := res.Rows[0][0]
	// jsonb may arrive as a JSON value or as a JSON string holding JSON.
	var s string
	if json.Unmarshal(cell, &s) == nil && len(s) > 0 && (s[0] == '[' || s[0] == '{') {
		cell = json.RawMessage(s)
	}
	return json.Unmarshal(cell, out)
}

// logf writes one log line. The host rate-limits logs, so its error is ignored.
func logf(level, format string, a ...any) {
	_ = callHost("log", map[string]string{"level": level, "message": fmt.Sprintf(format, a...)}, nil)
}

type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// postJSON sends a JSON body through the host's http.fetch. A non-2xx answer
// comes back as a status, not as an error.
func postJSON(url string, body []byte) (fetched, error) {
	var res fetched
	err := callHost("http.fetch", map[string]any{
		"method":     "POST",
		"url":        url,
		"headers":    map[string]string{"Content-Type": "application/json"},
		"body":       string(body),
		"timeout_ms": fetchTimeoutMS,
	}, &res)
	return res, err
}
