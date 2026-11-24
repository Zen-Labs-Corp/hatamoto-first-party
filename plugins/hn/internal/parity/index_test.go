package parity

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func TestSearchIndexPlain(t *testing.T) {
	for in, want := range map[string]string{
		"<p>Hello &amp; <i>wasm</i></p>\n<p>x</p>": "Hello & wasm x",
		"a<br>b":             "a b",
		"  spaced \t\n out ": "spaced out",
		"1 &lt; 2":           "1 < 2",
		"cut <a href=\"x":    "cut",
		"":                   "",
	} {
		if got := plain(in); got != want {
			t.Errorf("plain(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", maxDocBody) // 2 bytes each
	got := plain(long)
	if len(got) > maxDocBody || !utf8.ValidString(got) || len(got) < maxDocBody-1 {
		t.Errorf("cap: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestSearchIndexStoryEvents(t *testing.T) {
	db := &fakeDB{}
	install(t, db)
	r := newRegistry(t, nil)
	r.Publish(core.StoryEvent{Kind: core.StorySaved, EntityID: storyUUID, ItemNo: 42, Title: "A <b> title", Body: "text & more"})
	if len(db.Execs) != 1 || db.Execs[0].SQL != sqlUpsertStory || len(db.Execs[0].Args) != 3 ||
		db.Execs[0].Args[0] != storyUUID || db.Execs[0].Args[1] != "A <b> title" || db.Execs[0].Args[2] != "text & more" {
		t.Fatalf("saved: %v", db.Execs)
	}
	db.Execs = nil
	r.Publish(core.StoryEvent{Kind: core.StoryDeleted, EntityID: storyUUID})
	if len(db.Execs) != 2 || db.Execs[0].SQL != sqlDropStoryDocs || db.Execs[1].SQL != sqlDropStoryFavs || db.Execs[1].Args[0] != storyUUID {
		t.Fatalf("deleted: %v", db.Execs)
	}
	db.Execs = nil
	r.Publish(core.StoryEvent{Kind: core.StorySaved, EntityID: "nope"})
	r.Publish(core.StoryEvent{Kind: core.StoryDeleted, EntityID: ""})
	if len(db.Execs) != 0 {
		t.Errorf("bad ids ran %v", db.Execs)
	}
	db.Exec = func(string, []any) (int64, error) { return 0, hostErr("db_error") }
	r.Publish(core.StoryEvent{Kind: core.StorySaved, EntityID: storyUUID}) // must not panic
}

func TestSearchIndexCommentEvents(t *testing.T) {
	db := &fakeDB{}
	install(t, db)
	r := newRegistry(t, nil)
	pinNow(t, mustTime("2026-11-25T12:00:00Z"))
	ev := sdk.CommentEvent{ID: commentUUID, EntityID: storyUUID, Status: "approved", BodyHTML: "<p>Hello &amp; <i>wasm</i></p>\n<p>x</p>", CreatedAt: mustTime("2026-11-25T10:00:00Z")}
	for _, k := range []core.CommentKind{core.OnCreated, core.OnApproved, core.OnUpdated} {
		db.Execs = nil
		if err := r.CommentAction(k)(ev); err != nil {
			t.Fatal(err)
		}
		if len(db.Execs) != 1 || db.Execs[0].SQL != sqlUpsertComment {
			t.Fatalf("kind %d: %v", k, db.Execs)
		}
		a := db.Execs[0].Args
		if a[0] != commentUUID || a[1] != storyUUID || a[2] != "Hello & wasm x" || a[3] != "2026-11-25T10:00:00Z" {
			t.Errorf("args %v", a)
		}
	}
	zero := ev
	zero.CreatedAt = time.Time{}
	db.Execs = nil
	_ = r.CommentAction(core.OnCreated)(zero)
	if a := db.Execs[0].Args; a[3] != "2026-11-25T12:00:00Z" {
		t.Errorf("zero created_at: %v", a)
	}
	for _, status := range []string{"pending", "spam", "trash", "dead", ""} {
		db.Execs = nil
		e := ev
		e.Status = status
		_ = r.CommentAction(core.OnApproved)(e)
		if len(db.Execs) != 1 || db.Execs[0].SQL != sqlDropComment || db.Execs[0].Args[0] != commentUUID {
			t.Errorf("status %q: %v", status, db.Execs)
		}
	}
	db.Execs = nil
	for _, k := range []core.CommentKind{core.OnDeleted, core.OnSpam} {
		db.Execs = nil
		_ = r.CommentAction(k)(ev) // an approved status on these events must not index
		if len(db.Execs) != 1 || db.Execs[0].SQL != sqlDropComment {
			t.Errorf("kind %d: %v", k, db.Execs)
		}
	}
	db.Execs = nil
	_ = r.CommentAction(core.OnCreated)(sdk.CommentEvent{ID: "bad", Status: "approved"})
	if len(db.Execs) != 0 {
		t.Errorf("bad id ran %v", db.Execs)
	}
}

func TestSearchIndexCron(t *testing.T) {
	r := newRegistry(t, nil)
	if err := r.CronHandler("search")(sdk.CronEvent{Name: "search"}); err != nil {
		t.Fatalf("no host: %v", err)
	}

	missing := `[{"id":"` + commentUUID + `","entity_id":"` + storyUUID + `","body_html":"<p>Hi &amp; <b>there</b></p>","src_hash":"abc123","created_at":"2026-11-25T10:00:00Z"}]`
	db := &fakeDB{Query: func(sql string, _ []any) (any, error) {
		if sql == sqlMissingComment {
			return map[string]any{"rows": [][]any{{missing}}}, nil
		}
		return map[string]any{"rows": [][]any{}}, nil
	}}
	install(t, db)
	if err := r.CronHandler("search")(sdk.CronEvent{Name: "search"}); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, e := range db.Execs {
		order = append(order, e.SQL)
	}
	want := []string{sqlAddStories, sqlFixStories, sqlPruneStories, sqlPruneComments, sqlAddComments}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Fatalf("exec order %v", order)
	}
	if len(db.Queries) != 1 || db.Queries[0].SQL != sqlMissingComment {
		t.Errorf("queries %v", db.Queries)
	}
	docs, _ := db.Execs[4].Args[0].([]any)
	if len(docs) != 1 {
		t.Fatalf("comment arg %v", db.Execs[4].Args)
	}
	if d, _ := docs[0].(map[string]any); d["id"] != commentUUID || d["entity_id"] != storyUUID || d["body"] != "Hi & there" || d["created_at"] != "2026-11-25T10:00:00Z" || d["src_hash"] != "abc123" {
		t.Errorf("comment doc %v", d)
	}

	// an empty missing list makes no comment insert
	db.Execs, db.Queries, missing = nil, nil, "[]"
	_ = r.CronHandler("search")(sdk.CronEvent{Name: "search"})
	if len(db.Execs) != 4 {
		t.Errorf("empty: %d execs", len(db.Execs))
	}

	// a host error on step one stops the run, quietly
	db.Execs = nil
	db.Exec = func(string, []any) (int64, error) { return 0, hostErr("db_error") }
	if err := r.CronHandler("search")(sdk.CronEvent{Name: "search"}); err != nil || len(db.Execs) != 1 {
		t.Errorf("error run: %v, %d execs", err, len(db.Execs))
	}
}

func TestSearchIndexSQLIsSafe(t *testing.T) {
	for _, sql := range []string{sqlUpsertStory, sqlDropStoryDocs, sqlDropStoryFavs, sqlUpsertComment, sqlDropComment, sqlAddStories, sqlFixStories, sqlPruneStories, sqlPruneComments, sqlMissingComment, sqlAddComments} {
		low := strings.ToLower(sql)
		for _, bad := range []string{"pg_", "public", "ts_stat", "ts_rewrite", "ilike", "information_schema", "copy "} {
			if strings.Contains(low, bad) {
				t.Errorf("%q in %s", bad, sql)
			}
		}
	}
}

// Edited or anonymised comments must be re-indexed: the event stores md5 of the
// HTML and the cron selects live comments whose md5 differs from the stored hash.
func TestSearchIndexRefreshesChangedComments(t *testing.T) {
	db := &fakeDB{}
	install(t, db)
	r := newRegistry(t, nil)
	ev := sdk.CommentEvent{ID: commentUUID, EntityID: storyUUID, Status: "approved", BodyHTML: "<p>hi</p>"}
	_ = r.CommentAction(core.OnUpdated)(ev)
	if len(db.Execs) != 1 || db.Execs[0].Args[4] != "343fc7b27c1b966b623aa483c880210c" {
		t.Fatalf("execs %v", db.Execs)
	}
	if got := srcHash(""); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Errorf("md5 of empty: %s", got)
	}
	for _, want := range []string{"d.src_hash <> md5(c.body_html)", "d.target_id IS NULL", "c.live", "LIMIT 25"} {
		if !strings.Contains(sqlMissingComment, want) {
			t.Errorf("sqlMissingComment lacks %q", want)
		}
	}
	if !strings.Contains(sqlUpsertComment, "src_hash = EXCLUDED.src_hash") || !strings.Contains(sqlAddComments, "src_hash = EXCLUDED.src_hash") {
		t.Error("an upsert does not refresh src_hash")
	}
}
