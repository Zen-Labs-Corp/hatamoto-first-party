package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func adminGet(q url.Values) sdk.HTTPRequest {
	return sdk.HTTPRequest{Method: "GET", Path: "/admin/accounts", Pattern: "/admin/accounts", Query: q, User: &sdk.HTTPUser{ID: "u", Role: "owner"}}
}

func adminPost(id, action string) sdk.HTTPRequest {
	return sdk.HTTPRequest{
		Method: "POST", Path: "/admin/accounts/" + id + "/" + action, Pattern: "/admin/accounts/{id}/" + action,
		PathValues: map[string]string{"id": id}, User: &sdk.HTTPUser{ID: "u", Role: "owner"},
	}
}

const adminDoc = `{"total":51,"rows":[{"id":3,"login":"acme","display_name":"Acme Ltd","email":"dev@acme.io","plan":"free","status":"active","created_at":"2026-11-09T10:00:00+00:00","key_id":"3f9a1c2b7d4e5f60"},` +
	`{"id":2,"login":"nokey","display_name":"No Key","email":"n@k.io","plan":"free","status":"suspended","created_at":"2026-11-08T10:00:00+00:00","key_id":null}]}`

func TestAdminListEscapesTheSearchAndOffsets(t *testing.T) {
	f := &fakeHost{db: func(_ hostCall, out any) error { return cell(out, adminDoc) }}
	f.install(t)
	resp := send(t, adminGet(url.Values{"q": {` 50%_\Acme `}, "page": {"3"}}))
	if resp.Status != 200 {
		t.Fatalf("%+v", resp)
	}
	requireGuard(t, f)
	c := f.sqlCalls()[0]
	if c.args[0] != `50%_\Acme` || c.args[1] != `%50\%\_\\acme%` || c.args[2] != float64(100) && c.args[2] != 100 {
		t.Errorf("args %#v", c.args)
	}
	if strings.Contains(c.sql, "50%") || !strings.Contains(c.sql, "LIMIT 50 OFFSET $3") || !strings.Contains(c.sql, `ESCAPE '\'`) {
		t.Errorf("sql %s", c.sql)
	}
	var got struct {
		Total    int        `json:"total"`
		Rows     []adminRow `json:"rows"`
		Page     int        `json:"page"`
		PageSize int        `json:"page_size"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 51 || got.Page != 3 || got.PageSize != 50 || len(got.Rows) != 2 || got.Rows[0].KeyID == nil || got.Rows[1].KeyID != nil {
		t.Errorf("%+v", got)
	}
}

func TestAdminListDefaultsAndEmpty(t *testing.T) {
	f := &fakeHost{db: func(_ hostCall, out any) error { return cell(out, `{"total":0,"rows":[]}`) }}
	f.install(t)
	for _, page := range []string{"", "0", "-4", "abc"} {
		resp := send(t, adminGet(url.Values{"page": {page}}))
		if !strings.Contains(resp.Body, `"page":1`) || !strings.Contains(resp.Body, `"rows":[]`) {
			t.Errorf("page %q: %s", page, resp.Body)
		}
	}
	if c := f.sqlCalls()[0]; c.args[0] != "" || c.args[1] != "%%" || c.args[2] != 0 {
		t.Errorf("args %#v", c.args)
	}
	(&fakeHost{db: func(hostCall, any) error { return errDB }}).install(t)
	if resp := send(t, adminGet(nil)); resp.Status != http.StatusServiceUnavailable {
		t.Errorf("db error: %d", resp.Status)
	}
}

func TestSuspendUpdatesThenEndsTheSessions(t *testing.T) {
	f := &fakeHost{}
	f.install(t)
	resp := send(t, adminPost("7", "suspend"))
	if resp.Status != 200 {
		t.Fatalf("%+v", resp)
	}
	requireGuard(t, f)
	calls := f.sqlCalls()
	if len(calls) != 2 ||
		!strings.HasPrefix(calls[0].sql, "UPDATE accounts SET status") || calls[0].args[0] != int64(7) || calls[0].args[1] != "suspended" ||
		!strings.HasPrefix(calls[1].sql, "DELETE FROM sessions WHERE account") || calls[1].args[0] != int64(7) {
		t.Fatalf("calls %+v", calls)
	}
}

func TestUnsuspendOnlyChangesTheStatus(t *testing.T) {
	f := &fakeHost{}
	f.install(t)
	if resp := send(t, adminPost("7", "unsuspend")); resp.Status != 200 {
		t.Fatalf("%+v", resp)
	}
	calls := f.sqlCalls()
	if len(calls) != 1 || calls[0].args[1] != "active" {
		t.Fatalf("calls %+v", calls)
	}
}

func TestAdminStatusRefusals(t *testing.T) {
	f := &fakeHost{}
	f.install(t)
	for _, id := range []string{"", "abc", "0", "-1", "1; DROP TABLE accounts", "99999999999999999999"} {
		if resp := send(t, adminPost(id, "suspend")); resp.Status != http.StatusBadRequest {
			t.Errorf("id %q: %d", id, resp.Status)
		}
	}
	if len(f.sqlCalls()) != 0 {
		t.Error("an invalid id reached the database")
	}
	(&fakeHost{db: func(_ hostCall, out any) error { return affected(out, 0) }}).install(t)
	if resp := send(t, adminPost("5", "suspend")); resp.Status != http.StatusNotFound {
		t.Errorf("unknown account: %d", resp.Status)
	}
}

func TestAdminRoutesAreNotPublic(t *testing.T) {
	n := 0
	for _, r := range loadManifest(t).Routes {
		if strings.HasPrefix(r.Path, "/admin/") {
			n++
			if r.Public || len(r.Roles) != 0 {
				t.Errorf("route %s %s: public=%v roles=%v", r.Method, r.Path, r.Public, r.Roles)
			}
		}
	}
	if n != 8 {
		t.Errorf("%d admin routes", n)
	}
}
