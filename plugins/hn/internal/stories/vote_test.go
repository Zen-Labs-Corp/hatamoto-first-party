package stories

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

var voteNow = mustTime("2026-01-02T12:00:00Z")

// voteDB has alice (id 1, key k1) and a story by author 2 with the given overrides.
func voteDB(story map[string]any) *fakeDB {
	row := map[string]any{"entity_id": entityID, "item_no": 42, "author_id": 2, "kind": "story", "dead": false, "points": 5, "comment_count": 0,
		"created_at": "2026-01-02T10:00:00Z", "title": "T", "url": "", "body": "", "voted_at": nil}
	for k, v := range story {
		row[k] = v
	}
	b, _ := json.Marshal(row)
	return &fakeDB{
		Users: map[string]map[string]any{"k1": user(1, "alice")},
		Query: func(sql string, _ []any) (any, error) {
			if strings.Contains(sql, "FROM story_meta s LEFT JOIN story_votes") {
				return map[string]any{"rows": [][]any{{string(b)}}}, nil
			}
			return map[string]any{"rows": [][]any{}}, nil
		},
	}
}

func vote(t *testing.T, r *core.Registry, id string, form url.Values, key string, wantJSON bool) sdk.HTTPResponse {
	t.Helper()
	return post(t, r, "POST /vote/story/{id}", map[string]string{"id": id}, form, key, wantJSON)
}

func TestStoryVoteUp(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(nil)
	install(t, db)
	r := newRegistry(t, nil)
	resp := vote(t, r, "42", url.Values{"next": {"/news?p=2"}}, "k1", false)
	if resp.Status != 303 || location(resp) != "/news?p=2" {
		t.Fatalf("resp %d %q", resp.Status, location(resp))
	}
	if len(db.Execs) != 2 {
		t.Fatalf("execs %v", execSQL(db))
	}
	if s := db.Execs[0].SQL; !strings.Contains(s, "INSERT INTO story_votes") || !strings.Contains(s, "UPDATE story_meta SET points = points + 1, score =") {
		t.Errorf("vote must be one statement: %s", s)
	}
	if k := db.Execs[1]; !strings.Contains(k.SQL, "UPDATE users SET karma") || k.Args[0] != float64(1) || k.Args[1] != float64(2) {
		t.Errorf("karma exec %+v", k)
	}
}

func TestStoryVoteUpJSON(t *testing.T) {
	pinNow(t, voteNow)
	install(t, voteDB(nil))
	r := newRegistry(t, nil)
	resp := vote(t, r, "42", nil, "k1", true)
	if resp.Status != 200 || resp.Body != `{"points":6,"voted":true}` {
		t.Errorf("resp %d %s", resp.Status, resp.Body)
	}
}

func TestStoryVoteRepeatIsNoop(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(map[string]any{"voted_at": "2026-01-02T11:00:00Z"})
	install(t, db)
	r := newRegistry(t, nil)
	resp := vote(t, r, "42", nil, "k1", true)
	if len(db.Execs) != 0 || resp.Body != `{"points":5,"voted":true}` {
		t.Errorf("execs %v body %s", execSQL(db), resp.Body)
	}
}

func TestStoryVoteLostRaceDoesNotMoveKarma(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(nil)
	db.Exec = func(string, []any) (int64, error) { return 0, nil }
	install(t, db)
	r := newRegistry(t, nil)
	vote(t, r, "42", nil, "k1", false)
	if len(db.Execs) != 1 {
		t.Errorf("execs %v", execSQL(db))
	}
}

func TestStoryVoteUnWithinHour(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(map[string]any{"voted_at": "2026-01-02T11:01:00Z"}) // 59 minutes ago
	install(t, db)
	r := newRegistry(t, nil)
	resp := vote(t, r, "42", url.Values{"how": {"un"}}, "k1", true)
	if resp.Body != `{"points":4,"voted":false}` {
		t.Errorf("body %s", resp.Body)
	}
	if len(db.Execs) != 2 || !strings.Contains(db.Execs[0].SQL, "DELETE FROM story_votes") || !strings.Contains(db.Execs[0].SQL, "points = points - 1") || db.Execs[1].Args[0] != float64(-1) {
		t.Errorf("execs %+v", db.Execs)
	}
}

func TestStoryVoteUnTooLate(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(map[string]any{"voted_at": "2026-01-02T10:59:00Z"}) // 61 minutes ago
	install(t, db)
	r := newRegistry(t, nil)
	resp := vote(t, r, "42", url.Values{"how": {"un"}, "next": {"/news"}}, "k1", false)
	if location(resp) != "/news?m=story-unvote-late" || len(db.Execs) != 0 {
		t.Errorf("location %q execs %v", location(resp), execSQL(db))
	}
}

func TestStoryVoteUnWithoutVoteIsNoop(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(nil)
	install(t, db)
	r := newRegistry(t, nil)
	resp := vote(t, r, "42", url.Values{"how": {"un"}}, "k1", true)
	if len(db.Execs) != 0 || resp.Body != `{"points":5,"voted":false}` {
		t.Errorf("execs %v body %s", execSQL(db), resp.Body)
	}
}

func TestStoryVoteRefusals(t *testing.T) {
	pinNow(t, voteNow)
	for name, c := range map[string]struct {
		story map[string]any
		want  string
	}{
		"own":  {map[string]any{"author_id": 1}, "story-vote-own"},
		"job":  {map[string]any{"kind": "job"}, "story-vote-job"},
		"dead": {map[string]any{"dead": true}, "story-vote-dead"},
	} {
		db := voteDB(c.story)
		install(t, db)
		r := newRegistry(t, nil)
		resp := vote(t, r, "42", url.Values{"next": {"/news"}}, "k1", false)
		if location(resp) != "/news?m="+c.want || len(db.Execs) != 0 {
			t.Errorf("%s: location %q execs %v", name, location(resp), execSQL(db))
		}
	}
}

func TestStoryVoteSignedOut(t *testing.T) {
	db := voteDB(nil)
	install(t, db)
	r := newRegistry(t, nil)
	resp := vote(t, r, "42", nil, "", false)
	if location(resp) != "/login?m=story-login" || len(db.Execs) != 0 {
		t.Errorf("location %q execs %v", location(resp), execSQL(db))
	}
	if resp := vote(t, r, "42", nil, "", true); resp.Status != 401 {
		t.Errorf("json status %d", resp.Status)
	}
}

type plainErr struct{}

func (plainErr) Error() string { return "no votes today" }

func TestStoryVoteGateRefusal(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(nil)
	install(t, db)
	r := newRegistry(t, map[string]func(*core.Registry){"moderation": func(r *core.Registry) {
		r.Gate(core.GateVote, func(*core.GateReq) error { return plainErr{} })
	}})
	resp := vote(t, r, "42", url.Values{"next": {"/news"}}, "k1", false)
	if location(resp) != "/news?m=story-refused" || len(db.Execs) != 0 {
		t.Errorf("location %q execs %v", location(resp), execSQL(db))
	}
	if resp := vote(t, r, "42", nil, "k1", true); resp.Status != 403 || !strings.Contains(resp.Body, "no votes today") {
		t.Errorf("json %d %s", resp.Status, resp.Body)
	}
}

func TestStoryVoteMissing(t *testing.T) {
	db := voteDB(nil)
	db.Query = func(string, []any) (any, error) { return map[string]any{"rows": [][]any{}}, nil }
	install(t, db)
	r := newRegistry(t, nil)
	for _, id := range []string{"999", "abc"} {
		resp := vote(t, r, id, url.Values{"next": {"/news"}}, "k1", false)
		if location(resp) != "/news?m=story-missing" {
			t.Errorf("id %q: location %q", id, location(resp))
		}
	}
}

func TestStoryVoteNextIsSafe(t *testing.T) {
	pinNow(t, voteNow)
	install(t, voteDB(nil))
	r := newRegistry(t, nil)
	if loc := location(vote(t, r, "42", url.Values{"next": {"//evil.example"}}, "k1", false)); loc != "/" {
		t.Errorf("location %q", loc)
	}
}

func TestStoryVoteRingIsStoredUncounted(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(nil)
	install(t, db)
	r := newRegistry(t, map[string]func(*core.Registry){"moderation": func(r *core.Registry) {
		r.Gate(core.GateVote, func(g *core.GateReq) error { g.Ring = true; return nil })
	}})
	resp := vote(t, r, "42", nil, "k1", true)
	if resp.Body != `{"points":5,"voted":true}` {
		t.Errorf("body %s", resp.Body)
	}
	if len(db.Execs) != 1 || !strings.Contains(db.Execs[0].SQL, "INSERT INTO story_votes") || !strings.Contains(db.Execs[0].SQL, "false)") ||
		strings.Contains(db.Execs[0].SQL, "UPDATE") {
		t.Errorf("execs %v (want one uncounted insert, no points or karma)", execSQL(db))
	}
}

func TestStoryVoteUnOfUncountedVote(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(map[string]any{"voted_at": "2026-01-02T11:30:00Z", "vote_uncounted": true})
	install(t, db)
	r := newRegistry(t, nil)
	resp := vote(t, r, "42", url.Values{"how": {"un"}}, "k1", true)
	if resp.Body != `{"points":5,"voted":false}` {
		t.Errorf("body %s", resp.Body)
	}
	if len(db.Execs) != 1 || !strings.HasPrefix(db.Execs[0].SQL, "DELETE FROM story_votes") {
		t.Errorf("execs %v (want one plain delete, no points or karma)", execSQL(db))
	}
}

func TestStoryVoteCountedUnvoteOnlyDeletesCountedRows(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(map[string]any{"voted_at": "2026-01-02T11:30:00Z"})
	install(t, db)
	vote(t, newRegistry(t, nil), "42", url.Values{"how": {"un"}}, "k1", true)
	if len(db.Execs) == 0 || !strings.Contains(db.Execs[0].SQL, "user_id = $2 AND counted AND") {
		t.Errorf("execs %v", execSQL(db))
	}
}

// Three voters behind one network: the gate marks the third, so points go 1 -> 3, not 4.
func TestStoryVoteRingRepro(t *testing.T) {
	pinNow(t, voteNow)
	points := 1
	calls := 0
	db := voteDB(map[string]any{"points": 1})
	db.Exec = func(sql string, _ []any) (int64, error) {
		if strings.Contains(sql, "points = points + 1") {
			points++
		}
		return 1, nil
	}
	install(t, db)
	r := newRegistry(t, map[string]func(*core.Registry){"moderation": func(r *core.Registry) {
		r.Gate(core.GateVote, func(g *core.GateReq) error { calls++; g.Ring = calls >= 3; return nil })
	}})
	for i := 0; i < 3; i++ {
		vote(t, r, "42", nil, "k1", true)
	}
	if points != 3 {
		t.Errorf("points = %d, want 3", points)
	}
}

// The ring budget is used only by a vote that is really stored: a repeat "up" (double click,
// stale page) calls Stored no more, and a lost race (no row inserted) does not either.
func TestStoryVoteReportsOnlyStoredVotes(t *testing.T) {
	pinNow(t, voteNow)
	run := func(row map[string]any, rows int64, ring bool) (stored []bool) {
		db := voteDB(row)
		db.Exec = func(string, []any) (int64, error) { return rows, nil }
		install(t, db)
		r := newRegistry(t, map[string]func(*core.Registry){"moderation": func(r *core.Registry) {
			r.Gate(core.GateVote, func(g *core.GateReq) error {
				g.Ring = ring
				g.Stored = func(counted bool) { stored = append(stored, counted) }
				return nil
			})
		}})
		vote(t, r, "42", nil, "k1", true)
		return stored
	}
	if got := run(nil, 1, false); len(got) != 1 || !got[0] {
		t.Errorf("new counted vote: %v", got)
	}
	if got := run(nil, 1, true); len(got) != 1 || got[0] {
		t.Errorf("new uncounted vote: %v", got)
	}
	if got := run(map[string]any{"voted_at": "2026-01-02T11:30:00Z"}, 1, false); len(got) != 0 {
		t.Errorf("repeat vote: %v", got)
	}
	if got := run(nil, 0, false); len(got) != 0 {
		t.Errorf("lost race (counted): %v", got)
	}
	if got := run(nil, 0, true); len(got) != 0 {
		t.Errorf("lost race (uncounted): %v", got)
	}
}

func TestStoryVoteKeepsRowFragment(t *testing.T) {
	pinNow(t, voteNow)
	db := voteDB(nil)
	install(t, db)
	r := newRegistry(t, nil)
	if resp := vote(t, r, "42", url.Values{"next": {"/news?p=2#42"}}, "k1", false); resp.Status != 303 || location(resp) != "/news?p=2#42" {
		t.Errorf("vote %d %q", resp.Status, location(resp))
	}
	db = voteDB(map[string]any{"voted_at": "2026-01-02T11:30:00Z"})
	install(t, db)
	if resp := vote(t, r, "42", url.Values{"how": {"un"}, "next": {"/news#42"}}, "k1", false); location(resp) != "/news#42" {
		t.Errorf("unvote %q", location(resp))
	}
	db = voteDB(map[string]any{"voted_at": "2026-01-02T10:59:00Z"})
	install(t, db)
	if resp := vote(t, r, "42", url.Values{"how": {"un"}, "next": {"/news#42"}}, "k1", false); location(resp) != "/news?m=story-unvote-late#42" {
		t.Errorf("refusal %q", location(resp))
	}
}
