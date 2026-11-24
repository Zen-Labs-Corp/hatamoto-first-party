package moderation

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const commentID = "11111111-1111-1111-1111-111111111111"

func deadModeDB(dead bool) *fakeDB {
	return &fakeDB{
		Users: map[string]map[string]any{"k7": user(7, "ann")},
		Query: func(sql string, _ []any) (any, error) {
			if sql != sqlDeadBan {
				return nil, hostErr(sdk.ErrInvalidArgument)
			}
			return jsonRow(map[string]any{"dead": dead}), nil
		},
	}
}

func TestDeadMode(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	ev := sdk.CommentEvent{ID: commentID, CommenterKey: "k7"}

	t.Run("dead-mode commenter is marked dead", func(t *testing.T) {
		var marked []string
		db := deadModeDB(true)
		db.Comments = func(fn string, ids []string) (any, error) {
			marked = append(marked, fn+":"+strings.Join(ids, ","))
			return map[string]any{"changed": 1}, nil
		}
		h := install(t, db)
		if err := deadModeComment(ev); err != nil {
			t.Fatal(err)
		}
		if len(marked) != 1 || marked[0] != sdk.FnCommentsMarkDead+":"+commentID {
			t.Fatalf("marked = %v", marked)
		}
		if len(db.Execs) != 2 || db.Execs[0].SQL != sqlKillInsert || db.Execs[0].Args[2] != "ban" || !strings.Contains(db.Execs[0].SQL, "SET live = false") || db.Execs[1].SQL != sqlLogInsert {
			t.Fatalf("execs = %+v", db.Execs)
		}
		noEntityCalls(t, h)
	})

	t.Run("other commenter is left alone", func(t *testing.T) {
		h := install(t, deadModeDB(false))
		if err := deadModeComment(ev); err != nil {
			t.Fatal(err)
		}
		for _, c := range h.Calls {
			if c == sdk.FnCommentsMarkDead {
				t.Fatal("mark_dead called")
			}
		}
		noEntityCalls(t, h)
	})

	t.Run("unknown member is left alone", func(t *testing.T) {
		h := install(t, deadModeDB(true))
		if err := deadModeComment(sdk.CommentEvent{ID: commentID, CommenterKey: "nobody"}); err != nil {
			t.Fatal(err)
		}
		noEntityCalls(t, h)
	})

	t.Run("unavailable writes no kill", func(t *testing.T) {
		db := deadModeDB(true)
		db.Comments = func(string, []string) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }
		h := install(t, db)
		if err := deadModeComment(ev); err != nil {
			t.Fatalf("err = %v, want fail open", err)
		}
		if len(db.Execs) != 0 {
			t.Fatalf("execs = %+v", db.Execs)
		}
		noEntityCalls(t, h)
	})

	t.Run("story saved runs the dead CTE", func(t *testing.T) {
		db := &fakeDB{}
		h := install(t, db)
		r := newRegistry(t)
		r.Publish(core.StoryEvent{Kind: core.StorySaved, EntityID: commentID, ItemNo: 5})
		if len(db.Execs) != 1 || db.Execs[0].SQL != sqlStoryDeadMode || db.Execs[0].Args[0] != commentID {
			t.Fatalf("execs = %+v", db.Execs)
		}
		if !strings.Contains(db.Execs[0].SQL, "SET dead = true") || strings.Contains(db.Execs[0].SQL, "entities") {
			t.Fatalf("unexpected sql %q", db.Execs[0].SQL)
		}
		noEntityCalls(t, h)
	})
}

func TestStoryDeletedDropsModeration(t *testing.T) {
	db := &fakeDB{}
	install(t, db)
	r := newRegistry(t)
	r.Publish(core.StoryEvent{Kind: core.StoryDeleted, EntityID: storyUUID})
	if len(db.Execs) != 1 || db.Execs[0].SQL != sqlStoryDrop || db.Execs[0].Args[0] != storyUUID {
		t.Fatalf("execs = %+v", db.Execs)
	}
	for _, t2 := range []string{"flags", "vouches", "kills"} {
		if !strings.Contains(sqlStoryDrop, "FROM "+t2+" ") {
			t.Errorf("%s not cleaned", t2)
		}
	}
	db.Execs = nil
	r.Publish(core.StoryEvent{Kind: core.StoryDeleted})
	if len(db.Execs) != 0 {
		t.Fatalf("empty entity id ran %+v", db.Execs)
	}
}
