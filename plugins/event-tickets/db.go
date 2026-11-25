package main

import (
	"encoding/json"
	"errors"

	"hatamoto.dev/sdk"
)

// Every statement is one call: the host's SQL guard takes a single statement.

// dbExec runs one statement that changes rows.
func dbExec(sql string, args ...any) error {
	return callHost(sdk.FnDBExec, map[string]any{"sql": sql, "args": args}, nil)
}

// dbExecN runs one statement that changes rows and returns how many it changed.
func dbExecN(sql string, args ...any) (int64, error) {
	var res struct {
		RowsAffected int64 `json:"rows_affected"`
	}
	err := callHost(sdk.FnDBExec, map[string]any{"sql": sql, "args": args}, &res)
	return res.RowsAffected, err
}

// dbJSON runs a query that returns a single jsonb (or json) cell and decodes
// it into out. Selecting one document keeps column typing out of the guest.
func dbJSON(sql string, out any, args ...any) error {
	var res struct {
		Rows [][]json.RawMessage `json:"rows"`
	}
	if err := callHost(sdk.FnDBQuery, map[string]any{"sql": sql, "args": args}, &res); err != nil {
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
