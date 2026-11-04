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

// paymentsCSV renders rows, newest first as given, with a header row.
func paymentsCSV(rows []payment) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(csvHeader); err != nil {
		return "", err
	}
	for _, p := range rows {
		live := "false"
		if p.Live {
			live = "true"
		}
		rec := []string{
			p.Created.UTC().Format(time.RFC3339), csvCell(p.Label), p.Kind, decimalAmount(p.Amount, p.Currency),
			p.Currency, csvCell(p.Email), p.SessionID, p.EventID, live,
		}
		if err := w.Write(rec); err != nil {
			return "", err
		}
	}
	w.Flush()
	return buf.String(), w.Error()
}
