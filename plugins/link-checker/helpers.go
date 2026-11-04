package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// now is replaced in tests.
var now = time.Now

// dbExec runs one statement that changes rows.
func dbExec(sql string, args ...any) error {
	return callHost("db.exec", map[string]any{"sql": sql, "args": args}, nil)
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

// logf writes one log line; the host rate limits it and a failure is ignored.
func logf(level, format string, a ...any) {
	_ = callHost("log", map[string]string{"level": level, "message": fmt.Sprintf(format, a...)}, nil)
}

// kvGet decodes the value stored under key into out. It reports false, with no
// error, when the key does not exist.
func kvGet(key string, out any) (bool, error) {
	var got struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := callHost("kv.get", map[string]string{"key": key}, &got); err != nil {
		return false, err
	}
	if !got.Found {
		return false, nil
	}
	return true, json.Unmarshal(got.Value, out)
}

// kvSet stores v (any JSON value) under key.
func kvSet(key string, v any) error {
	return callHost("kv.set", map[string]any{"key": key, "value": v}, nil)
}

// kvDelete removes key; a missing key is not an error.
func kvDelete(key string) error {
	return callHost("kv.delete", map[string]string{"key": key}, nil)
}
