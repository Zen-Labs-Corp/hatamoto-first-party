package core_test

import (
	"encoding/json"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
)

func TestUserByKey(t *testing.T) {
	h := &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"db.query": func(a json.RawMessage) (any, error) {
			var in struct{ Args []string }
			json.Unmarshal(a, &in)
			if len(in.Args) == 1 && in.Args[0] == "k1" {
				return coretest.Rows(map[string]any{"id": 5, "username": "ann", "karma": 12, "created_at": "2026-01-02T03:04:05Z"}), nil
			}
			return map[string]any{"rows": [][]any{}}, nil
		},
	}}
	coretest.Install(t, h)

	u, err := core.UserByKey("")
	if u != nil || err != nil || len(h.Calls) != 0 {
		t.Fatalf("empty key: %v %v calls %v", u, err, h.Calls)
	}
	u, err = core.UserByKey("k1")
	if err != nil || u == nil || u.ID != 5 || u.Username != "ann" || u.Karma != 12 || u.CreatedAt.Year() != 2026 {
		t.Fatalf("found: %+v %v", u, err)
	}
	u, err = core.UserByKey("nope")
	if u != nil || err != nil {
		t.Fatalf("not found: %+v %v", u, err)
	}
	if u, err = core.UserByName("ann"); u != nil || err != nil {
		t.Fatalf("by name (no match): %+v %v", u, err)
	}
}

func TestAddKarmaArgsInOrder(t *testing.T) {
	var got json.RawMessage
	h := &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"db.exec": func(a json.RawMessage) (any, error) { got = a; return map[string]any{"rows_affected": 1}, nil },
	}}
	coretest.Install(t, h)
	if err := core.AddKarma(9, -2); err != nil {
		t.Fatal(err)
	}
	var in struct {
		Args []int `json:"args"`
	}
	json.Unmarshal(got, &in)
	if len(in.Args) != 2 || in.Args[0] != -2 || in.Args[1] != 9 {
		t.Fatalf("args %s", got)
	}
}
