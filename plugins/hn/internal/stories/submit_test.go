package stories

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
)

// submitDB has alice (id 1, key k1); the item_numbers insert answers 57, the
// duplicate lookup dup (0 = none), the vote lookup a story by author 2.
func submitDB(dup int, storyRow map[string]any) (*fakeDB, *[]json.RawMessage) {
	var created []json.RawMessage
	row := map[string]any{"entity_id": entityID, "item_no": dup, "author_id": 2, "kind": "story", "dead": false, "points": 5, "comment_count": 0,
		"created_at": "2026-01-02T10:00:00Z", "title": "T", "url": "", "body": "", "voted_at": nil}
	for k, v := range storyRow {
		row[k] = v
	}
	b, _ := json.Marshal(row)
	db := &fakeDB{
		Users: map[string]map[string]any{"k1": user(1, "alice")},
		Query: func(sql string, _ []any) (any, error) {
			switch {
			case strings.Contains(sql, "INSERT INTO item_numbers"):
				return coretest.Rows(57), nil
			case strings.Contains(sql, "count(*)"):
				return coretest.Rows(0), nil
			case strings.Contains(sql, "WHERE url_key"):
				if dup > 0 {
					return coretest.Rows(dup), nil
				}
			case strings.Contains(sql, "FROM story_meta s LEFT JOIN story_votes"):
				return map[string]any{"rows": [][]any{{string(b)}}}, nil
			}
			return map[string]any{"rows": [][]any{}}, nil
		},
		Entities: map[string]func(json.RawMessage) (any, error){
			"entities.create": func(a json.RawMessage) (any, error) {
				created = append(created, a)
				return map[string]any{"id": entityID}, nil
			},
			"entities.delete": func(json.RawMessage) (any, error) { return map[string]any{}, nil },
		},
	}
	return db, &created
}

func submit(t *testing.T, r *core.Registry, form url.Values, key string, wantJSON bool) (resp struct {
	Status int
	Loc    string
	Body   string
}) {
	t.Helper()
	got := post(t, r, "POST /submit", nil, form, key, wantJSON)
	resp.Status, resp.Loc, resp.Body = got.Status, location(got), got.Body
	return resp
}

func queryCount(db *fakeDB, sub string) int {
	n := 0
	for _, c := range db.Queries {
		if strings.Contains(c.SQL, sub) {
			n++
		}
	}
	return n
}

func TestSubmitTitle(t *testing.T) {
	pinNow(t, voteNow)
	for _, title := range []string{"", strings.Repeat("a", 81)} {
		db, created := submitDB(0, nil)
		install(t, db)
		resp := submit(t, newRegistry(t, nil), url.Values{"title": {title}, "url": {"https://example.com/a"}}, "k1", false)
		if resp.Loc != "/submit?m=story-title" || len(*created) != 0 {
			t.Errorf("title %d runes: %q created %d", len(title), resp.Loc, len(*created))
		}
	}
	db, created := submitDB(0, nil)
	install(t, db)
	resp := submit(t, newRegistry(t, nil), url.Values{"title": {strings.Repeat("a", 80)}, "url": {"https://example.com/a"}}, "k1", false)
	if resp.Status != 303 || resp.Loc != "/newest" || len(*created) != 1 {
		t.Errorf("80 runes: %d %q created %d", resp.Status, resp.Loc, len(*created))
	}
}

func TestSubmitBadURL(t *testing.T) {
	pinNow(t, voteNow)
	for _, raw := range []string{"javascript:alert(1)", "ftp://x.example/", "https://user:pw@x.example/", "https://x.example/" + strings.Repeat("a", 2000)} {
		db, created := submitDB(0, nil)
		install(t, db)
		resp := submit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "url": {raw}}, "k1", false)
		if resp.Loc != "/submit?m=story-url" || len(*created) != 0 || queryCount(db, "WHERE url_key") != 0 {
			t.Errorf("%.30q: %q created %d", raw, resp.Loc, len(*created))
		}
	}
}

func TestSubmitUrlOrText(t *testing.T) {
	pinNow(t, voteNow)
	db, created := submitDB(0, nil)
	install(t, db)
	r := newRegistry(t, nil)
	if resp := submit(t, r, url.Values{"title": {"T"}}, "k1", false); resp.Loc != "/submit?m=story-empty" {
		t.Errorf("empty: %q", resp.Loc)
	}
	if resp := submit(t, r, url.Values{"title": {"T"}, "text": {"hello"}}, "k1", false); resp.Loc != "/newest" {
		t.Errorf("text only: %q", resp.Loc)
	}
	if resp := submit(t, r, url.Values{"title": {"T"}, "text": {strings.Repeat("a", maxText+1)}}, "k1", false); resp.Loc != "/submit?m=story-text-long" {
		t.Errorf("long text: %q", resp.Loc)
	}
	if resp := submit(t, r, url.Values{"title": {"T"}, "url": {"https://example.com/b"}, "text": {"both"}}, "k1", false); resp.Loc != "/newest" {
		t.Errorf("both: %q", resp.Loc)
	}
	if len(*created) != 2 || !strings.Contains(string((*created)[1]), `"text":"both"`) || !strings.Contains(string((*created)[1]), `"url":"https://example.com/b"`) {
		t.Errorf("created %s", *created)
	}
}

func TestSubmitKind(t *testing.T) {
	pinNow(t, voteNow)
	for title, kind := range map[string]string{"Ask: how?": "ask", "Show: thing": "show", "Plain": "story"} {
		db, created := submitDB(0, nil)
		install(t, db)
		submit(t, newRegistry(t, nil), url.Values{"title": {title}, "text": {"x"}}, "k1", false)
		if len(*created) != 1 || !strings.Contains(string((*created)[0]), `"kind":"`+kind+`"`) {
			t.Errorf("%q: %s", title, *created)
		}
	}
}

func TestSubmitDuplicate(t *testing.T) {
	pinNow(t, voteNow)
	form := url.Values{"title": {"T"}, "url": {"https://example.com/a"}}
	db, created := submitDB(12, map[string]any{"item_no": 12})
	install(t, db)
	resp := submit(t, newRegistry(t, nil), form, "k1", false)
	if resp.Loc != "/item/12?m=story-dup" || len(*created) != 0 {
		t.Fatalf("dup: %q created %d", resp.Loc, len(*created))
	}
	if len(db.Execs) == 0 || !strings.Contains(db.Execs[0].SQL, "INSERT INTO story_votes") {
		t.Errorf("no vote on the existing story: %v", execSQL(db))
	}
	db, _ = submitDB(12, map[string]any{"item_no": 12, "author_id": 1})
	install(t, db)
	resp = submit(t, newRegistry(t, nil), form, "k1", false)
	if resp.Loc != "/item/12?m=story-dup" || len(db.Execs) != 0 {
		t.Errorf("own story: %q execs %v", resp.Loc, execSQL(db))
	}
}

type rateErr struct{}

func (rateErr) Error() string       { return "slow down" }
func (rateErr) Notice() core.Notice { return nRate }

func TestSubmitGate(t *testing.T) {
	pinNow(t, voteNow)
	form := url.Values{"title": {"T"}, "url": {"https://example.com/a"}}
	for _, tc := range []struct {
		err  error
		want string
	}{{errors.New("nope"), "/submit?m=story-refused"}, {rateErr{}, "/submit?m=story-rate"}} {
		db, created := submitDB(0, nil)
		install(t, db)
		r := newRegistry(t, map[string]func(*core.Registry){"moderation": func(r *core.Registry) {
			r.Gate(core.GateSubmit, func(*core.GateReq) error { return tc.err })
		}})
		resp := submit(t, r, form, "k1", false)
		if resp.Loc != tc.want || len(*created) != 0 {
			t.Errorf("%v: %q", tc.err, resp.Loc)
		}
	}
	db, _ := submitDB(0, nil)
	install(t, db)
	r := newRegistry(t, map[string]func(*core.Registry){"moderation": func(r *core.Registry) {
		r.Gate(core.GateSubmit, func(*core.GateReq) error { return errors.New("blocked domain") })
	}})
	resp := submit(t, r, form, "k1", true)
	if resp.Status != 403 || !strings.Contains(resp.Body, "blocked domain") {
		t.Errorf("json: %d %s", resp.Status, resp.Body)
	}
}

func TestSubmitSignedOut(t *testing.T) {
	pinNow(t, voteNow)
	db, created := submitDB(0, nil)
	install(t, db)
	resp := submit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "text": {"x"}}, "", false)
	if resp.Loc != "/login?m=story-login" || len(db.Queries)+len(db.Execs)+len(*created) != 0 {
		t.Errorf("%q queries %d", resp.Loc, len(db.Queries))
	}
}

func TestSubmitRateLimit(t *testing.T) {
	pinNow(t, voteNow)
	form := url.Values{"title": {"T"}, "text": {"x"}}
	db, created := submitDB(0, nil)
	db.Settings = map[string]any{"submit_per_hour": 2}
	query := db.Query
	db.Query = func(sql string, args []any) (any, error) {
		if strings.Contains(sql, "count(*)") {
			return coretest.Rows(2), nil
		}
		return query(sql, args)
	}
	install(t, db)
	resp := submit(t, newRegistry(t, nil), form, "k1", false)
	if resp.Loc != "/submit?m=story-rate" || len(*created) != 0 {
		t.Errorf("limit: %q", resp.Loc)
	}
	db, _ = submitDB(0, nil)
	db.Settings = map[string]any{"submit_per_hour": 0}
	install(t, db)
	resp = submit(t, newRegistry(t, nil), form, "k1", false)
	if resp.Loc != "/newest" || queryCount(db, "count(*)") != 0 {
		t.Errorf("no limit: %q counts %d", resp.Loc, queryCount(db, "count(*)"))
	}
}

func TestSubmitOff(t *testing.T) {
	pinNow(t, voteNow)
	db, _ := submitDB(0, nil)
	db.Entities["entities.create"] = func(json.RawMessage) (any, error) { return nil, hostErr("unavailable") }
	install(t, db)
	resp := submit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "text": {"x"}}, "k1", false)
	if resp.Loc != "/submit?m=story-off" {
		t.Errorf("%q", resp.Loc)
	}
}

func TestSubmitStoresSite(t *testing.T) {
	pinNow(t, voteNow)
	db, _ := submitDB(0, nil)
	install(t, db)
	resp := submit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "url": {"https://blog.github.com/x"}}, "k1", false)
	if resp.Status != 303 || resp.Loc != "/newest" {
		t.Fatalf("%d %q", resp.Status, resp.Loc)
	}
	for _, c := range db.Execs {
		if strings.Contains(c.SQL, "INSERT INTO story_meta") {
			if c.Args[5] != "blog.github.com/x" || c.Args[6] != "github.com" {
				t.Errorf("url_key %v domain %v", c.Args[5], c.Args[6])
			}
			return
		}
	}
	t.Errorf("no story_meta insert: %v", execSQL(db))
}

// The hourly limit counts story_actions, which a delete never shrinks, and
// every submit that reaches the host is recorded before it.
func TestSubmitRateCountsAppendOnlyLog(t *testing.T) {
	pinNow(t, voteNow)
	db, _ := submitDB(0, nil)
	install(t, db)
	resp := submit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "text": {"x"}}, "k1", false)
	if resp.Loc != "/newest" {
		t.Fatalf("%q", resp.Loc)
	}
	var counted bool
	for _, c := range db.Queries {
		if strings.Contains(c.SQL, "count(*)") {
			counted = true
			if !strings.Contains(c.SQL, "FROM story_actions") || strings.Contains(c.SQL, "story_meta") {
				t.Errorf("limit must count story_actions: %s", c.SQL)
			}
		}
	}
	if !counted {
		t.Error("no limit query")
	}
	var logged, deleted int
	for _, sql := range execSQL(db) {
		if strings.Contains(sql, "INSERT INTO story_actions") {
			logged++
		}
		if strings.Contains(sql, "DELETE FROM story_actions") {
			deleted++
		}
	}
	if logged != 1 || deleted != 0 {
		t.Errorf("log rows %d, deletes %d: %v", logged, deleted, execSQL(db))
	}
}

func TestSubmitRefusesControlCharacters(t *testing.T) {
	pinNow(t, voteNow)
	for name, tc := range map[string]struct {
		form url.Values
		want string
	}{
		"title": {url.Values{"title": {"a\x00b"}, "text": {"x"}}, "/submit?m=story-title"},
		"url":   {url.Values{"title": {"T"}, "url": {"https://example.com/a\x00b"}}, "/submit?m=story-url"},
		"text":  {url.Values{"title": {"T"}, "text": {"a\x00b"}}, "/submit?m=story-text-chars"},
		"bell":  {url.Values{"title": {"T"}, "text": {"a\x07b"}}, "/submit?m=story-text-chars"},
	} {
		db, created := submitDB(0, nil)
		install(t, db)
		resp := submit(t, newRegistry(t, nil), tc.form, "k1", false)
		if resp.Loc != tc.want || len(*created) != 0 || queryCount(db, "INSERT INTO item_numbers") != 0 || len(db.Execs) != 0 {
			t.Errorf("%s: %q created %d", name, resp.Loc, len(*created))
		}
	}
	db, _ := submitDB(0, nil)
	install(t, db)
	if resp := submit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "text": {"line one\r\n\tline two"}}, "k1", false); resp.Loc != "/newest" {
		t.Errorf("layout characters: %q", resp.Loc)
	}
}
