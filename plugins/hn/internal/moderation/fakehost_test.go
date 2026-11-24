package moderation

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
)

type sqlCall struct {
	SQL  string
	Args []any
}

// fakeDB is the data behind the fake host of a test.
type fakeDB struct {
	Settings map[string]any                              // settings.get answer ({} = defaults)
	Users    map[string]map[string]any                   // commenter_key -> users row
	Query    func(sql string, args []any) (any, error)   // other db.query calls; nil answers no rows
	Exec     func(sql string, args []any) (int64, error) // db.exec; nil answers 1 row affected
	Comments func(fn string, ids []string) (any, error)  // comments.mark_dead / comments.restore; nil answers all changed

	Queries, Execs []sqlCall
}

// user builds a users row.
func user(id int64, name string) map[string]any {
	return map[string]any{"id": id, "username": name, "karma": 10, "showdead": false, "commenter_key": "k" + strconv.FormatInt(id, 10), "created_at": "2020-01-01T00:00:00Z"}
}

// jsonRow answers a db.query with one json document.
func jsonRow(doc any) map[string]any { return coretest.Rows(doc) }

// install makes db the host of the test. No entities.* function is registered:
// moderation never calls them, so an unexpected call fails the test.
func install(t *testing.T, db *fakeDB) *coretest.Host {
	t.Helper()
	sqlArgs := func(a json.RawMessage) sqlCall {
		var c sqlCall
		if err := json.Unmarshal(a, &c); err != nil {
			t.Fatalf("bad sql args %s: %v", a, err)
		}
		return c
	}
	comments := func(fn string) func(json.RawMessage) (any, error) {
		return func(a json.RawMessage) (any, error) {
			var args sdk.CommentIDsArgs
			if err := json.Unmarshal(a, &args); err != nil {
				return nil, err
			}
			if db.Comments == nil {
				return map[string]any{"changed": len(args.IDs)}, nil
			}
			return db.Comments(fn, args.IDs)
		}
	}
	h := &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"settings.get": func(json.RawMessage) (any, error) {
			if db.Settings == nil {
				return map[string]any{}, nil
			}
			return db.Settings, nil
		},
		"db.query": func(a json.RawMessage) (any, error) {
			c := sqlArgs(a)
			if strings.Contains(c.SQL, "FROM users u WHERE u.commenter_key") && len(c.Args) == 1 {
				if row, ok := db.Users[c.Args[0].(string)]; ok {
					return coretest.Rows(row), nil
				}
				return map[string]any{"rows": [][]any{}}, nil
			}
			db.Queries = append(db.Queries, c)
			if db.Query == nil {
				return map[string]any{"rows": [][]any{}}, nil
			}
			return db.Query(c.SQL, c.Args)
		},
		"db.exec": func(a json.RawMessage) (any, error) {
			c := sqlArgs(a)
			db.Execs = append(db.Execs, c)
			n := int64(1)
			if db.Exec != nil {
				var err error
				if n, err = db.Exec(c.SQL, c.Args); err != nil {
					return nil, err
				}
			}
			return map[string]any{"rows_affected": n}, nil
		},
		sdk.FnCommentsMarkDead: comments(sdk.FnCommentsMarkDead),
		sdk.FnCommentsRestore:  comments(sdk.FnCommentsRestore),
	}}
	coretest.Install(t, h)
	return h
}

// noEntityCalls fails when the test made an entities.* host call.
func noEntityCalls(t *testing.T, h *coretest.Host) {
	t.Helper()
	for _, c := range h.Calls {
		if strings.HasPrefix(c, "entities.") {
			t.Errorf("unexpected host call %q", c)
		}
	}
}

// newRegistry registers stub phases around moderation.
func newRegistry(t *testing.T) *core.Registry {
	t.Helper()
	r := core.NewRegistry()
	for _, p := range core.Phases {
		fn := func(*core.Registry) {}
		if p == "moderation" {
			fn = Register
		}
		r.Register(p, fn)
	}
	return r
}

// pinNow fixes core.Now for the test.
func pinNow(t *testing.T, ts time.Time) {
	t.Helper()
	prev := core.Now
	core.Now = func() time.Time { return ts }
	t.Cleanup(func() { core.Now = prev })
}

// hostErr is a host failure with that code.
func hostErr(code string) error { return &sdk.HostError{Code: code, Message: "x"} }
