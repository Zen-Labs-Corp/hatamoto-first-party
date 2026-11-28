package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// adminHost fakes settings and the submissions and events tables for the
// Submissions and Imports routes.
type adminHost struct {
	settings    string
	settingsErr error  // the settings read fails
	rows        string // the submissions listing cell
	counts      string
	last        string // answer of the per-source stats query
	progress    string // answer of the stored-counts query, "{}" when empty
	drafts      string // the drafts of the default type, a JSON array of entities
	fetch       map[string]fakePage
	fetched     []string
	fetchMs     []any     // timeout_ms of each fetch
	seeded      []subSeed // when set, the submissions page is cut from these, as the statement does
	affected    int
	dbErr       error
	queries     []sqlCall
	execs       []sqlCall
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
			if h.settingsErr != nil {
				return h.settingsErr
			}
			s := h.settings
			if s == "" {
				s = `{}`
			}
			return json.Unmarshal([]byte(s), out)
		case sdk.FnEntitiesList:
			items := "[]"
			if a["schema_slug"] == defaultNewSlug && h.drafts != "" {
				items = h.drafts
			}
			return json.Unmarshal([]byte(`{"items":`+items+`,"next_cursor":""}`), out)
		case sdk.FnHTTPFetch:
			u, _ := a["url"].(string)
			h.fetched = append(h.fetched, u)
			h.fetchMs = append(h.fetchMs, a["timeout_ms"])
			p, ok := h.fetch[u]
			if !ok {
				return &sdk.HostError{Code: sdk.ErrBlocked}
			}
			if p.err != nil {
				return p.err
			}
			return json.Unmarshal([]byte(jsonText(map[string]any{"status": p.status, "body": p.body, "truncated": p.truncated})), out)
		case sdk.FnDBQuery:
			h.queries = append(h.queries, call)
			if h.dbErr != nil {
				return h.dbErr
			}
			cell := h.rows
			switch {
			case strings.Contains(call.sql, "'total'"):
				cell = h.page(call)
			case strings.Contains(call.sql, "FROM import_progress"):
				cell = h.progress
				if cell == "" {
					cell = `{}`
				}
			case strings.Contains(call.sql, "jsonb_object_agg"):
				cell = h.counts
			case strings.Contains(call.sql, "max(first_seen)"):
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

// subSeed is one row of the seeded submissions table.
type subSeed struct {
	ID                          int64
	Status, Title, Email, Venue string
}

// page answers submissionsPageSQL: args are status, q, limit, offset. Without
// seeded rows it wraps the rows cell.
func (h *adminHost) page(c sqlCall) string {
	if h.seeded == nil {
		var n []json.RawMessage
		_ = json.Unmarshal([]byte(h.rows), &n)
		return `{"total":` + jsonText(len(n)) + `,"items":` + h.rows + `}`
	}
	status, q := c.args[0].(string), strings.ToLower(c.args[1].(string))
	limit, offset := c.args[2].(int), c.args[3].(int)
	var f []map[string]any
	for _, r := range h.seeded {
		if (status == "all" || r.Status == status) && (q == "" || strings.Contains(strings.ToLower(r.Title+" "+r.Email+" "+r.Venue), q)) {
			f = append(f, map[string]any{"id": r.ID, "status": r.Status, "title": r.Title, "email": r.Email, "venue": r.Venue,
				"start_at": "2027-02-20T20:00:00Z", "end_at": "", "description": "", "created_at": "2027-02-01T10:00:00+00:00", "notified": true})
		}
	}
	items := []map[string]any{}
	if offset < len(f) {
		items = f[offset:min(offset+limit, len(f))]
	}
	b, _ := json.Marshal(map[string]any{"total": len(f), "items": items})
	return string(b)
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

// Core's bridge sends a non-string query value as its JSON text, so a status
// array reaches the route as `["pending"]`.
func TestAdminSubmissionsStatusFromBridgeArray(t *testing.T) {
	h := &adminHost{rows: `[]`, counts: `{}`}
	h.install(t)
	for _, v := range []string{`["pending"]`, `["rejected","all"]`} {
		r := callRoute(t, "GET", "/admin/submissions", owner, nil, map[string][]string{"status": {v}})
		if r.Status != 200 {
			t.Errorf("%s: status %d", v, r.Status)
		}
	}
	if h.queries[0].args[0] != "pending" || h.queries[2].args[0] != "rejected" {
		t.Errorf("args = %v, %v", h.queries[0].args, h.queries[2].args)
	}
	if r := callRoute(t, "GET", "/admin/submissions", owner, nil, map[string][]string{"status": {`["x"]`}}); r.Status != 400 {
		t.Errorf("bad element: status %d", r.Status)
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
{"name":"Town council","kind":"google","url":"https://calendar.google.com/calendar/ical/secret-token/basic.ics","category":"Civic","enabled":true,"schedule":"daily"},
{"name":"Riverside venue","kind":"outlook","url":"webcal://outlook.office365.com/owa/calendar/x/reachcalendar.ics","enabled":false},
{"kind":"meetup","url":"https://www.meetup.com/example-group/events/ical/"},
{"name":"Club","kind":"ical","url":"https://events.example.org/feed.ics","schedule":"daily"}]}`

func TestAdminImports(t *testing.T) {
	h := &adminHost{settings: importSettings, last: `{"last":"2027-02-01T10:00:00+00:00","last_new":"2027-01-30T08:00:00+00:00","today":12}`}
	h.install(t)

	r := callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	if r.Status != 200 {
		t.Fatalf("status %d", r.Status)
	}
	var got importList
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 4 || got.DailyCap != 200 || got.RunCap != 100 {
		t.Fatalf("list = %+v", got)
	}
	s0, s1, s2, s3 := got.Sources[0], got.Sources[1], got.Sources[2], got.Sources[3]
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
	if s0.LastNew == nil || *s0.LastNew != "2027-01-30T08:00:00+00:00" || s0.CreatedToday != 12 {
		t.Errorf("last new %v, created today %d", s0.LastNew, s0.CreatedToday)
	}
	// schedule and whether a scheduled run can read the source
	if s0.Schedule != "daily" || !s0.ScheduledOK || s1.Schedule != "off" || !s1.ScheduledOK || s2.Schedule != "off" {
		t.Errorf("schedules: %+v %+v %+v", s0, s1, s2)
	}
	if s3.Schedule != "daily" || s3.ScheduledOK || s3.Host != "events.example.org" {
		t.Errorf("unlisted host = %+v", s3)
	}

	// the stats query per source (parameters only, the key prefix of the source), then one read of the stored counts
	if len(h.queries) != 5 {
		t.Fatalf("queries = %v", h.queries)
	}
	q := h.queries[0]
	if q.args[0] != "google-town-council-" || q.args[1] != "google" || strings.Contains(q.sql, "town") || strings.Contains(q.sql, "google") || !strings.Contains(q.sql, "first_seen") {
		t.Errorf("query = %+v", q)
	}
	if h.queries[1].args[0] != "outlook-riverside-venue-" {
		t.Errorf("args = %v", h.queries[1].args)
	}
	if want := importKey("google", "Town council", "uid-1"); !strings.HasPrefix(want, q.args[0].(string)) {
		t.Errorf("prefix %v does not match key %s", q.args[0], want)
	}

	// the tab is the owner's: an editor is refused with a JSON body, and nothing is read or written
	h.queries, h.execs = nil, nil
	r = callRoute(t, "GET", "/admin/imports", editor, nil, nil)
	if r.Status != 403 || r.Body != `{"ok":false,"error":"not allowed"}` || strings.Contains(r.Body, "secret-token") || len(h.queries)+len(h.execs) != 0 {
		t.Errorf("editor: %d %s, %d queries, %d execs", r.Status, r.Body, len(h.queries), len(h.execs))
	}
	if r := callRoute(t, "GET", "/admin/imports", nil, nil, nil); r.Status != 403 {
		t.Errorf("no user: %d", r.Status)
	}
}

func TestAdminImportsNeverShowTheEventbriteToken(t *testing.T) {
	h := &adminHost{settings: `{"eventbrite_token":"SECRET-TOKEN-123","import_sources":[{"name":"Tickets","kind":"eventbrite","organization_id":"123","schedule":"daily"}]}`}
	h.install(t)
	for _, u := range []*sdk.HTTPUser{owner, editor} {
		r := callRoute(t, "GET", "/admin/imports", u, nil, nil)
		if strings.Contains(r.Body, "SECRET-TOKEN-123") {
			t.Errorf("%s: the token is in the body: %s", u.Role, r.Body)
		}
		if u == editor {
			continue
		}
		if r.Status != 200 || !strings.Contains(r.Body, `"Tickets"`) {
			t.Errorf("%s: %d %s", u.Role, r.Status, r.Body)
		}
		// a token and an organization id make the source schedulable
		if !strings.Contains(r.Body, `"scheduled_ok":true`) {
			t.Errorf("%s: eventbrite source is not schedulable: %s", u.Role, r.Body)
		}
	}
	h.settings = `{"import_sources":[{"name":"Tickets","kind":"eventbrite","organization_id":"123","schedule":"daily"}]}`
	if r := callRoute(t, "GET", "/admin/imports", owner, nil, nil); !strings.Contains(r.Body, `"scheduled_ok":false`) {
		t.Errorf("no token: %s", r.Body)
	}
}

func TestAdminImportsNoDatabase(t *testing.T) {
	h := &adminHost{settings: importSettings, dbErr: errors.New("no db")}
	h.install(t)
	r := callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	var got importList
	if r.Status != 200 || json.Unmarshal([]byte(r.Body), &got) != nil || len(got.Sources) != 4 || got.Sources[0].LastImported != nil || got.Sources[0].LastNew != nil || got.Sources[0].CreatedToday != 0 {
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

// seedSubmissions makes 143 rows in creation order: 20 pending, 101 approved
// and 22 rejected.
func seedSubmissions() []subSeed {
	var out []subSeed
	for i := 1; i <= 143; i++ {
		r := subSeed{ID: int64(i), Status: "rejected", Title: fmt.Sprintf("Show %03d", i), Email: fmt.Sprintf("user%d@example.org", i), Venue: "The Anchor"}
		switch {
		case i <= 20:
			r.Status = "pending"
		case i <= 121:
			r.Status = "approved"
		}
		if i%11 == 0 {
			r.Venue = "Barn"
		}
		out = append(out, r)
	}
	return out
}

func TestAdminSubmissionsPaging(t *testing.T) {
	h := &adminHost{seeded: seedSubmissions(), counts: `{"pending":20,"approved":101,"rejected":22}`}
	h.install(t)
	read := func(u *sdk.HTTPUser, q map[string][]string) submissionList {
		t.Helper()
		r := callRoute(t, "GET", "/admin/submissions", u, nil, q)
		var got submissionList
		if r.Status != 200 || json.Unmarshal([]byte(r.Body), &got) != nil {
			t.Fatalf("status %d: %s", r.Status, r.Body)
		}
		return got
	}
	all := read(owner, map[string][]string{"status": {"all"}})
	if all.Total != 143 || len(all.Items) != 50 || all.Page != 1 || all.PerPage != 50 || all.Counts["all"] != 143 || all.Items[0].ID != 1 {
		t.Errorf("all page 1: total %d items %d counts %v", all.Total, len(all.Items), all.Counts)
	}
	if p3 := read(editor, map[string][]string{"status": {"all"}, "page": {"3"}}); len(p3.Items) != 43 || p3.Items[0].ID != 101 || p3.Role != "editor" {
		t.Errorf("all page 3 = %d items", len(p3.Items))
	}
	// the 101st approved submission is the only one on page 3
	if p3 := read(owner, map[string][]string{"status": {"approved"}, "page": {"3"}}); p3.Total != 101 || len(p3.Items) != 1 || p3.Items[0].ID != 121 {
		t.Errorf("approved page 3 = %+v", p3.Items)
	}
	if def := read(owner, nil); def.Total != 20 || len(def.Items) != 20 || def.Items[0].Status != "pending" {
		t.Errorf("default is pending: total %d", def.Total)
	}
	if q := read(owner, map[string][]string{"status": {"all"}, "q": {"barn"}}); q.Total != 13 {
		t.Errorf("search by venue: total %d", q.Total)
	}
	if q := read(owner, map[string][]string{"status": {"approved"}, "q": {"USER55@"}}); q.Total != 1 || q.Items[0].Email != "user55@example.org" {
		t.Errorf("search by email: %+v", q.Items)
	}
	if q := read(owner, map[string][]string{"q": {"nothing like it"}}); q.Total != 0 || q.Items == nil || len(q.Items) != 0 {
		t.Errorf("no match: %+v", q)
	}
	// a status the page does not offer still works for older pages
	if p := read(owner, map[string][]string{"status": {"published"}}); p.Total != 0 {
		t.Errorf("published: total %d", p.Total)
	}
	h.queries = nil
	read(owner, map[string][]string{"status": {"approved"}, "page": {"3"}, "q": {"50%_"}})
	if a := h.queries[0].args; a[0] != "approved" || a[1] != `50\%\_` || a[2] != 50 || a[3] != 100 {
		t.Errorf("args = %v", a)
	}
	for _, want := range []string{"LIMIT $3", "OFFSET $4", "ILIKE", "ORDER BY created_at, id"} {
		if !strings.Contains(h.queries[0].sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
}

func TestAdminSubmissionsBadRequest(t *testing.T) {
	h := &adminHost{seeded: seedSubmissions(), counts: `{}`}
	h.install(t)
	for name, q := range map[string]map[string][]string{
		"page zero": {"page": {"0"}}, "page text": {"page": {"1; DROP"}}, "page too big": {"page": {"10001"}},
		"long search": {"q": {strings.Repeat("a", 201)}},
	} {
		if r := callRoute(t, "GET", "/admin/submissions", owner, nil, q); r.Status != 400 || r.Body != `{"error":"bad request"}` {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := callRoute(t, "GET", "/admin/submissions", owner, nil, map[string][]string{"status": {"bogus"}}); r.Status != 400 {
		t.Errorf("bogus status: %d", r.Status)
	}
	if len(h.queries) != 0 {
		t.Errorf("a bad request ran %d statements", len(h.queries))
	}
}
