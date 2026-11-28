package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func callAdmin(t *testing.T, method string, user *sdk.HTTPUser) sdk.HTTPResponse {
	t.Helper()
	return callAdminQ(t, method, user, nil)
}

func callAdminQ(t *testing.T, method string, user *sdk.HTTPUser, query map[string][]string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Pattern: "/admin/repeating", User: user, Query: query})
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

const (
	yogaEntity = `{"id":"y1","schema_slug":"calendar-event","slug":"yoga","name":"Yoga in the park","status":"published","attributes":{
"start_at":"2026-06-02T18:00","end_at":"2026-06-02T19:00","repeat":"weekly","repeat_weekdays":["tu"],
"repeat_skip_dates":["2026-06-09"],"repeat_overrides":["2026-06-16|2026-06-17T19:00"],"source_uid":"dup-r1/yoga"}}`
	bookEntity = `{"id":"b1","schema_slug":"calendar-event","slug":"book-club","name":"Book club","status":"published","attributes":{
"start_at":"2026-06-11T18:30","repeat":"monthly","repeat_by":"nth_weekday","repeat_nth":2,"repeat_count":12}}`
)

func TestRepeatingList(t *testing.T) {
	h := &fakeHost{entities: []string{yogaEntity, bookEntity},
		dbRows: `{"total":3,"items":[{"id":"y1","slug":"yoga","name":"Yoga in the park","source_uid":"dup-r1/yoga","uid_count":1},{"id":"b1","slug":"book-club","name":"Book club","source_uid":"dup-r1/yoga","uid_count":1},{"id":"gone","slug":"gone","name":"Gone"}]}`}
	h.install(t)

	r := callAdmin(t, "GET", &sdk.HTTPUser{ID: "u", Role: "editor"})
	if r.Status != 200 || !strings.HasPrefix(r.Headers["Content-Type"], "application/json") {
		t.Fatalf("status %d headers %v", r.Status, r.Headers)
	}
	var got adminList
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Ready || got.Role != "editor" || len(got.Events) != 3 || len(got.Items) != 3 || got.Total != 3 || got.Page != 1 || got.PerPage != 20 {
		t.Fatalf("list = %+v", got)
	}
	yoga, book, gone := got.Events[0], got.Events[1], got.Events[2]
	if yoga.Rule != "Weekly, Tue, ends never" || !yoga.Owned || yoga.Slug != "yoga" {
		t.Errorf("yoga = %+v", yoga)
	}
	if book.Rule != "Monthly, 2nd Thu, 12x" || book.Owned {
		t.Errorf("book = %+v", book)
	}
	if len(gone.Dates) != 0 || gone.Dates == nil {
		t.Errorf("an event that cannot be read keeps an empty date list: %+v", gone)
	}
	if len(yoga.Dates) != adminDates {
		t.Fatalf("yoga dates = %d", len(yoga.Dates))
	}
	want := []adminDate{
		{Day: "2026-06-02", Date: "2026-06-02", Start: "18:00", End: "19:00", State: "normal"},
		{Day: "2026-06-09", Date: "2026-06-09", Start: "18:00", End: "19:00", State: "skipped"},
		{Day: "2026-06-16", Date: "2026-06-17", Start: "19:00", End: "20:00", State: "changed"},
		{Day: "2026-06-23", Date: "2026-06-23", Start: "18:00", End: "19:00", State: "normal"},
	}
	for i, w := range want {
		if yoga.Dates[i] != w {
			t.Errorf("date %d = %+v, want %+v", i, yoga.Dates[i], w)
		}
	}
	if book.Dates[0].Date != "2026-06-11" || book.Dates[0].End != "" {
		t.Errorf("book first date = %+v", book.Dates[0])
	}
}

// A dup- key that two events carry (core's Duplicate copies source_uid) owns neither.
func TestRepeatingListSharedKeyNotOwned(t *testing.T) {
	h := &fakeHost{entities: []string{yogaEntity},
		dbRows: `{"total":2,"items":[{"id":"y1","slug":"yoga","name":"Yoga","source_uid":"dup-r1/yoga","uid_count":2},{"id":"y2","slug":"yoga-2","name":"Yoga 2","source_uid":"dup-r1/yoga"}]}`}
	h.install(t)
	r := callAdmin(t, "GET", &sdk.HTTPUser{ID: "u", Role: "owner"})
	var got adminList
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 2 || got.Events[0].Owned || got.Events[1].Owned {
		t.Errorf("events = %+v", got.Events)
	}
}

func TestRepeatingListEdges(t *testing.T) {
	h := &fakeHost{dbRows: `null`}
	h.install(t)
	var got adminList
	r := callAdmin(t, "GET", &sdk.HTTPUser{Role: "owner"})
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil || got.Ready || got.Events == nil || len(got.Events) != 0 {
		t.Errorf("index not ready: %s (%v)", r.Body, err)
	}
	if r := callAdmin(t, "GET", nil); r.Status != 403 {
		t.Errorf("no user: status %d", r.Status)
	}
	if r := callAdmin(t, "POST", &sdk.HTTPUser{Role: "owner"}); r.Status != 404 {
		t.Errorf("POST: status %d", r.Status)
	}
	(&fakeHost{}).install(t) // no db
	if r := callAdmin(t, "GET", &sdk.HTTPUser{Role: "owner"}); r.Status != 503 || strings.Contains(r.Body, "no db") {
		t.Errorf("db failure: %d %q", r.Status, r.Body)
	}
}

// pagedRepeating fakes the index with n repeating events named "Event 000".. in
// name order; entities cannot be read, so the events come without dates.
func pagedRepeating(t *testing.T, n int) *[]sqlCall {
	t.Helper()
	var calls []sqlCall
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		switch fn {
		case sdk.FnSettingsGet:
			return json.Unmarshal([]byte(`{}`), out)
		case sdk.FnEntitiesGet:
			return &sdk.HostError{Code: sdk.ErrNotFound}
		case sdk.FnDBQuery:
			list, _ := a["args"].([]any)
			calls = append(calls, sqlCall{sql: a["sql"].(string), args: list})
			limit, q, offset := list[0].(int), list[1].(string), list[2].(int)
			var names []string
			for i := 1; i <= n; i++ {
				if name := fmt.Sprintf("Event %03d", i); strings.Contains(strings.ToLower(name), strings.ToLower(q)) {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			items := []map[string]any{}
			for i := offset; i < min(offset+limit, len(names)); i++ {
				id := strings.ReplaceAll(strings.ToLower(names[i]), " ", "-")
				items = append(items, map[string]any{"id": id, "slug": id, "name": names[i], "source_uid": "", "uid_count": 1})
			}
			b, _ := json.Marshal(map[string]any{"total": len(names), "items": items})
			return json.Unmarshal([]byte(`{"rows":[[`+string(b)+`]]}`), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
	return &calls
}

func TestRepeatingPaging(t *testing.T) {
	calls := pagedRepeating(t, 55)
	read := func(u *sdk.HTTPUser, q map[string][]string) adminList {
		t.Helper()
		r := callAdminQ(t, "GET", u, q)
		var got adminList
		if r.Status != 200 || json.Unmarshal([]byte(r.Body), &got) != nil {
			t.Fatalf("status %d: %s", r.Status, r.Body)
		}
		return got
	}
	p1 := read(owner, nil)
	if len(p1.Items) != 20 || p1.Total != 55 || p1.Page != 1 || p1.PerPage != 20 || p1.Items[0].Name != "Event 001" {
		t.Errorf("page 1 = %d items, total %d, first %s", len(p1.Items), p1.Total, p1.Items[0].Name)
	}
	p3 := read(editor, map[string][]string{"page": {"3"}})
	if len(p3.Items) != 15 || p3.Items[0].Name != "Event 041" || p3.Items[0].Dates == nil || p3.Role != "editor" {
		t.Errorf("page 3 = %d items, first %+v", len(p3.Items), p3.Items[0])
	}
	if got := (*calls)[len(*calls)-1].args; got[0] != 20 || got[2] != 40 {
		t.Errorf("page 3 args = %v", got)
	}
	if q := read(owner, map[string][]string{"q": {"event 05"}}); q.Total != 6 || len(q.Items) != 6 {
		t.Errorf("search: total %d, items %d", q.Total, len(q.Items))
	}
	if q := read(owner, map[string][]string{"q": {"%"}}); q.Total != 0 || (*calls)[len(*calls)-1].args[1] != `\%` {
		t.Errorf("a literal percent: total %d, arg %v", q.Total, (*calls)[len(*calls)-1].args[1])
	}
	if e := read(owner, map[string][]string{"page": {"9"}}); len(e.Items) != 0 || e.Total != 55 || e.Items == nil {
		t.Errorf("a page past the end: %+v", e)
	}
	sql := (*calls)[0].sql
	for _, want := range []string{"LIMIT $1", "OFFSET $3", "ILIKE", "'total'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
}

func TestRepeatingBadRequest(t *testing.T) {
	calls := pagedRepeating(t, 5)
	for name, q := range map[string]map[string][]string{
		"page zero": {"page": {"0"}}, "page text": {"page": {"x"}}, "page too big": {"page": {"10001"}},
		"long search": {"q": {strings.Repeat("a", 201)}},
	} {
		if r := callAdminQ(t, "GET", owner, q); r.Status != 400 || r.Body != `{"error":"bad request"}` {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a bad request ran %d statements", len(*calls))
	}
}
