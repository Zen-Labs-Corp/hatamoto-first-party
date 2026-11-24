package comments

import (
	"fmt"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// reconFake has story eid1 with live comments cid1..cid3 by author user 1 (karma 3).
func reconFake(t *testing.T) *fake {
	f := installFake(t)
	a := f.user("k1", "ann", 3)
	f.user("k2", "bob", 1)
	f.stories[eid1] = &storyRow{ItemNo: 1, Title: "S", Count: 3}
	for _, id := range []string{cid1, cid2, cid3} {
		f.meta[id] = &metaRow{ID: id, Entity: eid1, Author: &a.ID, Live: true, Points: 1, Body: "<p>" + id[len(id)-1:] + "</p>", Name: "Ann"}
	}
	return f
}

func listing(f *fake, ids ...string) {
	f.threads[eid1] = nil
	for _, id := range ids {
		f.threads[eid1] = append(f.threads[eid1], sdk.CommentItem{ID: id, BodyHTML: f.meta[id].Body, AuthorName: f.meta[id].Name})
	}
}

// notFound makes core answer not_found for the comments' subtrees.
func notFound(f *fake, ids ...string) {
	for _, id := range ids {
		f.subtreeErrFor[id] = &sdk.HostError{Code: sdk.ErrNotFound}
	}
}

func TestReconcile(t *testing.T) {
	cron := sdk.CronEvent{Name: "reconcile"}
	run := func(t *testing.T) {
		t.Helper()
		if err := reconcileCron(cron); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("missing comment is removed with votes and karma", func(t *testing.T) {
		f := reconFake(t)
		f.votes[cid2] = map[int64]*voteRow{2: {Value: 1}}
		listing(f, cid1, cid3)
		notFound(f, cid2)
		run(t)
		if f.meta[cid2] != nil || f.votes[cid2] != nil || f.meta[cid1] == nil || f.meta[cid3] == nil {
			t.Fatalf("meta %v votes %v", f.meta, f.votes)
		}
		if f.users[1].Karma != 2 || f.stories[eid1].Count != 2 {
			t.Fatalf("karma %d count %d", f.users[1].Karma, f.stories[eid1].Count)
		}
	})

	t.Run("a comment approved during the listing is kept", func(t *testing.T) {
		f := reconFake(t)
		f.votes[cid2] = map[int64]*voteRow{2: {Value: 1}}
		listing(f, cid1, cid3) // cid2 was approved after its page was read; core still has it
		run(t)
		if f.meta[cid2] == nil || f.votes[cid2] == nil || f.users[1].Karma != 3 || len(f.subtreeCalls) != 1 {
			t.Fatalf("meta %v karma %d calls %v", f.meta, f.users[1].Karma, f.subtreeCalls)
		}
	})

	t.Run("a page shift during the listing keeps the comment", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1, cid2) // cid3 slid past the page boundary while page 1 was read
		f.threadPage = 1
		run(t)
		if f.meta[cid3] == nil {
			t.Fatalf("meta = %v", f.meta)
		}
	})

	t.Run("an unconfirmed deletion is kept", func(t *testing.T) {
		for name, e := range map[string]error{"unavailable": &sdk.HostError{Code: sdk.ErrUnavailable}, "other": plainErr{}} {
			f := reconFake(t)
			listing(f, cid1, cid3)
			f.subtreeErrFor[cid2] = e
			run(t)
			if f.meta[cid2] == nil {
				t.Fatalf("%s: row deleted", name)
			}
		}
	})

	t.Run("confirmations are capped per run", func(t *testing.T) {
		f := installFake(t)
		f.stories[eid1] = &storyRow{ItemNo: 1, Title: "S"}
		for i := range maxConfirms + 5 {
			id := fmt.Sprintf("30000000-0000-4000-8000-0000000005%02d", i)
			f.meta[id] = &metaRow{ID: id, Entity: eid1, Live: true, Points: 1}
			notFound(f, id)
		}
		f.threads[eid1] = []sdk.CommentItem{{ID: cid1}}
		f.meta[cid1] = &metaRow{ID: cid1, Entity: eid1, Live: true, Points: 1}
		run(t)
		if len(f.subtreeCalls) != maxConfirms || len(f.meta) != 1+5 {
			t.Fatalf("calls %d meta %d", len(f.subtreeCalls), len(f.meta))
		}
	})

	t.Run("listed comments missing from the mirror are mirrored", func(t *testing.T) {
		f := reconFake(t)
		const cid4 = "00000000-0000-4000-8000-000000000004"
		f.threads[eid1] = []sdk.CommentItem{
			{ID: cid1, BodyHTML: f.meta[cid1].Body, AuthorName: "Ann"},
			{ID: cid4, ParentID: cid1, AuthorName: "Bob", BodyHTML: "<p>new</p>", CommenterKey: "k2", CreatedAt: t0},
			{ID: cid2, BodyHTML: f.meta[cid2].Body, AuthorName: "Ann"},
			{ID: cid3, BodyHTML: f.meta[cid3].Body, AuthorName: "Ann"},
		}
		run(t)
		m := f.meta[cid4]
		if m == nil || !m.Live || m.Author == nil || *m.Author != 2 || m.Parent != cid1 || m.Body != "<p>new</p>" || f.stories[eid1].Count != 4 {
			t.Fatalf("row %+v count %d", m, f.stories[eid1].Count)
		}
	})

	t.Run("a story without any mirrored comment is backfilled", func(t *testing.T) {
		f := installFake(t)
		f.stories[eid2] = &storyRow{ItemNo: 2, Title: "T"}
		f.threads[eid2] = []sdk.CommentItem{{ID: cid1, BodyHTML: "<p>old</p>", AuthorName: "Ann", CreatedAt: t0}}
		run(t)
		if f.meta[cid1] == nil || f.stories[eid2].Count != 1 {
			t.Fatalf("meta %v", f.meta)
		}
	})

	t.Run("a paged listing deletes nothing wrongly", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1, cid2, cid3) // fake pages of 2
		run(t)
		if len(f.meta) != 3 || len(f.threadCall) != 2 {
			t.Fatalf("meta %d calls %v", len(f.meta), f.threadCall)
		}
	})

	t.Run("empty listing deletes nothing", func(t *testing.T) {
		f := reconFake(t)
		run(t)
		if len(f.meta) != 3 || f.users[1].Karma != 3 {
			t.Fatalf("meta %d karma %d", len(f.meta), f.users[1].Karma)
		}
	})

	t.Run("incomplete listing deletes nothing", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1)
		f.threadMore = true
		run(t)
		if len(f.meta) != 3 || len(f.threadCall) != maxThreadPages {
			t.Fatalf("meta %d calls %d", len(f.meta), len(f.threadCall))
		}
	})

	t.Run("unavailable thread changes nothing", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1)
		f.threadHook = func(int) error { return &sdk.HostError{Code: sdk.ErrUnavailable} }
		run(t)
		if len(f.meta) != 3 {
			t.Fatalf("meta = %d", len(f.meta))
		}
	})

	t.Run("another thread error skips the entity", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1)
		f.threadHook = func(int) error { return plainErr{} }
		run(t)
		if len(f.meta) != 3 {
			t.Fatalf("meta = %d", len(f.meta))
		}
	})

	t.Run("body, name and dead status sync", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1, cid2, cid3)
		f.threads[eid1][1] = sdk.CommentItem{ID: cid2, BodyHTML: "<p>[deleted]</p>", AuthorName: "[deleted]", Status: "dead"}
		run(t)
		m := f.meta[cid2]
		if m == nil || m.Body != "<p>[deleted]</p>" || m.Name != "[deleted]" || m.Live || f.stories[eid1].Count != 2 {
			t.Fatalf("row %+v count %d", m, f.stories[eid1].Count)
		}
	})

	t.Run("points drift is repaired", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1, cid2, cid3)
		f.meta[cid1].Points = 5
		f.meta[cid1].Dirty = false
		f.votes[cid1] = map[int64]*voteRow{2: {Value: 1}}
		run(t)
		if f.meta[cid1].Points != 2 || !f.meta[cid1].Dirty {
			t.Fatalf("points %d dirty %v", f.meta[cid1].Points, f.meta[cid1].Dirty)
		}
	})

	t.Run("a comment newer than the listing is kept", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1)
		now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		prev := core.Now
		t.Cleanup(func() { core.Now = prev })
		core.Now = func() time.Time { return now }
		f.meta[cid3].Created = now.Add(time.Second)
		f.meta[cid2].Created = now.Add(-time.Hour)
		notFound(f, cid2, cid3)
		run(t)
		if f.meta[cid2] != nil || f.meta[cid3] == nil {
			t.Fatalf("meta = %v", f.meta)
		}
	})

	t.Run("sample is capped at 20 entities", func(t *testing.T) {
		f := installFake(t)
		for i := range 25 {
			e := fmt.Sprintf("10000000-0000-4000-8000-0000000002%02d", i)
			id := fmt.Sprintf("20000000-0000-4000-8000-0000000002%02d", i)
			f.meta[id] = &metaRow{ID: id, Entity: e, Live: true, Points: 1}
			f.threads[e] = []sdk.CommentItem{{ID: id}}
		}
		run(t)
		if len(f.threadCall) != 20 || len(f.meta) != 25 {
			t.Fatalf("calls %d meta %d", len(f.threadCall), len(f.meta))
		}
	})

	t.Run("time budget stops the loop", func(t *testing.T) {
		f := installFake(t)
		now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		prev := core.Now
		t.Cleanup(func() { core.Now = prev })
		core.Now = func() time.Time { return now }
		for i := range 10 {
			e := fmt.Sprintf("10000000-0000-4000-8000-0000000003%02d", i)
			id := fmt.Sprintf("20000000-0000-4000-8000-0000000003%02d", i)
			f.meta[id] = &metaRow{ID: id, Entity: e, Live: true, Points: 1}
			f.threads[e] = []sdk.CommentItem{{ID: id}}
		}
		f.threadHook = func(int) error { now = now.Add(3 * time.Second); return nil }
		run(t)
		if len(f.threadCall) != 3 {
			t.Fatalf("calls = %d, want 3", len(f.threadCall))
		}
	})

	t.Run("story deleted removes its comments", func(t *testing.T) {
		f := reconFake(t)
		f.meta[cid3].Entity = eid2
		f.votes[cid1] = map[int64]*voteRow{2: {Value: 1}}
		r := newRegistry(nil)
		r.Publish(core.StoryEvent{Kind: core.StoryDeleted, EntityID: eid1})
		if f.meta[cid1] != nil || f.meta[cid2] != nil || f.meta[cid3] == nil {
			t.Fatalf("meta = %v", f.meta)
		}
		if f.users[1].Karma != 2 {
			t.Fatalf("karma = %d", f.users[1].Karma)
		}
	})

	t.Run("no host", func(t *testing.T) {
		core.Call = nil
		run(t)
		onStoryDeleted(core.StoryEvent{Kind: core.StoryDeleted, EntityID: eid1})
	})

	t.Run("registered as reconcile", func(t *testing.T) {
		f := reconFake(t)
		listing(f, cid1, cid3)
		notFound(f, cid2)
		if err := newRegistry(nil).CronHandler("reconcile")(cron); err != nil || f.meta[cid2] != nil {
			t.Fatalf("err %v meta %v", err, f.meta)
		}
	})
}
