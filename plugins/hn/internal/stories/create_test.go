package stories

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
)

const entityID = "0b9a1f2e-3c4d-4e5f-8a6b-7c8d9e0f1a2b"

// createDB answers the number reservation with 57 and entities.create with out.
func createDB(create func(json.RawMessage) (any, error)) *fakeDB {
	return &fakeDB{
		Query: func(sql string, args []any) (any, error) {
			if strings.Contains(sql, "INSERT INTO item_numbers") {
				return coretest.Rows(57), nil
			}
			return map[string]any{"rows": [][]any{}}, nil
		},
		Entities: map[string]func(json.RawMessage) (any, error){
			"entities.create": create,
			"entities.delete": func(json.RawMessage) (any, error) { return map[string]any{}, nil },
		},
	}
}

func okCreate(json.RawMessage) (any, error) { return map[string]any{"id": entityID}, nil }

func execSQL(db *fakeDB) []string {
	var out []string
	for _, c := range db.Execs {
		out = append(out, c.SQL)
	}
	return out
}

func TestCreateStoryWritesEntityThenMeta(t *testing.T) {
	pinNow(t, mustTime("2026-01-02T03:04:05Z"))
	var got struct {
		Name       string
		Slug       string
		Status     string
		Attributes map[string]any
	}
	db := createDB(func(a json.RawMessage) (any, error) {
		if err := json.Unmarshal(a, &got); err != nil {
			t.Errorf("args %s: %v", a, err)
		}
		return map[string]any{"id": entityID}, nil
	})
	h := install(t, db)
	var saved []core.StoryEvent
	r := newRegistry(t, map[string]func(*core.Registry){"parity": func(r *core.Registry) {
		r.Subscribe(core.StorySaved, func(ev core.StoryEvent) { saved = append(saved, ev) })
	}})
	no, n := createStory(r, newStory{AuthorID: 3, Title: "Hello", URL: "https://example.com/a", URLKey: "example.com/a?", Domain: "example.com", Text: "body", Kind: "story"})
	if no != 57 || n != "" {
		t.Fatalf("createStory = %d, %q", no, n)
	}
	if got.Name != "Hello" || got.Slug != "57" || got.Status != "published" {
		t.Errorf("entity args %+v", got)
	}
	if want := map[string]any{"url": "https://example.com/a", "text": "body", "kind": "story"}; !reflect.DeepEqual(got.Attributes, want) {
		t.Errorf("attributes %v, want %v", got.Attributes, want)
	}
	if want := []string{"db.query", "entities.create", "db.exec"}; !reflect.DeepEqual(h.Calls, want) {
		t.Errorf("calls %v, want %v", h.Calls, want)
	}
	ins := db.Execs[0]
	if !strings.Contains(ins.SQL, "INSERT INTO story_meta") || ins.Args[0] != entityID || ins.Args[1] != float64(57) || ins.Args[2] != float64(3) {
		t.Errorf("story_meta insert %+v", ins)
	}
	if len(saved) != 1 || saved[0].ItemNo != 57 || saved[0].EntityID != entityID || saved[0].Title != "Hello" {
		t.Errorf("events %+v", saved)
	}
}

func TestCreateStoryOmitsEmptyURL(t *testing.T) {
	var got struct{ Attributes map[string]any }
	install(t, createDB(func(a json.RawMessage) (any, error) {
		_ = json.Unmarshal(a, &got)
		return map[string]any{"id": entityID}, nil
	}))
	r := newRegistry(t, nil)
	if _, n := createStory(r, newStory{AuthorID: 3, Title: "Ask: why", Text: "because", Kind: "ask"}); n != "" {
		t.Fatalf("notice %q", n)
	}
	if _, has := got.Attributes["url"]; has {
		t.Errorf("empty url sent: %v", got.Attributes)
	}
	if got.Attributes["kind"] != "ask" || got.Attributes["text"] != "because" {
		t.Errorf("attributes %v", got.Attributes)
	}
}

func testCreateFailure(t *testing.T, create func(json.RawMessage) (any, error), want core.Notice) {
	t.Helper()
	db := createDB(create)
	install(t, db)
	var saved int
	r := newRegistry(t, map[string]func(*core.Registry){"parity": func(r *core.Registry) {
		r.Subscribe(core.StorySaved, func(core.StoryEvent) { saved++ })
	}})
	no, n := createStory(r, newStory{AuthorID: 3, Title: "Hello", Kind: "story"})
	if no != 0 || n != want {
		t.Fatalf("createStory = %d, %q, want 0, %q", no, n, want)
	}
	if len(db.Execs) != 1 || !strings.Contains(db.Execs[0].SQL, "DELETE FROM item_numbers") || db.Execs[0].Args[0] != float64(57) {
		t.Errorf("execs %v", execSQL(db))
	}
	if saved != 0 {
		t.Error("event published")
	}
}

func TestCreateStoryUnavailable(t *testing.T) {
	testCreateFailure(t, func(json.RawMessage) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }, nOff)
}

func TestCreateStoryRateLimited(t *testing.T) {
	testCreateFailure(t, func(json.RawMessage) (any, error) { return nil, hostErr(sdk.ErrRateLimited) }, nBusy)
}

func TestCreateStoryEmptyID(t *testing.T) {
	testCreateFailure(t, func(json.RawMessage) (any, error) { return map[string]any{}, nil }, nError)
}

func TestCreateStoryMetaFailureDeletesEntity(t *testing.T) {
	db := createDB(okCreate)
	db.Exec = func(sql string, _ []any) (int64, error) {
		if strings.Contains(sql, "INSERT INTO story_meta") {
			return 0, hostErr("internal")
		}
		return 1, nil
	}
	var deleted string
	db.Entities["entities.delete"] = func(a json.RawMessage) (any, error) {
		var in struct{ ID string }
		_ = json.Unmarshal(a, &in)
		deleted = in.ID
		return map[string]any{}, nil
	}
	install(t, db)
	r := newRegistry(t, nil)
	if no, n := createStory(r, newStory{AuthorID: 3, Title: "Hello", Kind: "story"}); no != 0 || n != nError {
		t.Fatalf("createStory = %d, %q", no, n)
	}
	if deleted != entityID {
		t.Errorf("entity deleted %q", deleted)
	}
	if last := db.Execs[len(db.Execs)-1]; !strings.Contains(last.SQL, "DELETE FROM item_numbers") || last.Args[0] != float64(57) {
		t.Errorf("last exec %+v", last)
	}
}
