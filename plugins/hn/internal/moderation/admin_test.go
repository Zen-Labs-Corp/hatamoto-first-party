package moderation

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func adminCall(t *testing.T, method, pattern string, vals map[string]string, query map[string][]string, body string, user *sdk.HTTPUser) sdk.HTTPResponse {
	t.Helper()
	resp, err := newRegistry(t).ServeHTTP(sdk.HTTPRequest{Method: method, Pattern: pattern, PathValues: vals, Query: query, Body: body, User: user})
	if err != nil {
		t.Fatalf("%s %s: %v", method, pattern, err)
	}
	return resp
}

var owner = &sdk.HTTPUser{ID: "u1", Role: "owner"}

func asOwner(t *testing.T, method, pattern string, vals map[string]string, body string) sdk.HTTPResponse {
	return adminCall(t, method, pattern, vals, nil, body, owner)
}

func wantStatus(t *testing.T, resp sdk.HTTPResponse, status int) map[string]any {
	t.Helper()
	if resp.Status != status {
		t.Fatalf("status %d (%s), want %d", resp.Status, resp.Body, status)
	}
	if resp.Headers["Content-Type"] != "application/json" {
		t.Fatalf("content type %q", resp.Headers["Content-Type"])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &out); err != nil {
		t.Fatalf("body %q: %v", resp.Body, err)
	}
	return out
}

// bobDB knows member bob (id 9) and answers resolve queries with it.
func bobDB() *fakeDB {
	return &fakeDB{Query: func(sql string, args []any) (any, error) {
		switch sql {
		case userSelectByName:
			return jsonRow(withKarma(9, "bob", 10)), nil
		case sqlResolveStoryID, sqlResolveComment:
			if args[0] == storyUUID || args[0] == commentID {
				return jsonRow(item(args[0].(string), 5, 9, false, "")), nil
			}
		}
		return map[string]any{"rows": [][]any{}}, nil
	}}
}

const userSelectByName = "SELECT to_jsonb(u) FROM users u WHERE u.username_lower = lower($1)"

func TestAdminOwnerOnly(t *testing.T) {
	install(t, bobDB())
	routes := []struct{ method, pattern string }{
		{"GET", "/admin/queue"}, {"GET", "/admin/log"},
		{"POST", "/admin/users/{name}/ban"}, {"POST", "/admin/users/{name}/unban"}, {"POST", "/admin/users/{name}/password/remove"},
		{"POST", "/admin/domains"}, {"POST", "/admin/domains/remove"},
		{"POST", "/admin/nets/ban"}, {"POST", "/admin/nets/unban"},
		{"POST", "/admin/items/kill"}, {"POST", "/admin/items/restore"},
	}
	for _, r := range routes {
		for _, u := range []*sdk.HTTPUser{nil, {ID: "u2", Role: "editor"}} {
			resp := adminCall(t, r.method, r.pattern, map[string]string{"name": "bob"}, nil, `{}`, u)
			if out := wantStatus(t, resp, 403); out["error"] != "owner only" {
				t.Errorf("%s %s: %v", r.method, r.pattern, out)
			}
		}
	}
}

func TestAdminUsers(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	const ban, unban = "/admin/users/{name}/ban", "/admin/users/{name}/unban"
	bob := map[string]string{"name": "bob"}

	t.Run("ban and unban round trip", func(t *testing.T) {
		db := bobDB()
		install(t, db)
		for _, mode := range []string{"dead", "blocked"} {
			wantStatus(t, asOwner(t, "POST", ban, bob, `{"mode":"`+mode+`","reason":" spam "}`), 200)
		}
		wantStatus(t, asOwner(t, "POST", unban, bob, `{}`), 200)
		wantStatus(t, asOwner(t, "POST", unban, bob, ``), 200)
		b := execs(db, "INSERT INTO bans")
		if len(b) != 2 || b[0].Args[0] != float64(9) || b[0].Args[1] != "dead" || b[0].Args[2] != "spam" || b[1].Args[1] != "blocked" {
			t.Fatalf("bans = %+v", b)
		}
		if d := execs(db, "DELETE FROM bans"); len(d) != 2 || d[0].Args[0] != float64(9) {
			t.Fatalf("deletes = %+v", d)
		}
		logs := execs(db, sqlLogInsert)
		if len(logs) != 4 || logs[0].Args[0] != "owner" || logs[0].Args[1] != "ban" || logs[0].Args[2] != "user:bob" || logs[2].Args[1] != "unban" {
			t.Fatalf("log = %+v", logs)
		}
	})
	t.Run("rejections", func(t *testing.T) {
		db := bobDB()
		install(t, db)
		wantStatus(t, asOwner(t, "POST", ban, bob, `{"mode":"banish"}`), 400)
		wantStatus(t, asOwner(t, "POST", ban, bob, `{"mode":"dead","reason":"a\nb"}`), 400)
		wantStatus(t, asOwner(t, "POST", ban, bob, `{"mode":"dead","reason":"`+strings.Repeat("x", 201)+`"}`), 400)
		wantStatus(t, asOwner(t, "POST", ban, bob, `{"mode":"dead","bogus":1}`), 400)
		wantStatus(t, asOwner(t, "POST", ban, bob, `{"mode":"dead","pad":"`+strings.Repeat("x", 70<<10)+`"}`), 413)
		if len(db.Execs) != 0 {
			t.Fatalf("execs = %+v", db.Execs)
		}
	})
	t.Run("unknown member", func(t *testing.T) {
		install(t, &fakeDB{})
		wantStatus(t, asOwner(t, "POST", ban, bob, `{"mode":"dead"}`), 404)
		wantStatus(t, asOwner(t, "POST", unban, bob, `{}`), 404)
	})
}

func TestAdminDomains(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	db := &fakeDB{}
	install(t, db)
	out := wantStatus(t, asOwner(t, "POST", "/admin/domains", nil, `{"domain":" WWW.Example.COM ","reason":"spam"}`), 200)
	if out["domain"] != "example.com" {
		t.Fatalf("out = %v", out)
	}
	if d := execs(db, "INSERT INTO banned_domains"); len(d) != 1 || d[0].Args[0] != "example.com" {
		t.Fatalf("inserts = %+v", d)
	}
	for _, bad := range []string{"", "localhost", "a b.com", "http://x.com", "x..com", strings.Repeat("a", 250) + ".com"} {
		b, _ := json.Marshal(map[string]string{"domain": bad})
		wantStatus(t, asOwner(t, "POST", "/admin/domains", nil, string(b)), 400)
	}
	wantStatus(t, asOwner(t, "POST", "/admin/domains/remove", nil, `{"domain":"Example.com"}`), 200)
	if d := execs(db, "DELETE FROM banned_domains"); len(d) != 1 || d[0].Args[0] != "example.com" {
		t.Fatalf("deletes = %+v", d)
	}
	logs := execs(db, sqlLogInsert)
	if len(logs) != 2 || logs[0].Args[1] != "domain-ban" || logs[1].Args[1] != "domain-remove" {
		t.Fatalf("log = %+v", logs)
	}
}

func TestAdminDomainKillsAndRestoresStories(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pinNow(t, now)
	db := &fakeDB{Exec: func(sql string, _ []any) (int64, error) {
		switch sql {
		case sqlDomainKill:
			return 3, nil
		case sqlDomainRevive:
			return 2, nil
		}
		return 1, nil
	}}
	install(t, db)
	out := wantStatus(t, asOwner(t, "POST", "/admin/domains", nil, `{"domain":"Spam.example","reason":"spam"}`), 200)
	if out["killed"] != float64(3) {
		t.Fatalf("out = %v", out)
	}
	if len(db.Execs) < 2 || !strings.HasPrefix(db.Execs[0].SQL, "INSERT INTO banned_domains") || db.Execs[1].SQL != sqlDomainKill ||
		db.Execs[1].Args[0] != "spam.example" || db.Execs[1].Args[1] != now.Format(time.RFC3339Nano) {
		t.Fatalf("execs = %+v", db.Execs)
	}
	out = wantStatus(t, asOwner(t, "POST", "/admin/domains/remove", nil, `{"domain":"spam.example"}`), 200)
	if out["restored"] != float64(2) {
		t.Fatalf("out = %v", out)
	}
	if n := len(db.Execs); n < 3 || db.Execs[n-3].SQL != sqlDomainDelete || db.Execs[n-2].SQL != sqlDomainRevive || db.Execs[n-2].Args[0] != "spam.example" {
		t.Fatalf("remove order: %+v", db.Execs)
	}
	logs := execs(db, sqlLogInsert)
	if len(logs) != 2 || logs[0].Args[3] != "killed 3" || logs[1].Args[3] != "restored 2" {
		t.Fatalf("log = %+v", logs)
	}
	for _, want := range []string{"WHERE NOT dead", "'domain'", "ON CONFLICT (kind, target_id) DO NOTHING"} {
		if !strings.Contains(sqlDomainKill, want) {
			t.Errorf("kill SQL lacks %q", want)
		}
	}
	for _, want := range []string{"NOT EXISTS (SELECT 1 FROM banned_domains", "k.cause = 'domain'"} {
		if !strings.Contains(sqlDomainRevive, want) {
			t.Errorf("revive SQL lacks %q", want)
		}
	}
	// a kill that fails answers 500; a retry is safe
	fail := &fakeDB{Exec: func(sql string, _ []any) (int64, error) {
		if sql == sqlDomainKill {
			return 0, hostErr(sdk.ErrUnavailable)
		}
		return 1, nil
	}}
	install(t, fail)
	wantStatus(t, asOwner(t, "POST", "/admin/domains", nil, `{"domain":"spam.example"}`), 500)
}

func TestAdminDomainsIDN(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	db := &fakeDB{}
	install(t, db)
	out := wantStatus(t, asOwner(t, "POST", "/admin/domains", nil, `{"domain":"MÜNCHEN-qa.example","reason":"spam"}`), 200)
	if out["domain"] != "xn--mnchen-qa-q9a.example" {
		t.Fatalf("out = %v", out)
	}
	if d := execs(db, "INSERT INTO banned_domains"); len(d) != 1 || d[0].Args[0] != "xn--mnchen-qa-q9a.example" {
		t.Fatalf("inserts = %+v", d)
	}
	wantStatus(t, asOwner(t, "POST", "/admin/domains/remove", nil, `{"domain":"münchen-qa.example"}`), 200)
	if d := execs(db, "DELETE FROM banned_domains"); len(d) != 1 || d[0].Args[0] != "xn--mnchen-qa-q9a.example" {
		t.Fatalf("deletes = %+v", d)
	}
	wantStatus(t, asOwner(t, "POST", "/admin/domains", nil, "{\"domain\":\"a\\ufffdb.example\"}"), 400)
}

func TestAdminNets(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pinNow(t, now)
	db := &fakeDB{}
	install(t, db)
	const key = "AbCdEfGh12345678"
	wantStatus(t, asOwner(t, "POST", "/admin/nets/ban", nil, `{"net_key":"`+key+`","reason":"flood","days":7}`), 200)
	wantStatus(t, asOwner(t, "POST", "/admin/nets/ban", nil, `{"net_key":"`+key+`"}`), 200)
	b := execs(db, "INSERT INTO net_bans")
	if len(b) != 2 || b[0].Args[0] != key || b[0].Args[2] != now.AddDate(0, 0, 7).Format(time.RFC3339Nano) || b[1].Args[2] != nil {
		t.Fatalf("bans = %+v", b)
	}
	for _, bad := range []string{`{"net_key":"short"}`, `{"net_key":"has space 12345"}`, `{"net_key":"` + key + `","days":-1}`, `{"net_key":"` + key + `","days":366}`} {
		wantStatus(t, asOwner(t, "POST", "/admin/nets/ban", nil, bad), 400)
	}
	wantStatus(t, asOwner(t, "POST", "/admin/nets/unban", nil, `{"net_key":"`+key+`"}`), 200)
	if d := execs(db, "DELETE FROM net_bans"); len(d) != 1 || d[0].Args[0] != key {
		t.Fatalf("deletes = %+v", d)
	}
	for _, l := range execs(db, sqlLogInsert) {
		if l.Args[2] != "net:AbCdEfGh" || strings.Contains(l.Args[3].(string), key) {
			t.Errorf("log %+v", l)
		}
	}
}

func TestAdminItems(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	t.Run("kill and restore a story", func(t *testing.T) {
		db := bobDB()
		h := install(t, db)
		out := wantStatus(t, asOwner(t, "POST", "/admin/items/kill", nil, `{"kind":"story","id":"`+storyUUID+`"}`), 200)
		if out["dead"] != true {
			t.Fatalf("out = %v", out)
		}
		if k := execs(db, "INSERT INTO kills"); len(k) != 1 || !strings.Contains(k[0].SQL, "SET cause = 'owner'") || k[0].Args[0] != "story" {
			t.Fatalf("kills = %+v", k)
		}
		if d := execs(db, "UPDATE story_meta SET dead"); len(d) != 1 || d[0].Args[1] != true {
			t.Fatalf("dead update = %+v", d)
		}
		noEntityCalls(t, h)
		out = wantStatus(t, asOwner(t, "POST", "/admin/items/restore", nil, `{"kind":"story","id":"`+storyUUID+`"}`), 200)
		if out["dead"] != false || len(execs(db, "DELETE FROM kills")) != 1 {
			t.Fatalf("out %v execs %+v", out, db.Execs)
		}
		noEntityCalls(t, h)
	})
	t.Run("kill and restore a comment", func(t *testing.T) {
		db := bobDB()
		var fns []string
		db.Comments = func(fn string, ids []string) (any, error) {
			fns = append(fns, fn+":"+ids[0])
			return map[string]any{"changed": 1}, nil
		}
		h := install(t, db)
		wantStatus(t, asOwner(t, "POST", "/admin/items/kill", nil, `{"kind":"comment","id":"`+commentID+`"}`), 200)
		wantStatus(t, asOwner(t, "POST", "/admin/items/restore", nil, `{"kind":"comment","id":"`+commentID+`"}`), 200)
		if strings.Join(fns, " ") != sdk.FnCommentsMarkDead+":"+commentID+" "+sdk.FnCommentsRestore+":"+commentID {
			t.Fatalf("calls = %v", fns)
		}
		noEntityCalls(t, h)
	})
	t.Run("kill of a comment core did not move", func(t *testing.T) {
		db := bobDB()
		db.Comments = func(string, []string) (any, error) { return map[string]any{"changed": 0}, nil }
		install(t, db)
		out := wantStatus(t, asOwner(t, "POST", "/admin/items/kill", nil, `{"kind":"comment","id":"`+commentID+`"}`), 409)
		if !strings.Contains(fmt.Sprint(out), "not public") {
			t.Fatalf("out = %v", out)
		}
		if len(execs(db, "kills")) != 0 || len(execs(db, "comment_meta")) != 0 {
			t.Fatalf("execs = %+v", db.Execs)
		}
	})
	t.Run("comment host unavailable", func(t *testing.T) {
		db := bobDB()
		db.Comments = func(string, []string) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }
		h := install(t, db)
		wantStatus(t, asOwner(t, "POST", "/admin/items/kill", nil, `{"kind":"comment","id":"`+commentID+`"}`), 503)
		wantStatus(t, asOwner(t, "POST", "/admin/items/restore", nil, `{"kind":"comment","id":"`+commentID+`"}`), 503)
		if len(execs(db, "INSERT INTO kills")) != 0 || len(execs(db, "DELETE")) != 0 {
			t.Fatalf("execs = %+v", db.Execs)
		}
		noEntityCalls(t, h)
	})
	t.Run("unknown or malformed", func(t *testing.T) {
		db := bobDB()
		install(t, db)
		wantStatus(t, asOwner(t, "POST", "/admin/items/kill", nil, `{"kind":"story","id":"33333333-3333-3333-3333-333333333333"}`), 404)
		wantStatus(t, asOwner(t, "POST", "/admin/items/kill", nil, `{"kind":"story","id":"42"}`), 404)
		wantStatus(t, asOwner(t, "POST", "/admin/items/kill", nil, `{"kind":"thing","id":"`+storyUUID+`"}`), 404)
		if len(db.Execs) != 0 {
			t.Fatalf("execs = %+v", db.Execs)
		}
	})
}

func TestAdminViews(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	view := func(db *fakeDB, v string) map[string]any {
		install(t, db)
		return wantStatus(t, adminCall(t, "GET", "/admin/queue", nil, map[string][]string{"view": {v}}, "", owner), 200)
	}
	answer := func(sql string, doc any) *fakeDB {
		return &fakeDB{Query: func(got string, _ []any) (any, error) {
			if got != sql {
				t.Errorf("unexpected sql %q", got)
			}
			return jsonRow(doc), nil
		}}
	}

	rows := []map[string]any{
		{"kind": "story", "id": storyUUID, "item_no": 5, "title": "Hello", "by": "bob", "flags": 4, "dead": false, "cause": "", "body_html": ""},
		{"kind": "comment", "id": commentID, "item_no": 5, "title": "", "by": "ann", "flags": 2, "dead": true, "cause": "flags", "body_html": "<p>a &amp; <b>b</b></p>"},
	}
	out := view(answer(sqlQueue, rows), "queue")
	items, _ := out["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != storyUUID || items[1].(map[string]any)["title"] != "a & b" {
		t.Fatalf("items = %v", out)
	}
	if _, leaked := items[1].(map[string]any)["body_html"]; leaked {
		t.Error("body_html leaked")
	}
	killed := []map[string]any{{"kind": "story", "id": storyUUID, "item_no": 5, "title": "Spam", "by": "bob", "flags": 0, "dead": true, "cause": "owner", "body_html": ""}}
	if it, _ := view(answer(sqlQueue, killed), "queue")["items"].([]any); len(it) != 1 || it[0].(map[string]any)["flags"] != float64(0) || it[0].(map[string]any)["dead"] != true || it[0].(map[string]any)["cause"] != "owner" {
		t.Fatalf("kills-only row = %v", it)
	}
	if out := view(answer(sqlQueue, []any{}), ""); len(out["items"].([]any)) != 0 {
		t.Fatalf("default view = %v", out)
	}
	for v, key := range map[string]string{"users": "users", "domains": "domains"} {
		sql := map[string]string{"users": sqlUsers, "domains": sqlDomains}[v]
		if out := view(answer(sql, []any{map[string]any{"x": 1}}), v); len(out[key].([]any)) != 1 {
			t.Errorf("%s = %v", v, out)
		}
	}
	out = view(answer(sqlNets, map[string]any{"bans": []any{}, "noisy": []any{map[string]any{"net_key": "k", "events": 9}}}), "nets")
	if len(out["noisy"].([]any)) != 1 || out["bans"] == nil {
		t.Fatalf("nets = %v", out)
	}
	wantStatus(t, adminCall(t, "GET", "/admin/queue", nil, map[string][]string{"view": {"bogus"}}, "", owner), 400)

	install(t, answer(sqlLogList, []any{map[string]any{"id": 2, "action": "ban"}, map[string]any{"id": 1, "action": "kill"}}))
	log := wantStatus(t, adminCall(t, "GET", "/admin/log", nil, nil, "", owner), 200)["log"].([]any)
	if len(log) != 2 || log[0].(map[string]any)["id"] != float64(2) {
		t.Fatalf("log = %v", log)
	}
	if !strings.Contains(sqlLogList, "ORDER BY id DESC") {
		t.Error("log is not newest first")
	}
}

// The queue must rank live targets before dead ones inside the LIMIT and skip
// targets whose story or comment row is gone.
func TestAdminRecentView(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	rows := []map[string]any{
		{"kind": "story", "id": storyUUID, "item_no": 5, "title": "Hello", "by": "bob", "flags": 0, "dead": false, "cause": "", "body_html": "", "at": "2026-01-02T03:00:00Z"},
		{"kind": "comment", "id": commentID, "item_no": 5, "title": "", "by": "ann", "flags": 0, "dead": true, "cause": "owner", "body_html": "<p>hi</p>", "at": "2026-01-02T02:00:00Z"},
	}
	install(t, &fakeDB{Query: func(got string, _ []any) (any, error) {
		if got != sqlRecent {
			t.Errorf("unexpected sql %q", got)
		}
		return jsonRow(rows), nil
	}})
	out := wantStatus(t, adminCall(t, "GET", "/admin/queue", nil, map[string][]string{"view": {"recent"}}, "", owner), 200)
	items, _ := out["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["at"] != "2026-01-02T03:00:00Z" || items[1].(map[string]any)["title"] != "hi" {
		t.Fatalf("items = %v", out)
	}
	// owner only, like every admin view
	install(t, bobDB())
	wantStatus(t, adminCall(t, "GET", "/admin/queue", nil, map[string][]string{"view": {"recent"}}, "", &sdk.HTTPUser{ID: "u2", Role: "editor"}), 403)
	if strings.Count(sqlRecent, "LIMIT 50") != 2 || !strings.Contains(sqlRecent, "ORDER BY x.at DESC") {
		t.Error("recent view is not the newest 50 stories + 50 comments, newest first")
	}
}

func TestQueueSQLShape(t *testing.T) {
	inner := sqlQueue[strings.Index(sqlQueue, "FROM (SELECT f.kind"):]
	order := strings.Index(inner, "ORDER BY (k.cause IS NOT NULL OR COALESCE(s.dead, false)), f.n DESC")
	limit := strings.Index(inner, "LIMIT 100")
	if order < 0 || limit < order {
		t.Error("live targets are not ranked first inside the limited select")
	}
	if !strings.Contains(inner, "WHERE s.entity_id IS NOT NULL OR cm.comment_id IS NOT NULL") {
		t.Error("orphaned flags are not excluded")
	}
	if !strings.Contains(inner, "FROM kills k WHERE NOT EXISTS (SELECT 1 FROM flags fl") {
		t.Error("kills without flags are not in the queue source")
	}
	if !strings.Contains(sqlQueue, "ORDER BY x.dead, x.flags DESC") {
		t.Error("outer order puts dead items first")
	}
}

func TestAdminPasswordRemove(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	const route = "/admin/users/{name}/password/remove"
	bob := map[string]string{"name": "bob"}
	withLogin := func(has bool) *fakeDB {
		db := bobDB()
		inner := db.Query
		db.Query = func(sql string, args []any) (any, error) {
			if sql == sqlLoginAccount && has {
				return jsonRow(map[string]any{"account": "acc-bob"}), nil
			}
			return inner(sql, args)
		}
		return db
	}
	t.Run("revokes, deletes and logs", func(t *testing.T) {
		db := withLogin(true)
		install(t, db)
		wantStatus(t, asOwner(t, "POST", route, bob, `{}`), 200)
		if len(db.Revoked) != 2 || db.Revoked[0] != "acc-bob" || db.Revoked[1] != "acc-bob" {
			t.Fatalf("revoked %v", db.Revoked)
		}
		if d := execs(db, sqlLoginDelete); len(d) != 1 || d[0].Args[0] != float64(9) {
			t.Fatalf("deletes = %+v", d)
		}
		if logs := execs(db, sqlLogInsert); len(logs) != 1 || logs[0].Args[1] != "password-remove" || logs[0].Args[2] != "user:bob" {
			t.Fatalf("log = %+v", logs)
		}
	})
	t.Run("a failed first revoke keeps the login for a retry", func(t *testing.T) {
		db := withLogin(true)
		db.Revoke = func(string) error { return hostErr(sdk.ErrUnavailable) }
		install(t, db)
		wantStatus(t, asOwner(t, "POST", route, bob, `{}`), 500)
		if d := execs(db, sqlLoginDelete); len(d) != 0 {
			t.Fatalf("deletes = %+v", d)
		}
		db.Revoke = nil
		wantStatus(t, asOwner(t, "POST", route, bob, `{}`), 200)
		if d := execs(db, sqlLoginDelete); len(d) != 1 {
			t.Fatalf("deletes after retry = %+v", d)
		}
	})
	t.Run("a failed second revoke is reported, the login is gone and it is logged", func(t *testing.T) {
		db := withLogin(true)
		calls := 0
		db.Revoke = func(string) error {
			calls++
			if calls == 2 {
				return hostErr(sdk.ErrUnavailable)
			}
			return nil
		}
		install(t, db)
		wantStatus(t, asOwner(t, "POST", route, bob, `{}`), 500)
		if d := execs(db, sqlLoginDelete); len(d) != 1 {
			t.Fatalf("deletes = %+v", d)
		}
		if logs := execs(db, sqlLogInsert); len(logs) != 1 || !strings.Contains(logs[0].Args[3].(string), "sessions not ended") {
			t.Fatalf("log = %+v", logs)
		}
	})
	t.Run("a failed delete does not revoke again", func(t *testing.T) {
		db := withLogin(true)
		db.Exec = func(sql string, _ []any) (int64, error) {
			if sql == sqlLoginDelete {
				return 0, hostErr(sdk.ErrUnavailable)
			}
			return 1, nil
		}
		install(t, db)
		wantStatus(t, asOwner(t, "POST", route, bob, `{}`), 500)
		if len(db.Revoked) != 1 {
			t.Fatalf("revoked %v", db.Revoked)
		}
	})
	t.Run("no password, unknown member", func(t *testing.T) {
		db := withLogin(false)
		install(t, db)
		wantStatus(t, asOwner(t, "POST", route, bob, `{}`), 404)
		install(t, &fakeDB{})
		wantStatus(t, asOwner(t, "POST", route, bob, `{}`), 404)
	})
}
