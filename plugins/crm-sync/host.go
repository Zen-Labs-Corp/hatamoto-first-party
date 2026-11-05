package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

// callHost is the host API seam: guest.Call inside the wasm module (see
// host_wasip1.go), a fake in tests.
var callHost func(fn string, args, out any) error

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

// kvGet reads a plugin key into out; false when the key is not set.
func kvGet(key string, out any) (bool, error) {
	var r struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := callHost("kv.get", map[string]string{"key": key}, &r); err != nil || !r.Found {
		return false, err
	}
	return true, json.Unmarshal(r.Value, out)
}

// kvSet stores a value; ttlSeconds <= 0 keeps it until it is overwritten.
func kvSet(key string, v any, ttlSeconds int) error {
	m := map[string]any{"key": key, "value": v}
	if ttlSeconds > 0 {
		m["ttl_seconds"] = ttlSeconds
	}
	return callHost("kv.set", m, nil)
}

// logf writes one host log line. Callers pass only counts and fixed words,
// never personal data, secrets or provider text.
func logf(level, format string, a ...any) {
	_ = callHost("log", map[string]string{"level": level, "message": "crm-sync: " + fmt.Sprintf(format, a...)}, nil)
}
