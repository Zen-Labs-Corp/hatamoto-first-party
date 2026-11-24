package stories

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// editDB has alice (id 1) as author of story 42, created 10 minutes before voteNow.
func editDB(story map[string]any) (*fakeDB, *[]map[string]json.RawMessage) {
	row := map[string]any{"author_id": 1, "created_at": "2026-01-02T11:50:00Z", "url": "https://example.com/old", "body": "old text"}
	for k, v := range story {
		row[k] = v
	}
	db := voteDB(row)
	var updates []map[string]json.RawMessage
	db.Entities = map[string]func(json.RawMessage) (any, error){
		"entities.update": func(a json.RawMessage) (any, error) {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(a, &m)
			updates = append(updates, m)
			return map[string]any{"id": entityID}, nil
		},
		"entities.delete": func(json.RawMessage) (any, error) { return map[string]any{}, nil },
	}
	return db, &updates
}

func edit(t *testing.T, r *core.Registry, form url.Values, key string) (int, string) {
	t.Helper()
	resp := post(t, r, "POST /story/{id}/edit", map[string]string{"id": "42"}, form, key, false)
	return resp.Status, location(resp)
}

func remove(t *testing.T, r *core.Registry, key string) string {
	t.Helper()
	return location(post(t, r, "POST /story/{id}/delete", map[string]string{"id": "42"}, nil, key, false))
}

func TestStoryEditAuthor(t *testing.T) {
	pinNow(t, voteNow)
	db, updates := editDB(nil)
	install(t, db)
	r := newRegistry(t, nil)
	var saved []core.StoryEvent
	r.Subscribe(core.StorySaved, func(ev core.StoryEvent) { saved = append(saved, ev) })
	status, loc := edit(t, r, url.Values{"title": {"New title"}, "url": {"https://example.com/new"}, "text": {"hi"}}, "k1")
	if status != 303 || loc != "/item/42?m=story-saved" {
		t.Fatalf("%d %q", status, loc)
	}
	if len(*updates) != 1 {
		t.Fatalf("updates %v", *updates)
	}
	u := (*updates)[0]
	if len(u) != 3 || string(u["id"]) != `"`+entityID+`"` || string(u["name"]) != `"New title"` {
		t.Errorf("update args %v", u)
	}
	var attrs map[string]string
	_ = json.Unmarshal(u["attributes"], &attrs)
	if len(attrs) != 3 || attrs["url"] != "https://example.com/new" || attrs["text"] != "hi" || attrs["kind"] != "story" {
		t.Errorf("attributes %v", attrs)
	}
	if len(db.Execs) != 1 || !strings.Contains(db.Execs[0].SQL, "UPDATE story_meta SET title") {
		t.Errorf("execs %v", execSQL(db))
	}
	if len(saved) != 1 || saved[0].ItemNo != 42 || saved[0].Title != "New title" {
		t.Errorf("events %+v", saved)
	}
}

func TestStoryEditRefused(t *testing.T) {
	pinNow(t, voteNow)
	form := url.Values{"title": {"T"}, "url": {"https://example.com/new"}}
	for _, tc := range []struct {
		name     string
		story    map[string]any
		settings map[string]any
		key      string
		want     string
	}{
		{"other user", map[string]any{"author_id": 2}, nil, "k1", "/submit?id=42&m=story-not-author"},
		{"window over", map[string]any{"created_at": "2026-01-02T09:59:00Z"}, nil, "k1", "/submit?id=42&m=story-window"},
		{"window off", nil, map[string]any{"edit_window_minutes": 0}, "k1", "/submit?id=42&m=story-window"},
		{"signed out", nil, nil, "", "/login?m=story-login"},
	} {
		db, updates := editDB(tc.story)
		db.Settings = tc.settings
		install(t, db)
		_, loc := edit(t, newRegistry(t, nil), form, tc.key)
		if loc != tc.want || len(*updates) != 0 || len(db.Execs) != 0 {
			t.Errorf("%s: %q", tc.name, loc)
		}
	}
}

func TestStoryEditKeepText(t *testing.T) {
	pinNow(t, voteNow)
	db, updates := editDB(nil)
	install(t, db)
	r := newRegistry(t, nil)
	edit(t, r, url.Values{"title": {"T"}, "keep_text": {"1"}}, "k1")
	edit(t, r, url.Values{"title": {"T"}, "url": {"https://example.com/old"}}, "k1")
	if len(*updates) != 2 {
		t.Fatalf("updates %v", *updates)
	}
	if a := string((*updates)[0]["attributes"]); !strings.Contains(a, `"text":"old text"`) {
		t.Errorf("keep_text: %s", a)
	}
	if a := string((*updates)[1]["attributes"]); !strings.Contains(a, `"text":null`) {
		t.Errorf("cleared text must be sent as null: %s", a)
	}
}

func TestStoryEditJobStaysJob(t *testing.T) {
	pinNow(t, voteNow)
	db, updates := editDB(map[string]any{"kind": "job"})
	install(t, db)
	edit(t, newRegistry(t, nil), url.Values{"title": {"Ask: x"}, "keep_text": {"1"}}, "k1")
	if len(*updates) != 1 || !strings.Contains(string((*updates)[0]["attributes"]), `"kind":"job"`) {
		t.Errorf("updates %v", *updates)
	}
}

func TestStoryEditDuplicateURL(t *testing.T) {
	pinNow(t, voteNow)
	db, updates := editDB(nil)
	query := db.Query
	db.Query = func(sql string, args []any) (any, error) {
		if strings.Contains(sql, "entity_id <> $3::uuid") {
			return map[string]any{"rows": [][]any{{9}}}, nil
		}
		return query(sql, args)
	}
	install(t, db)
	_, loc := edit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "url": {"https://example.com/other"}}, "k1")
	if loc != "/item/9?m=story-dup" || len(*updates) != 0 {
		t.Errorf("%q", loc)
	}
}

func TestStoryEditOff(t *testing.T) {
	pinNow(t, voteNow)
	db, _ := editDB(nil)
	db.Entities["entities.update"] = func(json.RawMessage) (any, error) { return nil, hostErr("unavailable") }
	install(t, db)
	_, loc := edit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "keep_text": {"1"}}, "k1")
	if loc != "/submit?id=42&m=story-off" || len(db.Execs) != 0 {
		t.Errorf("%q execs %v", loc, execSQL(db))
	}
}

func TestStoryEditDelete(t *testing.T) {
	pinNow(t, voteNow)
	db, _ := editDB(map[string]any{"comment_count": 1})
	install(t, db)
	if loc := remove(t, newRegistry(t, nil), "k1"); loc != "/item/42?m=story-has-comments" || len(db.Execs) != 0 {
		t.Errorf("with comments: %q", loc)
	}

	db, _ = editDB(nil)
	h := install(t, db)
	r := newRegistry(t, nil)
	var gone []core.StoryEvent
	r.Subscribe(core.StoryDeleted, func(ev core.StoryEvent) { gone = append(gone, ev) })
	if loc := remove(t, r, "k1"); loc != "/newest?m=story-deleted" {
		t.Errorf("delete: %q", loc)
	}
	sql := execSQL(db)
	if len(sql) != 2 || !strings.Contains(sql[0], "DELETE FROM story_meta") || !strings.Contains(sql[1], "DELETE FROM item_numbers") {
		t.Errorf("execs %v", sql)
	}
	if !strings.Contains(strings.Join(h.Calls, ","), "entities.delete") || len(gone) != 1 || gone[0].ItemNo != 42 {
		t.Errorf("calls %v events %+v", h.Calls, gone)
	}

	db, _ = editDB(nil)
	install(t, db)
	if loc := remove(t, newRegistry(t, nil), ""); loc != "/login?m=story-login" {
		t.Errorf("signed out: %q", loc)
	}
}

func TestStoryEditClearsURLWithNull(t *testing.T) {
	pinNow(t, voteNow)
	db, updates := editDB(nil)
	install(t, db)
	edit(t, newRegistry(t, nil), url.Values{"title": {"T"}, "text": {"now text"}}, "k1")
	if len(*updates) != 1 {
		t.Fatalf("updates %v", *updates)
	}
	if a := string((*updates)[0]["attributes"]); !strings.Contains(a, `"url":null`) || strings.Contains(a, `"text":null`) {
		t.Errorf("attributes %s", a)
	}
}
