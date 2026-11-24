package polish

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
	"hatamoto.dev/sdk"
)

const (
	eid  = "10000000-0000-4000-8000-000000000001"
	cid1 = "00000000-0000-4000-8000-000000000001"
	cid2 = "00000000-0000-4000-8000-000000000002"
	cid3 = "00000000-0000-4000-8000-000000000003"
	key  = "k1secret"
)

// stateEnv is a fake host with one member (id 7, the given karma).
type stateEnv struct {
	host     *coretest.Host
	karma    int
	noUser   bool
	stateErr error
	settings map[string]any
	stateSQL string
	stateArg []json.RawMessage
}

func newEnv(t *testing.T, karma int) *stateEnv {
	e := &stateEnv{karma: karma, settings: map[string]any{}}
	e.host = &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"settings.get": func(json.RawMessage) (any, error) { return e.settings, nil },
		"db.query": func(raw json.RawMessage) (any, error) {
			var a struct {
				SQL  string            `json:"sql"`
				Args []json.RawMessage `json:"args"`
			}
			_ = json.Unmarshal(raw, &a)
			if strings.Contains(a.SQL, "u.commenter_key = $1") {
				if e.noUser {
					return map[string]any{"rows": [][]any{}}, nil
				}
				return coretest.Rows(map[string]any{"id": 7, "commenter_key": key, "username": "pg", "karma": e.karma}), nil
			}
			if !strings.Contains(a.SQL, "comment_meta") {
				t.Errorf("unexpected sql %q", a.SQL)
			}
			e.stateSQL, e.stateArg = a.SQL, a.Args
			if e.stateErr != nil {
				return nil, e.stateErr
			}
			return coretest.Rows(map[string]any{
				"story": map[string]any{"voted": true},
				"comments": map[string]any{
					cid1: map[string]any{"voted": "up", "own": false, "dead": false},
					cid2: map[string]any{"voted": "", "own": true, "dead": false},
					cid3: map[string]any{"voted": "", "own": false, "dead": true},
				},
			}), nil
		},
	}}
	coretest.Install(t, e.host)
	return e
}

func serve(t *testing.T, id, k string) sdk.HTTPResponse {
	t.Helper()
	r := core.NewRegistry()
	for _, p := range core.Phases {
		if p == "polish" {
			r.Register(p, Register)
		} else {
			r.Register(p, func(*core.Registry) {})
		}
	}
	resp, err := r.ServeHTTP(sdk.HTTPRequest{
		Method: "GET", Pattern: "/state/{entity_id}",
		PathValues: map[string]string{"entity_id": id}, CommenterKey: k,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func checkHeaders(t *testing.T, resp sdk.HTTPResponse) {
	t.Helper()
	if resp.Status != 200 || resp.Headers["Content-Type"] != "application/json" || resp.Headers["Cache-Control"] != "no-store" {
		t.Errorf("response %d %v", resp.Status, resp.Headers)
	}
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return m
}

func TestStateNamedViewer(t *testing.T) {
	e := newEnv(t, 40)
	resp := serve(t, eid, key)
	checkHeaders(t, resp)
	want := map[string]any{
		"viewer": map[string]any{"karma": 40.0, "flag": true, "downvote": false, "vouch": true},
		"story":  map[string]any{"voted": true},
		"comments": map[string]any{
			cid1: map[string]any{"voted": "up", "own": false, "dead": false},
			cid2: map[string]any{"voted": "", "own": true, "dead": false},
			cid3: map[string]any{"voted": "", "own": false, "dead": true},
		},
	}
	if got := decode(t, resp.Body); !reflect.DeepEqual(got, want) {
		t.Errorf("body %s", resp.Body)
	}
	if strings.Contains(resp.Body, key) || strings.Contains(resp.Body, "commenter") {
		t.Errorf("body leaks the key: %s", resp.Body)
	}
	if len(e.stateArg) != 2 || string(e.stateArg[0]) != `"`+eid+`"` || string(e.stateArg[1]) != "7" {
		t.Errorf("args %s", e.stateArg)
	}
}

func TestStateSettingsThresholds(t *testing.T) {
	e := newEnv(t, 40)
	e.settings["downvote_karma"] = 10
	v := decode(t, serve(t, eid, key).Body)["viewer"].(map[string]any)
	if v["downvote"] != true {
		t.Errorf("viewer %v", v)
	}
}

func TestStateAnonymous(t *testing.T) {
	e := newEnv(t, 40)
	resp := serve(t, eid, "")
	checkHeaders(t, resp)
	if resp.Body != "{}" || len(e.host.Calls) != 0 {
		t.Errorf("body %q, calls %v", resp.Body, e.host.Calls)
	}
}

func TestStateUnknownKey(t *testing.T) {
	e := newEnv(t, 40)
	e.noUser = true
	resp := serve(t, eid, "nobody")
	checkHeaders(t, resp)
	if resp.Body != "{}" || e.stateSQL != "" {
		t.Errorf("body %q", resp.Body)
	}
}

func TestStateInvalidUUID(t *testing.T) {
	e := newEnv(t, 40)
	for _, id := range []string{"not-a-uuid", "../x", ""} {
		resp := serve(t, id, key)
		checkHeaders(t, resp)
		if resp.Body != "{}" {
			t.Errorf("%q: body %q", id, resp.Body)
		}
	}
	if len(e.host.Calls) != 0 {
		t.Errorf("host calls %v", e.host.Calls)
	}
}

func TestStateHostError(t *testing.T) {
	e := newEnv(t, 40)
	e.stateErr = errors.New("db down")
	resp := serve(t, eid, key)
	checkHeaders(t, resp)
	if resp.Body != "{}" {
		t.Errorf("body %q", resp.Body)
	}
}

func TestStateSQLLimit(t *testing.T) {
	e := newEnv(t, 40)
	serve(t, eid, key)
	for _, frag := range []string{"LIMIT 500", "NOT c.live", "$1::uuid", "$2::bigint"} {
		if !strings.Contains(e.stateSQL, frag) {
			t.Errorf("sql lacks %q", frag)
		}
	}
	if strings.Contains(e.stateSQL, eid) {
		t.Error("entity id inlined in the sql")
	}
}

func TestStateEmptyComments(t *testing.T) {
	e := newEnv(t, 40)
	e.host.Fns["db.query"] = func(raw json.RawMessage) (any, error) {
		if strings.Contains(string(raw), "u.commenter_key") {
			return coretest.Rows(map[string]any{"id": 7, "karma": 40}), nil
		}
		return coretest.Rows(map[string]any{"story": map[string]any{"voted": false}, "comments": nil}), nil
	}
	resp := serve(t, eid, key)
	m := decode(t, resp.Body)
	if c, ok := m["comments"].(map[string]any); !ok || len(c) != 0 {
		t.Errorf("body %s", resp.Body)
	}
}
