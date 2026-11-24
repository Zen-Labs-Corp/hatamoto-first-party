package comments

import (
	"fmt"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// addDirty adds n dirty rows (points 1) for an entity.
func addDirty(f *fake, entity string, n int) []string {
	var ids []string
	if f.stories[entity] == nil {
		f.stories[entity] = &storyRow{ItemNo: 1, Title: "S"}
	}
	for i := range n {
		id := fmt.Sprintf("%s%s%010d", entity[:24], entity[34:], i) // keeps the uuid shape, unique per entity
		f.meta[id] = &metaRow{ID: id, Entity: entity, Live: true, Points: 1, Dirty: true}
		ids = append(ids, id)
	}
	return ids
}

func dirtyCount(f *fake) int {
	n := 0
	for _, m := range f.meta {
		if m.Dirty {
			n++
		}
	}
	return n
}

func batchSizes(f *fake) []int {
	var out []int
	for _, b := range f.setRank {
		out = append(out, len(b))
	}
	return out
}

func TestCommentRank(t *testing.T) {
	cron := sdk.CronEvent{Name: "comment_rank"}
	const entA, entB = "10000000-0000-4000-8000-00000000000a", "10000000-0000-4000-8000-00000000000b"

	t.Run("batches per entity", func(t *testing.T) {
		f := installFake(t)
		addDirty(f, entA, 450)
		b := addDirty(f, entB, 4)
		f.meta[b[0]].Points = 4
		if err := rankCron(cron); err != nil {
			t.Fatal(err)
		}
		got := batchSizes(f)
		want := []int{200, 200, 50, 4}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("batches = %v, want %v", got, want)
		}
		if dirtyCount(f) != 0 {
			t.Fatalf("%d rows still dirty", dirtyCount(f))
		}
		for _, b := range f.setRank[:3] {
			for _, it := range b {
				if f.meta[it.ID].Entity != entA {
					t.Fatalf("batch mixes entities: %v", it.ID)
				}
			}
		}
		for _, it := range f.setRank[3] {
			if it.Rank != float64(f.meta[it.ID].Points) {
				t.Fatalf("rank %v for points %d", it.Rank, f.meta[it.ID].Points)
			}
		}
	})

	t.Run("a vote during the run keeps the row dirty", func(t *testing.T) {
		f := installFake(t)
		ids := addDirty(f, entA, 3)
		f.setRankHook = func(int) error { f.meta[ids[1]].Points = 2; return nil }
		if err := rankCron(cron); err != nil {
			t.Fatal(err)
		}
		if f.meta[ids[0]].Dirty || !f.meta[ids[1]].Dirty || f.meta[ids[2]].Dirty {
			t.Fatalf("dirty flags %v %v %v", f.meta[ids[0]].Dirty, f.meta[ids[1]].Dirty, f.meta[ids[2]].Dirty)
		}
	})

	for name, e := range map[string]error{"unavailable": &sdk.HostError{Code: sdk.ErrUnavailable}, "rate limited": &sdk.HostError{Code: sdk.ErrRateLimited}} {
		t.Run(name+" stops quietly", func(t *testing.T) {
			f := installFake(t)
			addDirty(f, entA, 3)
			addDirty(f, entB, 3)
			f.setRankHook = func(int) error { return e }
			if err := rankCron(cron); err != nil {
				t.Fatal(err)
			}
			if len(f.setRank) != 1 || dirtyCount(f) != 6 {
				t.Fatalf("calls %d dirty %d", len(f.setRank), dirtyCount(f))
			}
		})
	}

	t.Run("another error skips that entity only", func(t *testing.T) {
		f := installFake(t)
		addDirty(f, entA, 450)
		addDirty(f, entB, 2)
		f.setRankHook = func(call int) error {
			if call == 0 {
				return &sdk.HostError{Code: sdk.ErrInvalidArgument}
			}
			return nil
		}
		if err := rankCron(cron); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(batchSizes(f)) != "[200 2]" {
			t.Fatalf("batches = %v", batchSizes(f))
		}
		if dirtyCount(f) != 0 {
			t.Fatalf("dirty = %d, a refused entity must be given up", dirtyCount(f))
		}
	})

	t.Run("a retryable error keeps the entity dirty", func(t *testing.T) {
		f := installFake(t)
		addDirty(f, entA, 3)
		addDirty(f, entB, 2)
		f.setRankHook = func(call int) error {
			if call == 0 {
				return &sdk.HostError{Code: sdk.ErrTimeout}
			}
			return nil
		}
		if err := rankCron(cron); err != nil {
			t.Fatal(err)
		}
		if dirtyCount(f) != 3 {
			t.Fatalf("dirty = %d, want entity A's 3", dirtyCount(f))
		}
	})

	t.Run("refused foreign entities cannot starve stories", func(t *testing.T) {
		f := installFake(t)
		base := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		const hn = "10000000-0000-4000-8000-0000000fffff"
		addDirty(f, hn, 1)
		for _, m := range f.meta {
			m.Created = base // newer than the foreign rows below
		}
		// 20 entities that are not stories sort before the story and are older.
		for i := range 20 {
			id := fmt.Sprintf("20000000-0000-4000-8000-0000000004%02d", i)
			f.meta[id] = &metaRow{ID: id, Entity: fmt.Sprintf("10000000-0000-4000-8000-0000000004%02d", i), Live: true, Points: 1, Dirty: true, Created: base.Add(-time.Hour)}
		}
		f.setRankHook = func(int) error {
			for _, it := range f.setRank[len(f.setRank)-1] {
				if f.meta[it.ID].Entity != hn {
					return &sdk.HostError{Code: sdk.ErrNotFound}
				}
			}
			return nil
		}
		if err := rankCron(cron); err != nil {
			t.Fatal(err)
		}
		if len(f.setRank) != 1 || f.setRank[0][0].ID != addDirtyID(hn) {
			t.Fatalf("batches %v", batchSizes(f))
		}
	})

	t.Run("time budget", func(t *testing.T) {
		f := installFake(t)
		addDirty(f, entA, 1000)
		now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		prev := core.Now
		t.Cleanup(func() { core.Now = prev })
		core.Now = func() time.Time { return now }
		f.setRankHook = func(int) error { now = now.Add(3 * time.Second); return nil }
		if err := rankCron(cron); err != nil {
			t.Fatal(err)
		}
		if len(f.setRank) != 3 {
			t.Fatalf("calls = %d, want 3 (stop once 8 s passed)", len(f.setRank))
		}
	})

	t.Run("call cap", func(t *testing.T) {
		f := installFake(t)
		for i := range 20 {
			addDirty(f, fmt.Sprintf("10000000-0000-4000-8000-0000000001%02d", i), 1)
		}
		if err := rankCron(cron); err != nil {
			t.Fatal(err)
		}
		if len(f.setRank) != maxRankCalls || dirtyCount(f) != 20-maxRankCalls {
			t.Fatalf("calls %d dirty %d", len(f.setRank), dirtyCount(f))
		}
	})

	t.Run("no host", func(t *testing.T) {
		core.Call = nil
		if err := rankCron(cron); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("db error", func(t *testing.T) {
		f := installFake(t)
		addDirty(f, entA, 2)
		f.failSQL[sqlDirty] = plainErr{}
		if err := rankCron(cron); err != nil || len(f.setRank) != 0 {
			t.Fatalf("err %v calls %d", err, len(f.setRank))
		}
	})

	t.Run("registered as comment_rank", func(t *testing.T) {
		f := installFake(t)
		addDirty(f, entA, 2)
		if err := newRegistry(nil).CronHandler("comment_rank")(cron); err != nil || len(f.setRank) != 1 {
			t.Fatalf("err %v calls %d", err, len(f.setRank))
		}
	})
}

func addDirtyID(entity string) string {
	return fmt.Sprintf("%s%s%010d", entity[:24], entity[34:], 0)
}
