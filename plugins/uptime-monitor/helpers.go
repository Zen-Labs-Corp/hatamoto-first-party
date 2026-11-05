package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"hatamoto.dev/sdk"
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
// Callers never pass a heartbeat URL, an address or a mail body.
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

// hostErrCode returns the host error code of err (sdk.ErrTimeout, sdk.ErrBlocked,
// ...), or "" when err is not a host error.
func hostErrCode(err error) string {
	var he *sdk.HostError
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

// hostErr builds a host error, as the host returns one.
func hostErr(code, msg string) error { return &sdk.HostError{Code: code, Message: msg} }

// hostErrMessage returns the host's message for a host error, else err's text.
func hostErrMessage(err error) string {
	var he *sdk.HostError
	if errors.As(err, &he) {
		return he.Message
	}
	return err.Error()
}

// mailSend sends one plain text mail to every address in to (1..10).
func mailSend(to []string, subject, body string) error {
	return callHost("mail.send", map[string]any{"to": to, "subject": subject, "text": body}, nil)
}

// fetchReq is an http.fetch request; timeout_ms is capped by the host.
type fetchReq struct {
	Method    string `json:"method"`
	URL       string `json:"url"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// fetchRes is an http.fetch result. Truncated: the body was cut at the host's
// size limit.
type fetchRes struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// fetch runs one http.fetch. Redirects are followed by the host.
func fetch(req fetchReq) (fetchRes, error) {
	var res fetchRes
	err := callHost("http.fetch", req, &res)
	return res, err
}

// ts formats t for a timestamptz parameter.
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// tsPtr is ts for an optional time: nil stays NULL.
func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}
