package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// adminHost fakes settings and the submissions and events tables for the
// Submissions and Imports routes.
type adminHost struct {
	settings string
	rows     string // the submissions listing cell
	counts   string
	last     string // answer of the last-imported query
	affected int
	dbErr    error
	queries  []sqlCall
	execs    []sqlCall
}

func (h *adminHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		call := sqlCall{}
		call.sql, _ = a["sql"].(string)
		call.args, _ = a["args"].([]any)
		switch fn {
		case sdk.FnSettingsGet:
			s := h.settings
			if s == "" {
				s = `{}`
			}
			return json.Unmarshal([]byte(s), out)
		case sdk.FnDBQuery:
			h.queries = append(h.queries, call)
			if h.dbErr != nil {
				return h.dbErr
			}
			cell := h.rows
			switch {
			case strings.Contains(call.sql, "jsonb_object_agg"):
				cell = h.counts
			case strings.Contains(call.sql, "max(updated_at)"):
				cell = h.last
			}
			return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
		case sdk.FnDBExec:
			h.execs = append(h.execs, call)
			if h.dbErr != nil {
				return h.dbErr
			}
			return json.Unmarshal([]byte(`{"rows_affected":`+jsonText(h.affected)+`}`), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func callRoute(t *testing.T, method, pattern string, user *sdk.HTTPUser, values map[string]string, query map[string][]string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Pattern: pattern, User: user, PathValues: values, Query: query})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var r sdk.HTTPResponse
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

var (
	owner  = &sdk.HTTPUser{ID: "u1", Role: "owner"}
	editor = &sdk.HTTPUser{ID: "u2", Role: "editor"}
)

const subRows = `[{"id":7,"status":"pending","title":"Open mic","start_at":"2027-02-20T20:00:00Z","end_at":"","venue":"The Anchor","description":"Bring a song.","email":"visitor@example.org","created_at":"2027-02-01T10:00:00+00:00","notified":true}]`

func TestAdminSubmissionsList(t *testing.T) {
	h := &adminHost{rows: subRows, counts: `{"pending":1,"published":3}`}
	h.install(t)
	for _, u := range []*sdk.HTTPUser{owner, editor} {
		h.queries = nil
		r := callRoute(t, "GET", "/admin/submissions", u, nil, map[string][]string{"status": {"pending"}})
		if r.Status != 200 {
			t.Fatalf("%s: status %d", u.Role, r.Status)
		}
		var got submissionList
		if err := json.Unmarshal([]byte(r.Body), &got); err != nil {
			t.Fatal(err)
		}
		if got.Role != u.Role || len(got.Items) != 1 || got.Items[0].Email != "visitor@example.org" || got.Items[0].ID != 7 || got.Items[0].Title != "Open mic" {
			t.Errorf("%s: list = %+v", u.Role, got)
		}
		if got.Counts["pending"] != 1 || got.Counts["published"] != 3 || got.Counts["rejected"] != 0 || got.Counts["approved"] != 0 {
			t.Errorf("counts = %v", got.Counts)
		}
		if strings.Contains(r.Body, "notified") {
			t.Errorf("body exposes the mail flag: %s", r.Body)
		}
		if q := h.queries[0]; q.args[0] != "pending" {
			t.Errorf("status arg = %v", q.args)
		}
	}
}

func TestAdminSubmissionsStatusFilter(t *testing.T) {
	h := &adminHost{rows: `[]`, counts: `{}`}
	h.install(t)
	for _, status := range []string{"approved", "published", "rejected", "all"} {
		r := callRoute(t, "GET", "/admin/submissions", owner, nil, map[string][]string{"status": {status}})
		if r.Status != 200 || !strings.Contains(r.Body, `"items":[]`) {
			t.Errorf("%s: %d %s", status, r.Status, r.Body)
		}
	}
	if got := len(h.queries); got != 8 {
		t.Errorf("queries = %d", got)
	}
	// no status = pending, with the parameter
	h.queries = nil
	callRoute(t, "GET", "/admin/submissions", owner, nil, nil)
	if h.queries[0].args[0] != "pending" {
		t.Errorf("default args = %v", h.queries[0].args)
	}
	for _, bad := range []string{"x", "pending' OR 1=1", "PENDING"} {
		if r := callRoute(t, "GET", "/admin/submissions", owner, nil, map[string][]string{"status": {bad}}); r.Status != 400 {
			t.Errorf("%q: status %d", bad, r.Status)
		}
	}
	if r := callRoute(t, "GET", "/admin/submissions", nil, nil, nil); r.Status != 403 {
		t.Errorf("no user: %d", r.Status)
	}
	h.dbErr = errors.New("permission denied for table submissions")
	if r := callRoute(t, "GET", "/admin/submissions", owner, nil, nil); r.Status != 503 || strings.Contains(r.Body, "denied") {
		t.Errorf("db error: %d %s", r.Status, r.Body)
	}
}

func TestAdminSubmissionActions(t *testing.T) {
	pat := func(del bool) string {
		if del {
			return "/admin/submissions/{id}/delete"
		}
		return "/admin/submissions/{id}/reject"
	}
	id := map[string]string{"id": "7"}

	t.Run("editor may reject", func(t *testing.T) {
		h := &adminHost{affected: 1}
		h.install(t)
		r := callRoute(t, "POST", pat(false), editor, id, nil)
		if r.Status != 200 || !strings.Contains(r.Body, `"ok":true`) || len(h.execs) != 1 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		if e := h.execs[0]; e.args[0] != int64(7) || e.args[1] != "rejected" || !strings.Contains(e.sql, "status IN") {
			t.Errorf("exec = %+v", e)
		}
	})
	t.Run("editor may not delete", func(t *testing.T) {
		h := &adminHost{affected: 1}
		h.install(t)
		if r := callRoute(t, "POST", pat(true), editor, id, nil); r.Status != 403 || len(h.execs) != 0 {
			t.Errorf("%d, execs %v", r.Status, h.execs)
		}
	})
	t.Run("owner deletes", func(t *testing.T) {
		h := &adminHost{affected: 1}
		h.install(t)
		r := callRoute(t, "POST", pat(true), owner, id, nil)
		if r.Status != 200 || len(h.execs) != 1 || !strings.HasPrefix(h.execs[0].sql, "DELETE FROM submissions") {
			t.Errorf("%d %s %v", r.Status, r.Body, h.execs)
		}
	})
	t.Run("no user", func(t *testing.T) {
		h := &adminHost{affected: 1}
		h.install(t)
		for _, del := range []bool{false, true} {
			if r := callRoute(t, "POST", pat(del), nil, id, nil); r.Status != 403 {
				t.Errorf("del=%v: %d", del, r.Status)
			}
		}
	})
	t.Run("bad ids", func(t *testing.T) {
		h := &adminHost{affected: 1}
		h.install(t)
		for _, bad := range []string{"", "x", "7 OR 1=1", "-1", "1.5", "99999999999999999999"} {
			for _, del := range []bool{false, true} {
				if r := callRoute(t, "POST", pat(del), owner, map[string]string{"id": bad}, nil); r.Status != 400 {
					t.Errorf("%q del=%v: %d", bad, del, r.Status)
				}
			}
		}
		if len(h.execs) != 0 {
			t.Errorf("execs = %v", h.execs)
		}
	})
	t.Run("nothing changed", func(t *testing.T) {
		h := &adminHost{affected: 0}
		h.install(t)
		for _, del := range []bool{false, true} {
			if r := callRoute(t, "POST", pat(del), owner, id, nil); r.Status != 404 {
				t.Errorf("del=%v: %d", del, r.Status)
			}
		}
	})
	t.Run("db error", func(t *testing.T) {
		h := &adminHost{dbErr: errors.New("boom at host example.com")}
		h.install(t)
		r := callRoute(t, "POST", pat(true), owner, id, nil)
		if r.Status != 503 || strings.Contains(r.Body, "boom") {
			t.Errorf("%d %s", r.Status, r.Body)
		}
	})
}

const importSettings = `{"import_sources":[
{"name":"Town council","kind":"google","url":"https://calendar.google.com/calendar/ical/secret-token/basic.ics","category":"Civic","enabled":true},
{"name":"Riverside venue","kind":"outlook","url":"webcal://outlook.office365.com/owa/calendar/x/reachcalendar.ics","enabled":false},
{"kind":"meetup","url":"https://www.meetup.com/example-group/events/ical/"}]}`

func TestAdminImports(t *testing.T) {
	h := &adminHost{settings: importSettings, last: `"2027-02-01T10:00:00+00:00"`}
	h.install(t)

	r := callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	if r.Status != 200 {
		t.Fatalf("status %d", r.Status)
	}
	var got importList
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 3 {
		t.Fatalf("list = %+v", got)
	}
	s0, s1, s2 := got.Sources[0], got.Sources[1], got.Sources[2]
	if s0.Name != "Town council" || s0.Host != "calendar.google.com" || s0.Category != "Civic" || !s0.Enabled || s0.URL == "" {
		t.Errorf("source 0 = %+v", s0)
	}
	if s1.Enabled || !strings.HasPrefix(s1.URL, "https://outlook.office365.com/") {
		t.Errorf("source 1 = %+v", s1)
	}
	if s2.Name != "www.meetup.com" || !s2.Enabled {
		t.Errorf("source 2 = %+v", s2)
	}
	if s0.LastImported == nil || *s0.LastImported != "2027-02-01T10:00:00+00:00" {
		t.Errorf("last imported = %v", s0.LastImported)
	}

	// the last-imported query: parameters only, the key prefix of the source
	if len(h.queries) != 3 {
		t.Fatalf("queries = %v", h.queries)
	}
	q := h.queries[0]
	if q.args[0] != "google-town-council-" || q.args[1] != "google" || strings.Contains(q.sql, "town") || strings.Contains(q.sql, "google") {
		t.Errorf("query = %+v", q)
	}
	if h.queries[1].args[0] != "outlook-riverside-venue-" {
		t.Errorf("args = %v", h.queries[1].args)
	}
	if want := importKey("google", "Town council", "uid-1"); !strings.HasPrefix(want, q.args[0].(string)) {
		t.Errorf("prefix %v does not match key %s", q.args[0], want)
	}

	// an editor never gets an address
	r = callRoute(t, "GET", "/admin/imports", editor, nil, nil)
	if r.Status != 200 || strings.Contains(r.Body, "secret-token") || strings.Contains(r.Body, "reachcalendar") || strings.Contains(r.Body, `"url"`) {
		t.Errorf("editor body: %s", r.Body)
	}
	var ed importList
	_ = json.Unmarshal([]byte(r.Body), &ed)
	if ed.Role != "editor" || len(ed.Sources) != 3 || ed.Sources[0].Host != "calendar.google.com" {
		t.Errorf("editor list = %+v", ed)
	}
	if r := callRoute(t, "GET", "/admin/imports", nil, nil, nil); r.Status != 403 {
		t.Errorf("no user: %d", r.Status)
	}
}

func TestAdminImportsNoDatabase(t *testing.T) {
	h := &adminHost{settings: importSettings, dbErr: errors.New("no db")}
	h.install(t)
	r := callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	var got importList
	if r.Status != 200 || json.Unmarshal([]byte(r.Body), &got) != nil || len(got.Sources) != 3 || got.Sources[0].LastImported != nil {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	h.settings = `{}`
	if r := callRoute(t, "GET", "/admin/imports", owner, nil, nil); !strings.Contains(r.Body, `"sources":[]`) {
		t.Errorf("empty: %s", r.Body)
	}
}

func TestAdminRejectAll(t *testing.T) {
	const pat = "/admin/submissions/reject-all"
	t.Run("owner", func(t *testing.T) {
		h := &adminHost{affected: 3}
		h.install(t)
		r := callRoute(t, "POST", pat, owner, nil, nil)
		if r.Status != 200 || !strings.Contains(r.Body, `"rejected":3`) || len(h.execs) != 1 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		if sql := h.execs[0].sql; !strings.Contains(sql, "'rejected'") || !strings.Contains(sql, "status = 'pending'") {
			t.Errorf("sql = %s", sql)
		}
	})
	t.Run("editor and anonymous are refused", func(t *testing.T) {
		h := &adminHost{affected: 3}
		h.install(t)
		for _, u := range []*sdk.HTTPUser{editor, nil} {
			if r := callRoute(t, "POST", pat, u, nil, nil); r.Status != 403 {
				t.Errorf("user %v: %d", u, r.Status)
			}
		}
		if len(h.execs) != 0 {
			t.Errorf("execs = %v", h.execs)
		}
	})
	t.Run("db error", func(t *testing.T) {
		h := &adminHost{dbErr: errors.New("boom")}
		h.install(t)
		if r := callRoute(t, "POST", pat, owner, nil, nil); r.Status != 503 || strings.Contains(r.Body, "boom") {
			t.Errorf("%d %s", r.Status, r.Body)
		}
	})
}

func TestAdminSubmissionsReportsEnabled(t *testing.T) {
	for setting, want := range map[string]string{`{}`: `"enabled":false`, `{"submissions_enabled":true}`: `"enabled":true`} {
		h := &adminHost{settings: setting, rows: `[]`, counts: `{}`}
		h.install(t)
		if r := callRoute(t, "GET", "/admin/submissions", owner, nil, nil); !strings.Contains(r.Body, want) {
			t.Errorf("%s: %s", setting, r.Body)
		}
	}
}
