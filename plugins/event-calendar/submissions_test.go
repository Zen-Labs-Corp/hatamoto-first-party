package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// dbFake records the statements of db.exec and answers db.query and
// db.exec with what a test sets.
type dbFake struct {
	execs    []sqlCall
	queries  []sqlCall
	affected int    // rows_affected of every db.exec
	cell     string // the single jsonb cell db.query answers with
	meta     string // the stored signing key; "" = none yet
	execErr  error
}

type sqlCall struct {
	sql  string
	args []any
}

func (h *dbFake) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		call := sqlCall{sql: a["sql"].(string)}
		call.args, _ = a["args"].([]any)
		switch fn {
		case sdk.FnDBExec:
			h.execs = append(h.execs, call)
			if h.execErr != nil {
				return h.execErr
			}
			if strings.HasPrefix(call.sql, "INSERT INTO meta") && h.meta == "" {
				h.meta = call.args[1].(string)
			}
			if out != nil {
				return json.Unmarshal([]byte(`{"rows_affected":`+string(rune('0'+h.affected))+`}`), out)
			}
			return nil
		case sdk.FnDBQuery:
			h.queries = append(h.queries, call)
			cell := h.cell
			if strings.Contains(call.sql, "FROM meta") {
				if h.meta == "" {
					return errors.New("no row")
				}
				cell = `"` + h.meta + `"`
			}
			return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func TestHMACSecretGeneratedOnce(t *testing.T) {
	h := &dbFake{}
	h.install(t)
	first, err := hmacSecret()
	if err != nil || len(first) != 32 {
		t.Fatalf("secret %x, err %v", first, err)
	}
	second, err := hmacSecret()
	if err != nil || hex.EncodeToString(second) != hex.EncodeToString(first) {
		t.Errorf("the second call returned another key: %x vs %x (%v)", second, first, err)
	}
	for _, e := range h.execs {
		if !strings.Contains(e.sql, "ON CONFLICT (key) DO NOTHING") {
			t.Errorf("the insert must not overwrite: %s", e.sql)
		}
	}
	h2 := &dbFake{execErr: errors.New("db down")}
	h2.install(t)
	if _, err := hmacSecret(); err == nil {
		t.Error("a db error must be returned")
	}
}

func TestInsertSubmission(t *testing.T) {
	h := &dbFake{affected: 1}
	h.install(t)
	s := submission{Title: "Open mic", Start: "2027-06-12T19:00:00Z", End: "", Venue: "Town Hall", Description: "x", Email: "visitor@example.org"}
	if err := insertSubmission(s); err != nil {
		t.Fatal(err)
	}
	got := h.execs[0]
	if len(got.args) != 6 || got.args[0] != "Open mic" || got.args[5] != "visitor@example.org" || !strings.Contains(got.sql, "ON CONFLICT DO NOTHING") {
		t.Errorf("insert %+v", got)
	}
	h.affected = 0
	if err := insertSubmission(s); !errors.Is(err, errDuplicate) {
		t.Errorf("a conflict returns errDuplicate, got %v", err)
	}
}

func TestSubmissionReads(t *testing.T) {
	h := &dbFake{cell: `[{"id":7,"status":"pending","title":"Open mic","start_at":"2027-06-12T19:00:00Z","end_at":"","venue":"Town Hall","description":"","email":"visitor@example.org","created_at":"2027-01-01T10:00:00+00:00","notified":false}]`}
	h.install(t)
	list, err := listSubmissions("pending")
	if err != nil || len(list) != 1 || list[0].ID != 7 || list[0].Email != "visitor@example.org" || list[0].Title != "Open mic" {
		t.Fatalf("list %+v, %v", list, err)
	}
	if q := h.queries[0]; len(q.args) != 2 || q.args[0] != "pending" || q.args[1] != float64(maxSubmissionRows) && q.args[1] != maxSubmissionRows {
		t.Errorf("args %v", q.args)
	}
	if _, err := listSubmissions("all"); err != nil || len(h.queries[1].args) != 1 {
		t.Errorf("all: %v %v", err, h.queries[1].args)
	}
	got, ok, err := getSubmission(7)
	if err != nil || !ok || got.ID != 7 {
		t.Errorf("get %+v %v %v", got, ok, err)
	}
	h.cell = `[]`
	if _, ok, _ := getSubmission(8); ok {
		t.Error("a missing id is not found")
	}
	h.cell = `7`
	if n, err := pendingCount(); err != nil || n != 7 {
		t.Errorf("pending %d %v", n, err)
	}
	h.cell = `{"pending":2,"rejected":1}`
	if c, err := submissionCounts(); err != nil || c["pending"] != 2 || c["rejected"] != 1 {
		t.Errorf("counts %v %v", c, err)
	}
	h.cell = `[]`
	if l, err := unnotified(); err != nil || len(l) != 0 || !strings.Contains(h.queries[len(h.queries)-1].sql, "NOT notified") {
		t.Errorf("unnotified %v %v", l, err)
	}
}

func TestSetSubmissionStatus(t *testing.T) {
	h := &dbFake{affected: 1}
	h.install(t)
	if !setSubmissionStatus(5, "published", "pending", "approved") {
		t.Error("a changed row is true")
	}
	e := h.execs[0]
	if !strings.HasSuffix(e.sql, "AND status IN ($3, $4)") || len(e.args) != 4 || e.args[1] != "published" || e.args[2] != "pending" || e.args[3] != "approved" {
		t.Errorf("exec %+v", e)
	}
	setSubmissionStatus(5, "rejected")
	if e := h.execs[1]; strings.Contains(e.sql, "AND status") || len(e.args) != 2 {
		t.Errorf("without from: %+v", e)
	}
	h.affected = 0
	if setSubmissionStatus(5, "published", "pending") {
		t.Error("no row changed is false")
	}
	h.execErr = errors.New("db down")
	if setSubmissionStatus(5, "published") {
		t.Error("an error is false")
	}
}

func TestPruneAndNotified(t *testing.T) {
	h := &dbFake{affected: 1}
	h.install(t)
	now := time.Date(2027, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := pruneSubmissions(now); err != nil {
		t.Fatal(err)
	}
	e := h.execs[0]
	if e.args[0] != "2027-03-03T12:00:00Z" || e.args[1] != "2027-05-02T12:00:00Z" || strings.Contains(e.sql, "'pending'") {
		t.Errorf("prune %+v", e)
	}
	if err := markNotified(3, 4); err != nil || h.execs[1].args[0] != "[3,4]" {
		t.Errorf("markNotified %+v %v", h.execs[1], err)
	}
	if err := markNotified(); err != nil || len(h.execs) != 2 {
		t.Error("nothing to mark is no call")
	}
	if ok, err := deleteSubmission(3); !ok || err != nil {
		t.Errorf("delete %v %v", ok, err)
	}
}

func TestSubmissionMigration(t *testing.T) {
	raw, err := readFile(t, "migrations/004_submissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CREATE TABLE meta", "CREATE TABLE submissions", "submissions_dup_idx", "status IN ('pending', 'approved', 'published', 'rejected')"} {
		if !strings.Contains(raw, want) {
			t.Errorf("migration lacks %q", want)
		}
	}
	for _, bad := range []string{"FUNCTION", "TRIGGER", "GRANT", "public", "pg_"} {
		if strings.Contains(raw, bad) {
			t.Errorf("migration holds %q", bad)
		}
	}
}

func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	return string(b), err
}
