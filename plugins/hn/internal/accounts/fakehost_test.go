package accounts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
	"hatamoto.dev/sdk"
)

// fakeUsers is an in-memory users table behind the fake host.
type fakeUsers struct {
	host     *coretest.Host
	byKey    map[string]*core.User
	nextID   int64
	inserts  int
	updates  int
	queryErr error

	setViewer    []sdk.CommentViewerArgs
	setViewerErr error
}

var fakeCreated = time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)

func (f *fakeUsers) find(match func(*core.User) bool) *core.User {
	for _, u := range f.byKey {
		if match(u) {
			return u
		}
	}
	return nil
}

func (f *fakeUsers) add(key, name string) *core.User {
	f.nextID++
	u := &core.User{ID: f.nextID, CommenterKey: key, Username: name, Karma: 1, Maxvisit: 20, Minaway: 180, CreatedAt: fakeCreated}
	f.byKey[key] = u
	return u
}

// installFake installs a host that models the users table by SQL text.
func installFake(t *testing.T) *fakeUsers {
	t.Helper()
	f := &fakeUsers{byKey: map[string]*core.User{}}
	args := func(raw json.RawMessage) (string, []json.RawMessage) {
		var a struct {
			SQL  string            `json:"sql"`
			Args []json.RawMessage `json:"args"`
		}
		_ = json.Unmarshal(raw, &a)
		return a.SQL, a.Args
	}
	str := func(raw json.RawMessage) string {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	f.host = &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"db.query": func(raw json.RawMessage) (any, error) {
			if f.queryErr != nil {
				return nil, f.queryErr
			}
			sql, a := args(raw)
			var u *core.User
			switch {
			case strings.Contains(sql, "u.commenter_key = $1"):
				u = f.byKey[str(a[0])]
			case strings.Contains(sql, "u.username_lower = lower($1)"):
				want := strings.ToLower(str(a[0]))
				u = f.find(func(x *core.User) bool { return strings.ToLower(x.Username) == want })
			case strings.Contains(sql, "u.id = $1"):
				var id int64
				_ = json.Unmarshal(a[0], &id)
				u = f.find(func(x *core.User) bool { return x.ID == id })
			default:
				t.Errorf("fake: unexpected query %q", sql)
			}
			if u == nil {
				return map[string]any{"rows": [][]any{}}, nil
			}
			return coretest.Rows(u), nil
		},
		sdk.FnCommentsSetViewer: func(raw json.RawMessage) (any, error) {
			var a sdk.CommentViewerArgs
			_ = json.Unmarshal(raw, &a)
			f.setViewer = append(f.setViewer, a)
			return nil, f.setViewerErr
		},
		"db.exec": func(raw json.RawMessage) (any, error) {
			sql, a := args(raw)
			switch {
			case strings.HasPrefix(sql, "INSERT INTO users"):
				f.inserts++
				key, name := str(a[0]), str(a[1])
				if f.byKey[key] != nil || f.find(func(x *core.User) bool { return strings.EqualFold(x.Username, name) }) != nil {
					return map[string]any{"rows_affected": 0}, nil
				}
				f.add(key, name)
				return map[string]any{"rows_affected": 1}, nil
			case strings.HasPrefix(sql, "UPDATE users SET about"):
				f.updates++
				var (
					about  string
					sd, np bool
					mv, ma int
					id     int64
				)
				for i, dst := range []any{&about, &sd, &np, &mv, &ma, &id} {
					_ = json.Unmarshal(a[i], dst)
				}
				u := f.find(func(x *core.User) bool { return x.ID == id })
				if u == nil {
					return map[string]any{"rows_affected": 0}, nil
				}
				u.About, u.Showdead, u.Noprocrast, u.Maxvisit, u.Minaway = about, sd, np, mv, ma
				return map[string]any{"rows_affected": 1}, nil
			default:
				t.Errorf("fake: unexpected exec %q", sql)
				return map[string]any{"rows_affected": 0}, nil
			}
		},
	}}
	coretest.Install(t, f.host)
	return f
}
