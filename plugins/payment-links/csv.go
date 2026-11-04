package main

import (
	"bytes"
	"encoding/csv"
	"strings"
	"time"
)

var csvHeader = []string{"date", "label", "kind", "amount", "currency", "email", "session_id", "event_id", "livemode"}

// csvCell neutralises a cell a spreadsheet would run as a formula: one that
// starts with = + - @ or a tab or carriage return gets a leading quote.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// maxCSVBytes keeps the file well under the 1 MiB answer cap of a plugin call,
// leaving room for the JSON escaping of the body (quotes and newlines grow it).
const maxCSVBytes = 700 << 10

// paymentsCSV renders rows, newest first as given, with a header row. It stops
// before the output would pass maxBytes and reports how many rows it wrote.
func paymentsCSV(rows []payment, maxBytes int) (string, int, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(csvHeader); err != nil {
		return "", 0, err
	}
	for i, p := range rows {
		live := "false"
		if p.Live {
			live = "true"
		}
		rec := []string{
			p.Created.UTC().Format(time.RFC3339), csvCell(p.Label), p.Kind, decimalAmount(p.Amount, p.Currency),
			p.Currency, csvCell(p.Email), p.SessionID, p.EventID, live,
		}
		w.Flush()
		before := buf.Len()
		if err := w.Write(rec); err != nil {
			return "", 0, err
		}
		w.Flush()
		if buf.Len() > maxBytes {
			buf.Truncate(before)
			return buf.String(), i, w.Error()
		}
	}
	w.Flush()
	return buf.String(), len(rows), w.Error()
}
