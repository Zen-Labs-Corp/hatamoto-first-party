package comments

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
	"hatamoto.dev/sdk"
)

// Test ids.
const (
	cid1 = "00000000-0000-4000-8000-000000000001"
	cid2 = "00000000-0000-4000-8000-000000000002"
	cid3 = "00000000-0000-4000-8000-000000000003"
	eid1 = "10000000-0000-4000-8000-000000000001"
	eid2 = "10000000-0000-4000-8000-000000000002"
)

type metaRow struct {
	ID, Entity, Parent string
	Author             *int64
	Name, Body         string
	Live               bool
	Points             int
	Dirty              bool
	Created            time.Time
}

type voteRow struct {
	Value   int
	Created time.Time
}

type storyRow struct {
	ItemNo int64
	Title  string
	Count  int
	Dead   bool
}

// fake is an in-memory model of the tables this phase touches, behind the
// fake host. Statements are matched by their exact text; an unknown one fails the test.
type fake struct {
	t     *testing.T
	host  *coretest.Host
	meta  map[string]*metaRow
	votes map[string]map[int64]*voteRow // comment -> user -> vote

	users    map[int64]*core.User
	stories  map[string]*storyRow
	settings map[string]any

	// failSQL makes one statement fail with the given error.
	failSQL map[string]error
	// execs records every exec statement with its raw arguments.
	execs []execRec

	// setRank records every comments.set_rank batch; setRankHook runs on each
	// call (0-based index) and may fail it or touch the model.
	setRank     [][]sdk.CommentRankItem
	setRankHook func(call int) error

	// threads is what comments.thread lists per entity, threadPage items per
	// page; threadMore forces has_more on every page; threadHook runs on each
	// call (0-based) and may fail it.
	threads    map[string][]sdk.CommentItem
	threadPage int
	threadMore bool
	threadHook func(call int) error
	threadCall []string // "entity#page" of every call

	// byAuthor is what comments.by_author lists per commenter key (newest first,
	// 50 per page); subtrees is what comments.subtree answers per comment.
	// byAuthorErr and subtreeErr fail every call; subtreeErrFor fails one comment.
	byAuthor      map[string][]sdk.CommentItem
	byAuthorErr   error
	byAuthorCalls []string // "key#page"
	subtrees      map[string]sdk.CommentSubtree
	subtreeErr    error
	subtreeErrFor map[string]error
	subtreeCalls  []string

	nextUser int64
}

type execRec struct {
	SQL  string
	Args []json.RawMessage
}

func (f *fake) user(key, name string, karma int) *core.User {
	f.nextUser++
	u := &core.User{ID: f.nextUser, CommenterKey: key, Username: name, Karma: karma, CreatedAt: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)}
	f.users[u.ID] = u
	return u
}

func (f *fake) userBy(match func(*core.User) bool) *core.User {
	for _, u := range f.users {
		if match(u) {
			return u
		}
	}
	return nil
}

func decodeArgs(raw json.RawMessage) (string, []json.RawMessage) {
	var a struct {
		SQL  string            `json:"sql"`
		Args []json.RawMessage `json:"args"`
	}
	_ = json.Unmarshal(raw, &a)
	return a.SQL, a.Args
}

func argStr(a []json.RawMessage, i int) string {
	var s string
	if i < len(a) {
		_ = json.Unmarshal(a[i], &s)
	}
	return s
}

func argInt(a []json.RawMessage, i int) int64 {
	var n int64
	if i < len(a) {
		_ = json.Unmarshal(a[i], &n)
	}
	return n
}

func argBool(a []json.RawMessage, i int) bool {
	var b bool
	if i < len(a) {
		_ = json.Unmarshal(a[i], &b)
	}
	return b
}

func argTime(a []json.RawMessage, i int) time.Time {
	var tm time.Time
	if i < len(a) {
		_ = json.Unmarshal(a[i], &tm)
	}
	return tm
}

// argStrings decodes an argument that is a JSON list sent as one text value.
func argStrings(a []json.RawMessage, i int) []string {
	var out []string
	if i >= len(a) {
		return nil
	}
	if json.Unmarshal(a[i], &out) == nil {
		return out
	}
	var s string
	if json.Unmarshal(a[i], &s) == nil {
		_ = json.Unmarshal([]byte(s), &out)
	}
	return out
}

func rowsAffected(n int64) any { return map[string]any{"rows_affected": n} }

var noRows = map[string]any{"rows": [][]any{}}

// installFake installs the fake host for the test.
func installFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		t:        t,
		meta:     map[string]*metaRow{},
		votes:    map[string]map[int64]*voteRow{},
		users:    map[int64]*core.User{},
		stories:  map[string]*storyRow{},
		settings: map[string]any{},
		failSQL:  map[string]error{},

		threads:    map[string][]sdk.CommentItem{},
		threadPage: 2,

		byAuthor:      map[string][]sdk.CommentItem{},
		subtrees:      map[string]sdk.CommentSubtree{},
		subtreeErrFor: map[string]error{},
	}
	f.host = &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"db.query": func(raw json.RawMessage) (any, error) {
			sql, a := decodeArgs(raw)
			if err := f.failSQL[sql]; err != nil {
				return nil, err
			}
			return f.query(sql, a), nil
		},
		"db.exec": func(raw json.RawMessage) (any, error) {
			sql, a := decodeArgs(raw)
			f.execs = append(f.execs, execRec{sql, a})
			if err := f.failSQL[sql]; err != nil {
				return nil, err
			}
			return rowsAffected(f.exec(sql, a)), nil
		},
		sdk.FnCommentsSetRank: func(raw json.RawMessage) (any, error) {
			var a sdk.CommentRankArgs
			_ = json.Unmarshal(raw, &a)
			call := len(f.setRank)
			f.setRank = append(f.setRank, a.Items)
			if f.setRankHook != nil {
				if err := f.setRankHook(call); err != nil {
					return nil, err
				}
			}
			return sdk.CommentsChanged{Changed: len(a.Items)}, nil
		},
		sdk.FnCommentsThread: func(raw json.RawMessage) (any, error) {
			var a sdk.CommentsThreadArgs
			_ = json.Unmarshal(raw, &a)
			call := len(f.threadCall)
			f.threadCall = append(f.threadCall, fmt.Sprintf("%s#%d", a.EntityID, a.Page))
			if !a.IncludeDead {
				f.t.Errorf("fake: comments.thread without include_dead")
			}
			if f.threadHook != nil {
				if err := f.threadHook(call); err != nil {
					return nil, err
				}
			}
			all := f.threads[a.EntityID]
			lo, hi := min((a.Page-1)*f.threadPage, len(all)), min(a.Page*f.threadPage, len(all))
			return sdk.CommentThread{Items: all[lo:hi], Page: a.Page, HasMore: hi < len(all) || f.threadMore}, nil
		},
		sdk.FnCommentsByAuthor: func(raw json.RawMessage) (any, error) {
			var a sdk.CommentsByAuthorArgs
			_ = json.Unmarshal(raw, &a)
			f.byAuthorCalls = append(f.byAuthorCalls, fmt.Sprintf("%s#%d", a.CommenterKey, a.Page))
			if f.byAuthorErr != nil {
				return nil, f.byAuthorErr
			}
			all := f.byAuthor[a.CommenterKey]
			lo, hi := min((a.Page-1)*50, len(all)), min(a.Page*50, len(all))
			return sdk.CommentThread{Items: all[lo:hi], Page: a.Page, HasMore: hi < len(all)}, nil
		},
		sdk.FnCommentsSubtree: func(raw json.RawMessage) (any, error) {
			var a sdk.CommentSubtreeArgs
			_ = json.Unmarshal(raw, &a)
			f.subtreeCalls = append(f.subtreeCalls, a.CommentID)
			if f.subtreeErr != nil {
				return nil, f.subtreeErr
			}
			if err := f.subtreeErrFor[a.CommentID]; err != nil {
				return nil, err
			}
			return f.subtrees[a.CommentID], nil
		},
		"settings.get": func(json.RawMessage) (any, error) { return f.settings, nil },
	}}
	coretest.Install(t, f.host)
	return f
}

func (f *fake) query(sql string, a []json.RawMessage) any {
	var u *core.User
	switch {
	case sql == sqlSample:
		return coretest.Rows(f.sample(argStr(a, 0)))
	case sql == sqlGone:
		return coretest.Rows(f.gone(argStr(a, 0), argStrings(a, 1), argTime(a, 2)))
	case sql == sqlEntityIDs:
		ids := []string{}
		for id, m := range f.meta {
			if m.Entity == argStr(a, 0) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		return coretest.Rows(ids)
	case sql == sqlDirty:
		return coretest.Rows(f.dirtyRows())
	case sql == sqlNewest:
		return coretest.Rows(f.newest(int(argInt(a, 0))))
	case sql == sqlCommentStories:
		refs := map[string]any{}
		for _, id := range argStrings(a, 0) {
			if m := f.meta[id]; m != nil && f.stories[m.Entity] != nil {
				s := f.stories[m.Entity]
				refs[id] = map[string]any{"item_no": s.ItemNo, "title": s.Title, "author_id": m.Author}
			}
		}
		return coretest.Rows(refs)
	case sql == sqlMyVotes:
		mine := map[string]int{}
		for _, id := range argStrings(a, 1) {
			if v := f.votes[id][argInt(a, 0)]; v != nil {
				mine[id] = v.Value
			}
		}
		return coretest.Rows(mine)
	case sql == sqlVoteTarget:
		m := f.meta[argStr(a, 0)]
		if m == nil {
			return noRows
		}
		return coretest.Rows(map[string]any{"author_id": m.Author, "live": m.Live, "points": m.Points})
	case sql == sqlVoteMine:
		v := f.votes[argStr(a, 0)][argInt(a, 1)]
		if v == nil {
			return noRows
		}
		return coretest.Rows(map[string]any{"value": v.Value, "created_at": v.Created})
	case sql == sqlRemovalInfo:
		return coretest.Rows(f.removalInfo(argStrings(a, 0)))
	case strings.Contains(sql, "u.commenter_key = $1"):
		key := argStr(a, 0)
		u = f.userBy(func(x *core.User) bool { return x.CommenterKey == key })
	case strings.Contains(sql, "u.username_lower = lower($1)"):
		want := strings.ToLower(argStr(a, 0))
		u = f.userBy(func(x *core.User) bool { return strings.ToLower(x.Username) == want })
	case strings.Contains(sql, "u.id = $1"):
		id := argInt(a, 0)
		u = f.userBy(func(x *core.User) bool { return x.ID == id })
	default:
		f.t.Errorf("fake: unexpected query %q", sql)
		return noRows
	}
	if u == nil {
		return noRows
	}
	return coretest.Rows(u)
}

func (f *fake) exec(sql string, a []json.RawMessage) int64 {
	switch sql {
	case sqlUpsertCreated:
		return f.upsert(a, false)
	case sqlUpsertState:
		return f.upsert(a, true)
	case sqlSync:
		var recs []struct {
			ID         string `json:"id"`
			BodyHTML   string `json:"body_html"`
			AuthorName string `json:"author_name"`
			Live       bool   `json:"live"`
		}
		var text string
		_ = json.Unmarshal(a[0], &text)
		_ = json.Unmarshal([]byte(text), &recs)
		var n int64
		for _, r := range recs {
			m := f.meta[r.ID]
			if m != nil && (m.Body != r.BodyHTML || m.Name != r.AuthorName || m.Live != r.Live) {
				m.Body, m.Name, m.Live = r.BodyHTML, r.AuthorName, r.Live
				n++
			}
		}
		return n
	case sqlRepoint:
		var n int64
		for id, m := range f.meta {
			if m.Entity != argStr(a, 0) {
				continue
			}
			want := 1
			for _, v := range f.votes[id] {
				want += v.Value
			}
			if m.Points != want {
				m.Points, m.Dirty = want, true
				n++
			}
		}
		return n
	case sqlClean:
		var sent []struct {
			ID     string `json:"id"`
			Points int    `json:"points"`
		}
		var text string
		_ = json.Unmarshal(a[0], &text)
		_ = json.Unmarshal([]byte(text), &sent)
		var n int64
		for _, s := range sent {
			if m := f.meta[s.ID]; m != nil && m.Points == s.Points && m.Dirty {
				m.Dirty = false
				n++
			}
		}
		return n
	case sqlVoteInsert:
		id, uid := argStr(a, 0), argInt(a, 1)
		if f.meta[id] == nil || f.votes[id][uid] != nil {
			return 0
		}
		if f.votes[id] == nil {
			f.votes[id] = map[int64]*voteRow{}
		}
		f.votes[id][uid] = &voteRow{Value: int(argInt(a, 2)), Created: core.Now()}
		return 1
	case sqlVoteDelete:
		id, uid := argStr(a, 0), argInt(a, 1)
		if v := f.votes[id][uid]; v != nil && v.Value == int(argInt(a, 2)) {
			delete(f.votes[id], uid)
			return 1
		}
		return 0
	case sqlPoints:
		m := f.meta[argStr(a, 0)]
		if m == nil {
			return 0
		}
		m.Points = 1
		for _, v := range f.votes[m.ID] {
			m.Points += v.Value
		}
		m.Dirty = true
		return 1
	case sqlRecount:
		return f.recount(argStr(a, 0))
	case sqlDeleteMeta:
		return f.deleteMeta(argStrings(a, 0))
	case "UPDATE users SET karma = karma + $1 WHERE id = $2":
		if u := f.users[argInt(a, 1)]; u != nil {
			u.Karma += int(argInt(a, 0))
			return 1
		}
		return 0
	default:
		f.t.Errorf("fake: unexpected exec %q", sql)
		return 0
	}
}

// newRegistry registers the phases in order: this phase for real, the others
// empty, plus an optional vote gate from "moderation".
func newRegistry(gate func(*core.GateReq) error) *core.Registry {
	r := core.NewRegistry()
	r.Register("accounts", func(*core.Registry) {})
	r.Register("stories", func(*core.Registry) {})
	r.Register("comments", Register)
	r.Register("moderation", func(r *core.Registry) {
		if gate != nil {
			r.Gate(core.GateVote, gate)
		}
	})
	return r
}

// post sends a same-origin form post to a route pattern with one {id} wildcard.
func post(r *core.Registry, pattern, key, id, body string) sdk.HTTPResponse {
	resp, _ := r.ServeHTTP(sdk.HTTPRequest{
		Method: "POST", Pattern: pattern, Body: body, CommenterKey: key,
		PathValues: map[string]string{"id": id},
		Headers:    map[string]string{"sec-fetch-site": "same-origin"},
	})
	return resp
}

// upsert models sqlUpsertCreated (state false) and sqlUpsertState (state true).
func (f *fake) upsert(a []json.RawMessage, state bool) int64 {
	var author *int64
	if key := argStr(a, 3); key != "" {
		if u := f.userBy(func(x *core.User) bool { return x.CommenterKey == key }); u != nil {
			id := u.ID
			author = &id
		}
	}
	id, name, body, live := argStr(a, 0), argStr(a, 4), argStr(a, 5), argBool(a, 6)
	m := f.meta[id]
	if m == nil {
		f.meta[id] = &metaRow{ID: id, Entity: argStr(a, 1), Parent: argStr(a, 2), Author: author, Name: name, Body: body,
			Live: live, Points: 1, Dirty: true, Created: argTime(a, 7)}
		return 1
	}
	if state {
		m.Live = live
	} else {
		m.Live = m.Live || live
	}
	if m.Author == nil {
		m.Author = author
	}
	if name != "" {
		m.Name = name
	}
	if body != "" {
		m.Body = body
	}
	return 1
}

func (f *fake) recount(entity string) int64 {
	s := f.stories[entity]
	if s == nil {
		return 0
	}
	n := 0
	for _, m := range f.meta {
		if m.Entity == entity && m.Live {
			n++
		}
	}
	if s.Count == n {
		return 0
	}
	s.Count = n
	return 1
}

func (f *fake) deleteMeta(ids []string) int64 {
	var n int64
	for _, id := range ids {
		if f.meta[id] != nil {
			delete(f.meta, id)
			delete(f.votes, id)
			n++
		}
	}
	return n
}

// removalInfo models sqlRemovalInfo.
func (f *fake) removalInfo(ids []string) map[string]any {
	type karma struct {
		AuthorID int64 `json:"author_id"`
		Net      int   `json:"net"`
	}
	entities := []string{}
	seen := map[string]bool{}
	net := map[int64]int{}
	for _, id := range ids {
		m := f.meta[id]
		if m == nil {
			continue
		}
		if !seen[m.Entity] {
			seen[m.Entity] = true
			entities = append(entities, m.Entity)
		}
		if m.Author == nil {
			continue
		}
		for _, v := range f.votes[id] {
			net[*m.Author] += v.Value
		}
	}
	ks := []karma{}
	for a, n := range net {
		ks = append(ks, karma{a, n})
	}
	return map[string]any{"entities": entities, "karma": ks}
}

// dirtyRows models sqlDirty: dirty rows by entity then id, at most 1000.
func (f *fake) dirtyRows() []map[string]any {
	var ms []*metaRow
	for _, m := range f.meta {
		if m.Dirty {
			ms = append(ms, m)
		}
	}
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].Entity != ms[j].Entity {
			return ms[i].Entity < ms[j].Entity
		}
		return ms[i].ID < ms[j].ID
	})
	out := []map[string]any{}
	for _, m := range ms[:min(len(ms), 1000)] {
		out = append(out, map[string]any{"comment_id": m.ID, "entity_id": m.Entity, "points": m.Points})
	}
	return out
}

// sample models sqlSample.
func (f *fake) sample(hour string) []string {
	seen := map[string]bool{}
	var es []string
	for _, m := range f.meta {
		if !seen[m.Entity] {
			seen[m.Entity] = true
			es = append(es, m.Entity)
		}
	}
	key := func(e string) string { return fmt.Sprintf("%x", md5.Sum([]byte(e+hour))) }
	sort.Slice(es, func(i, j int) bool { return key(es[i]) < key(es[j]) })
	if len(es) > 20 {
		es = es[:20]
	}
	if es == nil {
		es = []string{}
	}
	return es
}

// gone models sqlGone.
func (f *fake) gone(entity string, listed []string, cutoff time.Time) []string {
	in := map[string]bool{}
	for _, id := range listed {
		in[id] = true
	}
	out := []string{}
	for id, m := range f.meta {
		if m.Entity == entity && m.Created.Before(cutoff) && !in[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// newest models sqlNewest: live comments of live stories, newest first, 31 rows from offset.
func (f *fake) newest(offset int) []map[string]any {
	var ms []*metaRow
	for _, m := range f.meta {
		if s := f.stories[m.Entity]; m.Live && s != nil && !s.Dead {
			ms = append(ms, m)
		}
	}
	sort.Slice(ms, func(i, j int) bool {
		if !ms[i].Created.Equal(ms[j].Created) {
			return ms[i].Created.After(ms[j].Created)
		}
		return ms[i].ID < ms[j].ID
	})
	out := []map[string]any{}
	for i := min(offset, len(ms)); i < min(offset+31, len(ms)); i++ {
		m, s := ms[i], f.stories[ms[i].Entity]
		row := map[string]any{"comment_id": m.ID, "author_id": m.Author, "author_name": m.Name, "body_html": m.Body,
			"points": m.Points, "created_at": m.Created, "item_no": s.ItemNo, "title": s.Title}
		if m.Parent != "" {
			row["parent_id"] = m.Parent
		}
		out = append(out, row)
	}
	return out
}
