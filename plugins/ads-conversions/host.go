package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

// callHost is the host API seam: guest.Call inside the wasm module (see
// host_wasip1.go), a fake in tests.
var callHost func(fn string, args, out any) error

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

// kvGet reads one key; found is false when it is not set.
func kvGet(key string, out any) (found bool, err error) {
	var res struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := callHost("kv.get", map[string]string{"key": key}, &res); err != nil {
		return false, err
	}
	if !res.Found {
		return false, nil
	}
	if out != nil {
		if err := json.Unmarshal(res.Value, out); err != nil {
			return false, err
		}
	}
	return true, nil
}

// kvSet stores a key without expiry (ttl 0) or with a lifetime in seconds.
func kvSet(key string, value any, ttlSeconds int) error {
	args := map[string]any{"key": key, "value": value}
	if ttlSeconds > 0 {
		args["ttl_seconds"] = ttlSeconds
	}
	return callHost("kv.set", args, nil)
}

// kvDelete removes a key; a missing key is not an error.
func kvDelete(key string) error {
	return callHost("kv.delete", map[string]string{"key": key}, nil)
}

// logf writes one log line. The host rate-limits logs, so its error is ignored.
func logf(level, format string, a ...any) {
	_ = callHost("log", map[string]string{"level": level, "message": fmt.Sprintf(format, a...)}, nil)
}
