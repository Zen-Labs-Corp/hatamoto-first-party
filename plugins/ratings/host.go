package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// callHost is the host API seam: guest.Call inside the wasm module (see
// host_wasip1.go), a fake in tests.
var callHost func(fn string, args, out any) error

// dbExec runs one statement that changes rows.
func dbExec(sql string, args ...any) error {
	_, err := dbExecN(sql, args...)
	return err
}

// dbExecN runs one statement that changes rows and returns how many it changed.
func dbExecN(sql string, args ...any) (int, error) {
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost("db.exec", map[string]any{"sql": sql, "args": args}, &res)
	return res.RowsAffected, err
}

// dbRows runs a query and returns its rows; each cell is raw JSON.
func dbRows(sql string, args ...any) ([][]json.RawMessage, error) {
	var res struct {
		Rows [][]json.RawMessage `json:"rows"`
	}
	if err := callHost("db.query", map[string]any{"sql": sql, "args": args}, &res); err != nil {
		return nil, err
	}
	return res.Rows, nil
}

// cellInt reads a number cell that may arrive as a JSON number or string.
func cellInt(c json.RawMessage) int {
	return int(cellFloat(c))
}

// cellFloat reads a number cell that may arrive as a JSON number or string.
func cellFloat(c json.RawMessage) float64 {
	var f float64
	if json.Unmarshal(c, &f) == nil {
		return f
	}
	var s string
	if json.Unmarshal(c, &s) == nil {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f
		}
	}
	return 0
}

// cellString reads a text cell.
func cellString(c json.RawMessage) string {
	var s string
	_ = json.Unmarshal(c, &s)
	return s
}
