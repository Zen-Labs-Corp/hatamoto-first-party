package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReplacePageLinksUpsertsAndPrunes(t *testing.T) {
	f := newFakeHost(t, nil)
	refs := []linkRef{{"/about", kindInternal}, {"https://example.org/x", kindOutbound}}
	if err := replacePageLinks(pageRef{ID: "e1", Name: "Pricing", Path: "/pricing"}, refs); err != nil {
		t.Fatal(err)
	}
	q := f.sqls()
	if len(q) != 3 {
		t.Fatalf("want 3 statements, got %d: %v", len(q), q)
	}
	for _, s := range q {
		oneStatement(t, s)
	}
	if !strings.Contains(q[0], "ON CONFLICT (url) DO UPDATE SET last_seen = now()") || strings.Contains(q[0], "status") {
		t.Fatalf("links upsert must not touch status: %s", q[0])
	}
	if !strings.Contains(q[1], "ON CONFLICT (link_id, entity_id) DO UPDATE") {
		t.Fatalf("link_pages upsert: %s", q[1])
	}
	if !strings.HasPrefix(q[2], "DELETE FROM link_pages") || !strings.Contains(q[2], "NOT IN") {
		t.Fatalf("prune: %s", q[2])
	}
	p := f.calls[1].params()
	if p[1] != "e1" || p[2] != "Pricing" || p[3] != "/pricing" {
		t.Fatalf("params %v", p)
	}
	var sent []linkRef
	if err := json.Unmarshal([]byte(p[0].(string)), &sent); err != nil || len(sent) != 2 {
		t.Fatalf("payload %v %v", p[0], err)
	}
}

func TestReplacePageLinksEmptySetOnlyPrunes(t *testing.T) {
	f := newFakeHost(t, nil)
	if err := replacePageLinks(pageRef{ID: "e1"}, nil); err != nil {
		t.Fatal(err)
	}
	if q := f.sqls(); len(q) != 1 || !strings.HasPrefix(q[0], "DELETE FROM link_pages") {
		t.Fatalf("got %v", q)
	}
	if got := f.calls[0].params()[1]; got != "[]" {
		t.Fatalf("empty set must be [], got %v", got)
	}
}

func TestReplacePageLinksChunksAndStopsOnError(t *testing.T) {
	f := newFakeHost(t, nil)
	var refs []linkRef
	for i := range 1001 {
		refs = append(refs, linkRef{URL: "/p" + time.Duration(i).String(), Kind: kindInternal})
	}
	if err := replacePageLinks(pageRef{ID: "e"}, refs); err != nil {
		t.Fatal(err)
	}
	if n := len(f.sqls()); n != 3*2+1 {
		t.Fatalf("1001 links = 3 chunks of 2 statements plus the prune, got %d", n)
	}
	f = newFakeHost(t, func(call, any) error { return errors.New("down") })
	if err := replacePageLinks(pageRef{ID: "e"}, refs[:1]); err == nil {
		t.Fatal("host error must surface to the caller")
	}
	if len(f.sqls()) != 1 {
		t.Fatal("must stop at the first failing statement")
	}
}

func TestDueLinks(t *testing.T) {
	fixedNow(t, "2026-01-02T03:04:05Z")
	f := newFakeHost(t, func(_ call, out any) error {
		return cell(out, `[{"id":7,"url":"/a","kind":"internal","fail_count":1}]`)
	})
	got, err := dueLinks(kindInternal, 20)
	if err != nil || len(got) != 1 || got[0].ID != 7 || got[0].FailCount != 1 {
		t.Fatalf("got %+v %v", got, err)
	}
	c := f.calls[0]
	if !strings.Contains(c.sql(), "status <> 'ignored'") || !strings.Contains(c.sql(), "LIMIT $3") {
		t.Fatal(c.sql())
	}
	// The oldest link of every host first: ranked per host, rank before age.
	if !strings.Contains(c.sql(), "PARTITION BY lower(substring(url") || !strings.Contains(c.sql(), "ORDER BY t.rn, t.next_check") {
		t.Fatalf("the batch must span hosts: %s", c.sql())
	}
	if p := c.params(); p[0] != "internal" || p[1] != "2026-01-02T03:04:05Z" || p[2] != float64(20) {
		t.Fatalf("params %v", p)
	}
}

func TestMarkResult(t *testing.T) {
	at := fixedNow(t, "2026-01-02T03:04:05Z")
	f := newFakeHost(t, nil)
	if err := markResult(9, statusBroken, 404, "", 0, at.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	c := f.calls[0]
	oneStatement(t, c.sql())
	if !strings.Contains(c.sql(), "status <> 'ignored'") || !strings.Contains(c.sql(), "coalesce(first_broken") {
		t.Fatal(c.sql())
	}
	p := c.params()
	if p[0] != float64(9) || p[1] != "broken" || p[2] != float64(404) || p[6] != "2026-01-09T03:04:05Z" {
		t.Fatalf("params %v", p)
	}
}

func TestPathAndParents(t *testing.T) {
	for in, want := range map[string][]string{
		"/":          nil,
		"/about":     {"/about"},
		"/blog/a/b":  {"/blog/a/b", "/blog/a", "/blog"},
		"":           nil,
		"/services/": {"/services"},
	} {
		if got := pathAndParents(in); !slices.Equal(got, want) {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestKnownPaths(t *testing.T) {
	f := newFakeHost(t, func(_ call, out any) error { return cell(out, `{"known":true}`) })
	if err := upsertKnownPaths([]string{"/blog/a", "/blog"}); err != nil {
		t.Fatal(err)
	}
	if q := f.sqls()[0]; !strings.Contains(q, "ON CONFLICT (path) DO UPDATE") {
		t.Fatal(q)
	}
	if ok, err := isKnownParent("/blog"); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestPurgeRetention(t *testing.T) {
	at := fixedNow(t, "2026-02-01T00:00:00Z")
	f := newFakeHost(t, nil)
	if err := purge(at, true); err != nil {
		t.Fatal(err)
	}
	q := f.sqls()
	if len(q) != 3 {
		t.Fatalf("got %v", q)
	}
	for i, c := range f.calls {
		oneStatement(t, q[i])
		if c.params()[0] != "2026-01-02T00:00:00Z" {
			t.Fatalf("cutoff must be 30 days back, got %v", c.params())
		}
	}
	if !strings.Contains(q[1], "NOT EXISTS") {
		t.Fatalf("a link still on a page must survive: %s", q[1])
	}
}

func TestListCountsAndQueue(t *testing.T) {
	fixedNow(t, "2026-01-02T03:04:05Z")
	f := newFakeHost(t, func(c call, out any) error {
		switch {
		case strings.Contains(c.sql(), "'broken', count"):
			return cell(out, `{"broken":2,"ok":5,"pages":3}`)
		case strings.Contains(c.sql(), "'n'"):
			return cell(out, `{"n":37}`)
		}
		return cell(out, `[{"id":3,"url":"https://example.org/x","kind":"outbound","status":"broken","http_status":404,"error":null,"pages":[{"entity_id":"e1","entity_name":"Home","entity_path":"/"}]}]`)
	})
	rows, err := listLinks(statusBroken, 2, 51)
	if err != nil || len(rows) != 1 || rows[0].Pages[0].EntityName != "Home" || *rows[0].HTTPStatus != 404 || rows[0].Error != nil {
		t.Fatalf("%+v %v", rows, err)
	}
	if p := f.calls[0].params(); p[0] != "broken" || p[1] != float64(2) || p[2] != float64(51) {
		t.Fatalf("params %v", p)
	}
	if c, err := countLinks(); err != nil || c.Broken != 2 || c.OK != 5 || c.Pages != 3 {
		t.Fatalf("%+v %v", c, err)
	}
	if n, err := queueSize(); err != nil || n != 37 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestSetRecheckAndToggleIgnore(t *testing.T) {
	fixedNow(t, "2026-01-02T03:04:05Z")
	found := `[{"id":4,"url":"/x","kind":"internal","status":"pending","pages":[]}]`
	f := newFakeHost(t, func(_ call, out any) error { return cell(out, found) })
	row, ok, err := setRecheck(4)
	if err != nil || !ok || row.ID != 4 {
		t.Fatalf("%+v %v %v", row, ok, err)
	}
	if !strings.HasPrefix(f.sqls()[0], "UPDATE links SET next_check") || f.calls[0].params()[1] != "2026-01-02T03:04:05Z" {
		t.Fatalf("%v", f.calls[0])
	}
	if _, ok, _ := toggleIgnore(4); !ok {
		t.Fatal("toggle")
	}
	if q := f.sqls()[2]; !strings.Contains(q, "WHEN status = 'ignored' THEN 'pending'") {
		t.Fatal(q)
	}
	found = `[]`
	if _, ok, err := setRecheck(99); ok || err != nil {
		t.Fatalf("missing link: %v %v", ok, err)
	}
}

func TestStoreFailsOpenOnHostError(t *testing.T) {
	newFakeHost(t, func(call, any) error { return errors.New("db unavailable") })
	if _, err := dueLinks(kindOutbound, 5); err == nil {
		t.Fatal("dueLinks must return the error, not panic")
	}
	if _, err := countLinks(); err == nil {
		t.Fatal("countLinks must return the error")
	}
	if err := purge(now(), true); err == nil {
		t.Fatal("purge must return the error")
	}
}

func TestPurgeSkippedWithoutACompletedPass(t *testing.T) {
	f := newFakeHost(t, nil)
	if err := purge(now(), false); err != nil || len(f.calls) != 0 {
		t.Fatalf("nothing may be deleted: %v %v", err, f.calls)
	}
}

func TestPassInfoRecent(t *testing.T) {
	at := fixedNow(t, "2026-02-01T00:00:00Z")
	for _, c := range []struct {
		p    passInfo
		want bool
	}{
		{passInfo{}, false},
		{passInfo{Done: true, StartedAt: "2026-01-20T00:00:00Z"}, true},
		{passInfo{Done: true, StartedAt: "2025-12-01T00:00:00Z"}, false},
		{passInfo{Done: true, StartedAt: ""}, false},
	} {
		if got := c.p.recent(at); got != c.want {
			t.Errorf("%+v: %v", c.p, got)
		}
	}
}

func TestParentPathsExcludeThePageItself(t *testing.T) {
	for in, want := range map[string][]string{
		"/pricing": nil, "/blog/a": {"/blog"}, "/a/b/c/": {"/a/b", "/a"}, "": nil, "/": nil,
	} {
		if got := parentPaths(in); !slices.Equal(got, want) {
			t.Errorf("%q: %v want %v", in, got, want)
		}
	}
}

func TestQueriesIgnoreLinksOnNoPage(t *testing.T) {
	f := newFakeHost(t, func(_ call, out any) error { return cell(out, `[]`) })
	_, _ = dueLinks(kindOutbound, 5)
	_, _ = listLinks(statusBroken, 0, 5)
	_ = dbJSONDiscard(countLinks)
	_, _ = queueSize()
	if len(f.calls) != 4 {
		t.Fatalf("calls %d", len(f.calls))
	}
	for _, c := range f.calls {
		if !strings.Contains(c.sql(), "FROM link_pages p WHERE p.link_id =") {
			t.Errorf("a link on no page must not be checked or listed: %s", c.sql())
		}
	}
}

func dbJSONDiscard(fn func() (counts, error)) error { _, err := fn(); return err }

func TestRemoveExcluded(t *testing.T) {
	f := newFakeHost(t, nil)
	if err := removeExcluded(nil); err != nil || len(f.calls) != 0 {
		t.Fatalf("nothing to do without prefixes: %v %v", err, f.calls)
	}
	if err := removeExcluded([]string{"https://partner.example/", "/members"}); err != nil {
		t.Fatal(err)
	}
	c := f.calls[0]
	oneStatement(t, c.sql())
	if !strings.HasPrefix(c.sql(), "DELETE FROM link_pages") || c.params()[0] != `["https://partner.example/","/members"]` {
		t.Fatalf("%s %v", c.sql(), c.params())
	}
}

func TestSelectRowsCapsPagesPerLink(t *testing.T) {
	f := newFakeHost(t, func(_ call, out any) error { return cell(out, `[]`) })
	_, _ = listLinks(statusOK, 0, 51)
	q := f.calls[0].sql()
	if maxPagesPerLink != 10 || !strings.Contains(q, "LIMIT 10) p") || !strings.Contains(q, "AS page_count") {
		t.Fatalf("pages per link must be capped with a total: %s", q)
	}
}

func TestKnownPathCoversPublishedOwnPaths(t *testing.T) {
	f := newFakeHost(t, func(_ call, out any) error { return cell(out, `{"known":true}`) })
	if _, err := isKnownParent("/fr/a-propos"); err != nil {
		t.Fatal(err)
	}
	if q := f.sqls()[0]; !strings.Contains(q, "FROM page_paths WHERE path = $1") {
		t.Fatalf("a published page's own path must count as known: %s", q)
	}
}
