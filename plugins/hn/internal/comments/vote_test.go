package comments

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const votePattern = "/vote/comment/{id}"

type noticeErr struct{}

func (noticeErr) Error() string       { return "closed" }
func (noticeErr) Notice() core.Notice { return nSignin }

type plainErr struct{}

func (plainErr) Error() string { return "closed" }

// voteFake has voter k1 (user 1, karma 10) and author k2 (user 2) of comment cid1 (live).
func voteFake(t *testing.T) *fake {
	f := installFake(t)
	f.user("k1", "ann", 10)
	author := f.user("k2", "bob", 1)
	f.stories[eid1] = &storyRow{ItemNo: 1, Title: "S"}
	f.meta[cid1] = &metaRow{ID: cid1, Entity: eid1, Author: &author.ID, Live: true, Points: 1}
	return f
}

func vote(r *core.Registry, key, id string, form url.Values) sdk.HTTPResponse {
	return post(r, votePattern, key, id, form.Encode())
}

func expectLoc(t *testing.T, resp sdk.HTTPResponse, loc string) {
	t.Helper()
	if resp.Status != 303 || resp.Headers["Location"] != loc {
		t.Fatalf("got %d %q, want 303 %q (%s)", resp.Status, resp.Headers["Location"], loc, resp.Body)
	}
	if strings.Contains(resp.Headers["Location"]+resp.Body, "k1") || strings.Contains(resp.Headers["Location"]+resp.Body, "k2") {
		t.Fatalf("response leaks a commenter key: %+v", resp)
	}
}

func TestCommentVote(t *testing.T) {
	next := url.Values{"next": {"/item/7"}}
	with := func(how string) url.Values { return url.Values{"how": {how}, "next": {"/item/7"}} }

	t.Run("signed out", func(t *testing.T) {
		voteFake(t)
		expectLoc(t, vote(newRegistry(nil), "", cid1, with("up")), "/login?m=signin_first")
	})

	t.Run("key without user", func(t *testing.T) {
		voteFake(t)
		expectLoc(t, vote(newRegistry(nil), "k9", cid1, with("up")), "/login?m=comment-name")
	})

	t.Run("up counts once", func(t *testing.T) {
		f := voteFake(t)
		r := newRegistry(nil)
		expectLoc(t, vote(r, "k1", cid1, with("up")), "/item/7")
		m := f.meta[cid1]
		if f.votes[cid1][1] == nil || m.Points != 2 || !m.Dirty || f.users[2].Karma != 2 {
			t.Fatalf("votes %v points %d dirty %v karma %d", f.votes[cid1], m.Points, m.Dirty, f.users[2].Karma)
		}
		expectLoc(t, vote(r, "k1", cid1, with("up")), "/item/7?m=comment-voted")
		expectLoc(t, vote(r, "k1", cid1, with("down")), "/item/7?m=comment-karma")
		if m.Points != 2 || f.users[2].Karma != 2 {
			t.Fatalf("repeat changed state: points %d karma %d", m.Points, f.users[2].Karma)
		}
	})

	t.Run("missing how is up", func(t *testing.T) {
		f := voteFake(t)
		expectLoc(t, vote(newRegistry(nil), "k1", cid1, next), "/item/7")
		if f.votes[cid1][1] == nil || f.votes[cid1][1].Value != 1 {
			t.Fatalf("votes = %v", f.votes[cid1])
		}
	})

	t.Run("down needs karma", func(t *testing.T) {
		f := voteFake(t)
		r := newRegistry(nil)
		expectLoc(t, vote(r, "k1", cid1, with("down")), "/item/7?m=comment-karma")
		f.users[1].Karma = 500
		expectLoc(t, vote(r, "k1", cid1, with("down")), "/item/7")
		if f.meta[cid1].Points != 0 || f.users[2].Karma != 0 {
			t.Fatalf("points %d karma %d", f.meta[cid1].Points, f.users[2].Karma)
		}
	})

	t.Run("down threshold from settings", func(t *testing.T) {
		f := voteFake(t)
		f.settings["downvote_karma"] = 10
		expectLoc(t, vote(newRegistry(nil), "k1", cid1, with("down")), "/item/7")
		if f.users[2].Karma != 0 {
			t.Fatalf("karma = %d", f.users[2].Karma)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		f := voteFake(t)
		f.meta[cid2] = &metaRow{ID: cid2, Entity: eid1, Live: false, Points: 1}
		f.meta[cid3] = &metaRow{ID: cid3, Entity: eid1, Live: true, Points: 1} // staff comment: no author
		r := newRegistry(nil)
		for _, c := range map[string]struct{ key, id, how, code string }{
			"own":     {"k2", cid1, "up", "comment-own"},
			"dead":    {"k1", cid2, "up", "comment-dead"},
			"unknown": {"k1", "20000000-0000-4000-8000-000000000009", "up", "comment-missing"},
			"bad id":  {"k1", "nope", "up", "comment-missing"},
			"bad how": {"k1", cid1, "sideways", "comment-bad"},
			"no vote": {"k1", cid1, "un", "comment-novote"},
		} {
			expectLoc(t, vote(r, c.key, c.id, with(c.how)), "/item/7?m="+c.code)
		}
		if len(f.votes) != 0 || f.users[2].Karma != 1 {
			t.Fatalf("a refusal changed state: votes %v karma %d", f.votes, f.users[2].Karma)
		}
		expectLoc(t, vote(r, "k1", cid3, with("up")), "/item/7") // staff comment: counted, no karma call
		for _, e := range f.execs {
			if e.SQL == "UPDATE users SET karma = karma + $1 WHERE id = $2" {
				t.Fatalf("karma call for a staff comment: %v", e.Args)
			}
		}
	})

	t.Run("unvote window", func(t *testing.T) {
		f := voteFake(t)
		r := newRegistry(nil)
		now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		prev := core.Now
		t.Cleanup(func() { core.Now = prev })
		core.Now = func() time.Time { return now }

		expectLoc(t, vote(r, "k1", cid1, with("up")), "/item/7")
		core.Now = func() time.Time { return now.Add(59 * time.Minute) }
		expectLoc(t, vote(r, "k1", cid1, with("un")), "/item/7")
		if f.votes[cid1][1] != nil || f.meta[cid1].Points != 1 || f.users[2].Karma != 1 {
			t.Fatalf("votes %v points %d karma %d", f.votes[cid1], f.meta[cid1].Points, f.users[2].Karma)
		}

		core.Now = func() time.Time { return now }
		expectLoc(t, vote(r, "k1", cid1, with("up")), "/item/7")
		core.Now = func() time.Time { return now.Add(61 * time.Minute) }
		expectLoc(t, vote(r, "k1", cid1, with("un")), "/item/7?m=comment-late")
		if f.votes[cid1][1] == nil || f.users[2].Karma != 2 {
			t.Fatalf("late unvote changed state: votes %v karma %d", f.votes[cid1], f.users[2].Karma)
		}
	})

	t.Run("gate refusal", func(t *testing.T) {
		f := voteFake(t)
		expectLoc(t, vote(newRegistry(func(*core.GateReq) error { return noticeErr{} }), "k1", cid1, with("up")), "/item/7?m=signin_first")
		expectLoc(t, vote(newRegistry(func(*core.GateReq) error { return plainErr{} }), "k1", cid1, with("up")), "/item/7?m=comment-refused")
		if len(f.votes) != 0 {
			t.Fatalf("votes = %v", f.votes)
		}
		resp, _ := newRegistry(func(*core.GateReq) error { return plainErr{} }).ServeHTTP(sdk.HTTPRequest{
			Method: "POST", Pattern: votePattern, CommenterKey: "k1", PathValues: map[string]string{"id": cid1},
			Headers: map[string]string{"sec-fetch-site": "same-origin", "accept": "application/json"},
		})
		if resp.Status != 403 {
			t.Fatalf("json gate refusal = %d", resp.Status)
		}
	})

	t.Run("json success", func(t *testing.T) {
		voteFake(t)
		resp, _ := newRegistry(nil).ServeHTTP(sdk.HTTPRequest{
			Method: "POST", Pattern: votePattern, CommenterKey: "k1", PathValues: map[string]string{"id": cid1},
			Headers: map[string]string{"sec-fetch-site": "same-origin", "accept": "application/json"},
		})
		if resp.Status != 200 || resp.Body != `{"voted":true}` {
			t.Fatalf("got %d %s", resp.Status, resp.Body)
		}
	})

	t.Run("cross-site is refused by the dispatcher", func(t *testing.T) {
		f := voteFake(t)
		resp, _ := newRegistry(nil).ServeHTTP(sdk.HTTPRequest{
			Method: "POST", Pattern: votePattern, CommenterKey: "k1", PathValues: map[string]string{"id": cid1},
			Headers: map[string]string{"sec-fetch-site": "cross-site"},
		})
		if resp.Status != 403 || len(f.votes) != 0 {
			t.Fatalf("got %d, votes %v", resp.Status, f.votes)
		}
	})

	t.Run("unsafe next", func(t *testing.T) {
		voteFake(t)
		expectLoc(t, vote(newRegistry(nil), "k1", cid1, url.Values{"how": {"up"}, "next": {"//evil"}}), "/")
	})

	t.Run("db failure shows a notice", func(t *testing.T) {
		f := voteFake(t)
		f.failSQL[sqlVoteTarget] = plainErr{}
		expectLoc(t, vote(newRegistry(nil), "k1", cid1, with("up")), "/item/7?m=comment-error")
	})
}
