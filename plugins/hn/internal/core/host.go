package core

import (
	"encoding/json"
	"errors"
)

// Call runs a host function: guest.Call in the wasm build (set by the root
// host_wasip1.go), a fake in tests. Read it lazily, never at init.
var Call func(fn string, args, out any) error

// ErrNoRows is returned by QueryJSON when the query returned no row.
var ErrNoRows = errors.New("core: no rows")

// ErrTruncated is returned by Query (with the rows the host did send) when the
// host cut the result at its row or size cap. A caller that cannot work with
// partial rows treats it as a failure; add a LIMIT to stay under the cap.
var ErrTruncated = errors.New("core: query result truncated")

var errNoHost = errors.New("core: host not available")

// Exec runs ONE statement that changes rows and returns how many it changed.
//
// Args travel as JSON: a Go slice or map reaches Postgres as JSON text, not as
// an array. Pass a list as one JSON argument and expand it in SQL
// (jsonb_array_elements_text($1::jsonb)); "= ANY($1)" does not work.
func Exec(sql string, args ...any) (int64, error) {
	if Call == nil {
		return 0, errNoHost
	}
	var res struct {
		RowsAffected int64 `json:"rows_affected"`
	}
	err := Call("db.exec", map[string]any{"sql": sql, "args": args}, &res)
	return res.RowsAffected, err
}

// Query runs a read and returns the raw cells of every row. Args follow the
// rules of Exec (a slice arrives as JSON text). When the host truncated the
// result it returns the rows it got together with ErrTruncated.
func Query(sql string, args ...any) ([][]json.RawMessage, error) {
	if Call == nil {
		return nil, errNoHost
	}
	var res struct {
		Rows      [][]json.RawMessage `json:"rows"`
		Truncated bool                `json:"truncated"`
	}
	if err := Call("db.query", map[string]any{"sql": sql, "args": args}, &res); err != nil {
		return nil, err
	}
	if res.Truncated {
		return res.Rows, ErrTruncated
	}
	return res.Rows, nil
}

// QueryJSON runs a query whose first cell of the first row is a json document
// and decodes it into out. Selecting one document keeps column typing out of
// the guest. It returns ErrNoRows when there is no row.
func QueryJSON(sql string, out any, args ...any) error {
	rows, err := Query(sql, args...)
	if err != nil {
		return err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ErrNoRows
	}
	cell := rows[0][0]
	// jsonb may arrive as a JSON value or as a JSON string holding JSON.
	var s string
	if json.Unmarshal(cell, &s) == nil && len(s) > 0 && (s[0] == '[' || s[0] == '{') {
		cell = json.RawMessage(s)
	}
	return json.Unmarshal(cell, out)
}
