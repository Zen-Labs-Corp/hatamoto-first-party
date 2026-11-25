package stories

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func TestEntityDeletedDropsTheStory(t *testing.T) {
	db := &fakeDB{Query: func(sql string, args []any) (any, error) {
		if strings.Contains(sql, "DELETE FROM story_meta") && args[0] == entityID {
			return map[string]any{"rows": [][]any{{`{"item_no":42,"title":"T"}`}}}, nil
		}
		return map[string]any{"rows": [][]any{}}, nil
	}}
	install(t, db)
	r := newRegistry(t, nil)
	var gone []core.StoryEvent
	r.Subscribe(core.StoryDeleted, func(ev core.StoryEvent) { gone = append(gone, ev) })
	h := EntityDeleted(r)

	// another content type, or no id: nothing happens
	for _, ev := range []sdk.EntityEvent{{ID: entityID, SchemaSlug: "page"}, {SchemaSlug: storySchema}} {
		if err := h(ev); err != nil || len(gone) != 0 || len(db.Execs) != 0 {
			t.Fatalf("%+v: err %v events %v execs %v", ev, err, gone, execSQL(db))
		}
	}
	if err := h(sdk.EntityEvent{ID: entityID, SchemaSlug: storySchema}); err != nil {
		t.Fatal(err)
	}
	if sql := execSQL(db); len(sql) != 1 || !strings.Contains(sql[0], "DELETE FROM item_numbers") {
		t.Errorf("execs %v", sql)
	}
	if len(gone) != 1 || gone[0].ItemNo != 42 || gone[0].EntityID != entityID || gone[0].Title != "T" {
		t.Errorf("events %+v", gone)
	}

	// already gone (the author's own delete): no event
	db.Query = func(string, []any) (any, error) { return map[string]any{"rows": [][]any{}}, nil }
	if err := h(sdk.EntityEvent{ID: entityID, SchemaSlug: storySchema}); err != nil || len(gone) != 1 {
		t.Errorf("second delete: err %v events %d", err, len(gone))
	}
}
