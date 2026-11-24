package comments

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

var t0 = time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)

func event(id, status string) sdk.CommentEvent {
	return sdk.CommentEvent{ID: id, EntityID: eid1, ParentID: cid3, AuthorName: "Ann", BodyHTML: "<p>hi</p>",
		Status: status, CommenterKey: "k1", CreatedAt: t0}
}

// deliver sends one comment action through the registry.
func deliver(t *testing.T, r *core.Registry, k core.CommentKind, ev sdk.CommentEvent) {
	t.Helper()
	if err := r.CommentAction(k)(ev); err != nil {
		t.Fatalf("handler: %v", err)
	}
}

func eventFake(t *testing.T) (*fake, *core.Registry) {
	f := installFake(t)
	f.user("k1", "ann", 1)
	f.stories[eid1] = &storyRow{ItemNo: 1, Title: "S"}
	return f, newRegistry(nil)
}

func TestEvents(t *testing.T) {
	t.Run("created approved mirrors the row and counts", func(t *testing.T) {
		f, r := eventFake(t)
		deliver(t, r, core.OnCreated, event(cid1, "approved"))
		m := f.meta[cid1]
		if m == nil || !m.Live || m.Author == nil || *m.Author != 1 || m.Body != "<p>hi</p>" || m.Parent != cid3 || m.Entity != eid1 || m.Name != "Ann" {
			t.Fatalf("row = %+v", m)
		}
		if f.stories[eid1].Count != 1 {
			t.Fatalf("count = %d", f.stories[eid1].Count)
		}
	})

	t.Run("repeated deliveries keep one row and one count", func(t *testing.T) {
		for _, order := range [][]core.CommentKind{
			{core.OnCreated, core.OnApproved, core.OnCreated, core.OnApproved},
			{core.OnApproved, core.OnCreated, core.OnApproved, core.OnCreated},
		} {
			f, r := eventFake(t)
			for _, k := range order {
				deliver(t, r, k, event(cid1, "approved"))
			}
			if len(f.meta) != 1 || f.stories[eid1].Count != 1 {
				t.Fatalf("rows %d count %d", len(f.meta), f.stories[eid1].Count)
			}
		}
	})

	t.Run("pending then approved", func(t *testing.T) {
		f, r := eventFake(t)
		deliver(t, r, core.OnCreated, event(cid1, "pending"))
		if f.meta[cid1].Live || f.stories[eid1].Count != 0 {
			t.Fatalf("pending row = %+v count %d", f.meta[cid1], f.stories[eid1].Count)
		}
		deliver(t, r, core.OnApproved, event(cid1, "approved"))
		if !f.meta[cid1].Live || f.stories[eid1].Count != 1 {
			t.Fatalf("approved row = %+v count %d", f.meta[cid1], f.stories[eid1].Count)
		}
	})

	t.Run("late pending created keeps live", func(t *testing.T) {
		f, r := eventFake(t)
		deliver(t, r, core.OnApproved, event(cid1, "approved"))
		deliver(t, r, core.OnCreated, event(cid1, "pending"))
		if !f.meta[cid1].Live || f.stories[eid1].Count != 1 {
			t.Fatalf("row = %+v count %d", f.meta[cid1], f.stories[eid1].Count)
		}
	})

	t.Run("staff and unknown key have no author", func(t *testing.T) {
		f, r := eventFake(t)
		staff := event(cid1, "approved")
		staff.Staff = true
		deliver(t, r, core.OnCreated, staff)
		stranger := event(cid2, "approved")
		stranger.CommenterKey = "k2"
		deliver(t, r, core.OnCreated, stranger)
		if f.meta[cid1].Author != nil || f.meta[cid2].Author != nil {
			t.Fatalf("authors = %v %v", f.meta[cid1].Author, f.meta[cid2].Author)
		}
		for _, e := range f.execs {
			for _, a := range e.Args {
				if strings.Contains(string(a), "k1") && e.SQL == sqlUpsertCreated {
					t.Fatalf("staff key sent: %s", a)
				}
			}
		}
	})

	t.Run("updated refreshes body and live", func(t *testing.T) {
		f, r := eventFake(t)
		deliver(t, r, core.OnApproved, event(cid1, "approved"))
		up := event(cid1, "approved")
		up.BodyHTML = "<p>edited</p>"
		deliver(t, r, core.OnUpdated, up)
		if f.meta[cid1].Body != "<p>edited</p>" {
			t.Fatalf("body = %q", f.meta[cid1].Body)
		}
		deliver(t, r, core.OnUpdated, event(cid1, "dead"))
		if f.meta[cid1] == nil || f.meta[cid1].Live || f.stories[eid1].Count != 0 {
			t.Fatalf("dead row = %+v count %d", f.meta[cid1], f.stories[eid1].Count)
		}
	})

	for name, kind := range map[string]core.CommentKind{"spam": core.OnSpam, "deleted": core.OnDeleted} {
		t.Run(name+" removes row votes and karma", func(t *testing.T) {
			f, r := eventFake(t)
			f.user("k2", "bob", 1)
			f.user("k3", "cy", 1)
			deliver(t, r, core.OnApproved, event(cid1, "approved"))
			deliver(t, r, core.OnApproved, event(cid2, "approved"))
			f.votes[cid1] = map[int64]*voteRow{2: {Value: 1}, 3: {Value: 1}}
			f.users[1].Karma = 3 // 1 + two upvotes
			if f.stories[eid1].Count != 2 {
				t.Fatalf("count = %d", f.stories[eid1].Count)
			}
			ev := event(cid1, "spam")
			deliver(t, r, kind, ev)
			if f.meta[cid1] != nil || f.votes[cid1] != nil || f.meta[cid2] == nil {
				t.Fatalf("meta %v votes %v", f.meta, f.votes)
			}
			if f.users[1].Karma != 1 || f.stories[eid1].Count != 1 {
				t.Fatalf("karma %d count %d", f.users[1].Karma, f.stories[eid1].Count)
			}
			deliver(t, r, kind, ev) // second delivery: no-op
			if f.users[1].Karma != 1 || f.stories[eid1].Count != 1 {
				t.Fatalf("redelivery: karma %d count %d", f.users[1].Karma, f.stories[eid1].Count)
			}
		})
	}

	t.Run("delete without entity id uses the stored row", func(t *testing.T) {
		f, r := eventFake(t)
		deliver(t, r, core.OnApproved, event(cid1, "approved"))
		deliver(t, r, core.OnDeleted, sdk.CommentEvent{ID: cid1})
		if f.meta[cid1] != nil || f.stories[eid1].Count != 0 {
			t.Fatalf("meta %v count %d", f.meta, f.stories[eid1].Count)
		}
	})

	t.Run("entity without story_meta", func(t *testing.T) {
		f, r := eventFake(t)
		ev := event(cid1, "approved")
		ev.EntityID = eid2
		deliver(t, r, core.OnCreated, ev)
		deliver(t, r, core.OnDeleted, ev)
		if len(f.meta) != 0 {
			t.Fatalf("meta = %v", f.meta)
		}
	})

	t.Run("invalid ids are skipped", func(t *testing.T) {
		f, r := eventFake(t)
		bad := event("nope", "approved")
		deliver(t, r, core.OnCreated, bad)
		bad = event(cid1, "approved")
		bad.EntityID = "x'; --"
		deliver(t, r, core.OnCreated, bad)
		if len(f.meta) != 0 || len(f.execs) != 0 {
			t.Fatalf("meta %v execs %d", f.meta, len(f.execs))
		}
	})

	t.Run("db failure returns an error", func(t *testing.T) {
		f, r := eventFake(t)
		f.failSQL[sqlUpsertCreated] = errors.New("down")
		if err := r.CommentAction(core.OnCreated)(event(cid1, "approved")); err != nil {
			t.Fatalf("registry must swallow handler errors, got %v", err)
		}
		if err := onUpsert(sqlUpsertCreated, approved)(event(cid1, "approved")); err == nil {
			t.Fatal("handler hid a db failure")
		}
		f.failSQL = map[string]error{sqlRemovalInfo: errors.New("down")}
		if err := onRemove(event(cid1, "spam")); err == nil {
			t.Fatal("remove hid a db failure")
		}
	})

	t.Run("no host", func(t *testing.T) {
		core.Call = nil
		if err := onUpsert(sqlUpsertCreated, approved)(event(cid1, "approved")); err != nil {
			t.Fatal(err)
		}
		if err := onRemove(event(cid1, "spam")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("key travels only as the upsert argument", func(t *testing.T) {
		f, r := eventFake(t)
		deliver(t, r, core.OnApproved, event(cid1, "approved"))
		deliver(t, r, core.OnSpam, event(cid1, "spam"))
		for _, e := range f.execs {
			if e.SQL == sqlUpsertState {
				continue
			}
			b, _ := json.Marshal(e.Args)
			if strings.Contains(string(b), "k1") {
				t.Fatalf("key in %q args %s", e.SQL, b)
			}
		}
	})
}

func TestOnlyStoryCommentsAreMirrored(t *testing.T) {
	// the fake models the guard; pin it in the statements it stands for
	if !strings.Contains(sqlUpsertCreated, "FROM story_meta") || !strings.Contains(sqlUpsertState, "FROM story_meta") || !strings.Contains(sqlDirty, "JOIN story_meta") {
		t.Fatal("statements lost their story_meta guard")
	}
	f, r := eventFake(t)
	deliver(t, r, core.OnCreated, event(cid1, "approved"))
	foreign := event(cid2, "approved")
	foreign.EntityID = eid2
	deliver(t, r, core.OnCreated, foreign)
	if f.meta[cid1] == nil || f.meta[cid2] != nil {
		t.Fatalf("meta = %v", f.meta)
	}
}
