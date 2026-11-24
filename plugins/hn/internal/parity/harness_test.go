package parity

import (
	"encoding/json"
	"net/url"
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
	Settings map[string]any                                // settings.get answer ({} = defaults)
	Users    map[string]map[string]any                     // commenter_key -> users row
	Query    func(sql string, args []any) (any, error)     // other db.query calls; nil answers no rows
	Exec     func(sql string, args []any) (int64, error)   // db.exec; nil answers 1 row affected
	Entities map[string]func(json.RawMessage) (any, error) // "entities.create" etc.

	Queries, Execs []sqlCall
}

// user builds a users row.
func user(id int64, name string) map[string]any {
	return map[string]any{"id": id, "username": name, "karma": 10, "showdead": false, "commenter_key": "k" + strconv.FormatInt(id, 10), "created_at": "2020-01-01T00:00:00Z",
		"noprocrast": false, "maxvisit": 20, "minaway": 180, "about": ""}
}

// install makes db the host of the test and returns it for call-order checks.
func install(t *testing.T, db *fakeDB) *coretest.Host {
	t.Helper()
	sqlArgs := func(a json.RawMessage) sqlCall {
		var c sqlCall
		if err := json.Unmarshal(a, &c); err != nil {
			t.Fatalf("bad sql args %s: %v", a, err)
		}
		return c
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
			if strings.Contains(c.SQL, "FROM users u WHERE ") && len(c.Args) == 1 {
				for _, row := range db.Users {
					if userMatches(c.SQL, c.Args[0], row) {
						return coretest.Rows(row), nil
					}
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
	}}
	for fn, f := range db.Entities {
		h.Fns[fn] = f
	}
	coretest.Install(t, h)
	return h
}

func userMatches(sql string, arg any, row map[string]any) bool {
	switch {
	case strings.Contains(sql, "u.commenter_key"):
		return row["commenter_key"] == arg
	case strings.Contains(sql, "u.username_lower"):
		s, _ := arg.(string)
		name, _ := row["username"].(string)
		return strings.EqualFold(name, s)
	case strings.Contains(sql, "u.id"):
		f, _ := arg.(float64)
		id, _ := row["id"].(int64)
		return id == int64(f)
	}
	return false
}

// newRegistry registers every phase in order: parity for real, the others
// from others (or as no-ops).
func newRegistry(t *testing.T, others map[string]func(*core.Registry)) *core.Registry {
	t.Helper()
	r := core.NewRegistry()
	for _, p := range core.Phases {
		fn := others[p]
		if p == "parity" {
			fn = Register
		}
		if fn == nil {
			fn = func(*core.Registry) {}
		}
		r.Register(p, fn)
	}
	return r
}

// render runs render.page over blocks for a path.
func render(t *testing.T, r *core.Registry, path, key string, query map[string]string, blocks ...sdk.Block) sdk.PageContext {
	t.Helper()
	out, err := r.RenderPage(sdk.PageContext{Path: path, Query: query, CommenterKey: key, Blocks: blocks})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// post sends a form to a route key such as "POST /vote/story/{id}".
func post(t *testing.T, r *core.Registry, key string, pathValues map[string]string, form url.Values, commenterKey string, wantJSON bool) sdk.HTTPResponse {
	t.Helper()
	method, pattern, _ := strings.Cut(key, " ")
	headers := map[string]string{"sec-fetch-site": "same-origin"}
	if wantJSON {
		headers["accept"] = "application/json"
	}
	resp, err := r.ServeHTTP(sdk.HTTPRequest{Method: method, Pattern: pattern, PathValues: pathValues, Headers: headers, Body: form.Encode(), CommenterKey: commenterKey})
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return resp
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

// location is the redirect target of a response.
func location(resp sdk.HTTPResponse) string { return resp.Headers["Location"] }

// mustTime parses an RFC 3339 time.
func mustTime(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}
