package stories

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
)

var owner = &sdk.HTTPUser{ID: "o1", Role: "owner"}

func postJob(t *testing.T, r *core.Registry, body string, u *sdk.HTTPUser) sdk.HTTPResponse {
	t.Helper()
	resp, err := r.ServeHTTP(sdk.HTTPRequest{Method: "POST", Pattern: "/admin/jobs", Body: body, User: u})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// jobDB has bob (id 2); the lowest-id member is id 7 unless noMembers.
func jobDB(noMembers bool) (*fakeDB, *[]string) {
	var created []string
	db, _ := submitDB(0, nil)
	db.Users = map[string]map[string]any{"k2": user(2, "bob")}
	query := db.Query
	db.Query = func(sql string, args []any) (any, error) {
		if strings.Contains(sql, "FROM users ORDER BY id") {
			if noMembers {
				return map[string]any{"rows": [][]any{}}, nil
			}
			return coretest.Rows(7), nil
		}
		return query(sql, args)
	}
	db.Entities["entities.create"] = func(a json.RawMessage) (any, error) {
		created = append(created, string(a))
		return map[string]any{"id": entityID}, nil
	}
	return db, &created
}

func insertArgs(t *testing.T, db *fakeDB) []any {
	t.Helper()
	for _, c := range db.Execs {
		if strings.Contains(c.SQL, "INSERT INTO story_meta") {
			return c.Args
		}
	}
	t.Fatalf("no story_meta insert: %v", execSQL(db))
	return nil
}

func TestJobsCreate(t *testing.T) {
	pinNow(t, voteNow)
	db, created := jobDB(false)
	install(t, db)
	resp := postJob(t, newRegistry(t, nil), `{"title":"Acme is hiring","url":"https://acme.example/jobs"}`, owner)
	if resp.Status != 200 || resp.Body != `{"item_no":57,"path":"/item/57"}` || resp.Headers["Cache-Control"] != "no-store" {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
	if len(*created) != 1 || !strings.Contains((*created)[0], `"kind":"job"`) {
		t.Errorf("created %v", *created)
	}
	args := insertArgs(t, db)
	if args[2] != float64(7) || args[8] != "job" {
		t.Errorf("insert args %v", args)
	}
}

func TestJobsBy(t *testing.T) {
	pinNow(t, voteNow)
	db, _ := jobDB(false)
	install(t, db)
	resp := postJob(t, newRegistry(t, nil), `{"title":"T","text":"x","by":"bob"}`, owner)
	if resp.Status != 200 {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
	if args := insertArgs(t, db); args[2] != float64(2) {
		t.Errorf("author %v", args[2])
	}
	resp = postJob(t, newRegistry(t, nil), `{"title":"T","text":"x","by":"nobody"}`, owner)
	if resp.Status != 400 || !strings.Contains(resp.Body, "unknown member") {
		t.Errorf("unknown: %d %s", resp.Status, resp.Body)
	}
}

func TestJobsNoMembers(t *testing.T) {
	pinNow(t, voteNow)
	db, created := jobDB(true)
	install(t, db)
	resp := postJob(t, newRegistry(t, nil), `{"title":"T","text":"x"}`, owner)
	if resp.Status != 409 || len(*created) != 0 {
		t.Errorf("%d %s", resp.Status, resp.Body)
	}
}

func TestJobsRefused(t *testing.T) {
	pinNow(t, voteNow)
	db, created := jobDB(false)
	install(t, db)
	r := newRegistry(t, nil)
	for body, want := range map[string]int{
		`{"title":"T","text":"x","extra":1}`:     400,
		`not json`:                               400,
		`{"title":"","text":"x"}`:                400,
		`{"title":"T"}`:                          400,
		`{"title":"T","url":"ftp://x.example/"}`: 400,
	} {
		if resp := postJob(t, r, body, owner); resp.Status != want {
			t.Errorf("%s: %d %s", body, resp.Status, resp.Body)
		}
	}
	if resp := postJob(t, r, `{"title":"T","text":"x"}`, nil); resp.Status != 403 {
		t.Errorf("no user: %d", resp.Status)
	}
	if len(*created) != 0 {
		t.Errorf("created %v", *created)
	}
}

func TestJobsOff(t *testing.T) {
	pinNow(t, voteNow)
	db, _ := jobDB(false)
	db.Entities["entities.create"] = func(json.RawMessage) (any, error) { return nil, hostErr("unavailable") }
	install(t, db)
	if resp := postJob(t, newRegistry(t, nil), `{"title":"T","text":"x"}`, owner); resp.Status != 503 {
		t.Errorf("%d %s", resp.Status, resp.Body)
	}
}
