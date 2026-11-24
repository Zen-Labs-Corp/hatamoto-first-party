package core_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
)

func TestHostExecAndQueryJSON(t *testing.T) {
	h := &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"db.exec": func(a json.RawMessage) (any, error) {
			var in struct {
				SQL  string `json:"sql"`
				Args []any  `json:"args"`
			}
			if err := json.Unmarshal(a, &in); err != nil || in.SQL != "UPDATE x SET a = $1" || len(in.Args) != 1 {
				t.Errorf("exec args %s", a)
			}
			return map[string]any{"rows_affected": 3}, nil
		},
		"db.query": func(json.RawMessage) (any, error) {
			// jsonb as a JSON string holding JSON
			return coretest.Rows(`{"n": 7}`), nil
		},
	}}
	coretest.Install(t, h)
	n, err := core.Exec("UPDATE x SET a = $1", 1)
	if err != nil || n != 3 {
		t.Fatalf("Exec = %d, %v", n, err)
	}
	var out struct{ N int }
	if err := core.QueryJSON("SELECT 1", &out); err != nil || out.N != 7 {
		t.Fatalf("QueryJSON = %+v, %v", out, err)
	}
}

func TestHostQueryJSONValueCellAndNoRows(t *testing.T) {
	rows := coretest.Rows(map[string]any{"n": 9})
	h := &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"db.query": func(json.RawMessage) (any, error) { return rows, nil },
	}}
	coretest.Install(t, h)
	var out struct{ N int }
	if err := core.QueryJSON("SELECT 1", &out); err != nil || out.N != 9 {
		t.Fatalf("QueryJSON = %+v, %v", out, err)
	}
	rows = map[string]any{"rows": [][]any{}}
	if err := core.QueryJSON("SELECT 1", &out); !errors.Is(err, core.ErrNoRows) {
		t.Fatalf("err = %v, want ErrNoRows", err)
	}
}

func TestHostNotAvailable(t *testing.T) {
	core.Call = nil
	if _, err := core.Exec("x"); err == nil {
		t.Error("Exec without host: nil error")
	}
	if err := core.QueryJSON("x", new(int)); err == nil {
		t.Error("QueryJSON without host: nil error")
	}
}

func TestHostQueryTruncated(t *testing.T) {
	h := &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"db.query": func(json.RawMessage) (any, error) {
			return map[string]any{"rows": [][]any{{1}, {2}}, "truncated": true}, nil
		},
	}}
	coretest.Install(t, h)
	rows, err := core.Query("SELECT n FROM t")
	if !errors.Is(err, core.ErrTruncated) || len(rows) != 2 {
		t.Fatalf("Query = %d rows, %v; want 2 rows and ErrTruncated", len(rows), err)
	}
	if err := core.QueryJSON("SELECT n FROM t", new(int)); !errors.Is(err, core.ErrTruncated) {
		t.Fatalf("QueryJSON err = %v, want ErrTruncated", err)
	}
}
