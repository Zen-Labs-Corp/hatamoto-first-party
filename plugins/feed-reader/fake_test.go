package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeItem is a stored item in the fake database.
type fakeItem struct {
	FeedKey   string
	GUID      string
	Title     string
	Link      string
	Excerpt   string
	Published time.Time
	FetchedAt time.Time
}

func (i fakeItem) sortTime() time.Time {
	if i.Published.IsZero() {
		return i.FetchedAt
	}
	return i.Published
}

// fakeHost is an in-memory host: settings, a small interpreter for the
// statements store.go issues, and a log capture.
type fakeHost struct {
	t        *testing.T
	settings map[string]any
	feeds    map[string]feedRow
	items    map[string]fakeItem // key feed_key + "/" + guid
	clock    time.Time

	dbDown      bool
	itemsErr    error // fails the item upsert only
	settingsErr error
	logs        []string

	// fetch answers http.fetch; the default is a host error.
	fetch    func(url string, headers map[string]string) (fetchRes, error)
	requests []fetchReq
}

type fetchReq struct {
	URL       string
	Headers   map[string]string
	TimeoutMS int
}

func newFake(t *testing.T) *fakeHost {
	t.Helper()
	f := &fakeHost{
		t:        t,
		settings: map[string]any{},
		feeds:    map[string]feedRow{},
		items:    map[string]fakeItem{},
		clock:    time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
	}
	callHost = f.call
	now = func() time.Time { return f.clock }
	t.Cleanup(func() {
		callHost = nil
		now = time.Now
	})
	return f
}

func (f *fakeHost) logged() string { return strings.Join(f.logs, "\n") }

func (f *fakeHost) call(fn string, args, out any) error {
	switch fn {
	case "settings.get":
		if f.settingsErr != nil {
			return f.settingsErr
		}
		return roundTrip(f.settings, out)
	case "log":
		m := args.(map[string]string)
		f.logs = append(f.logs, m["level"]+": "+m["message"])
		return nil
	case "http.fetch":
		a := args.(map[string]any)
		req := fetchReq{URL: a["url"].(string), Headers: a["headers"].(map[string]string), TimeoutMS: a["timeout_ms"].(int)}
		f.requests = append(f.requests, req)
		if f.fetch == nil {
			return errors.New("no fetch configured")
		}
		res, err := f.fetch(req.URL, req.Headers)
		if err != nil {
			return err
		}
		return roundTrip(res, out)
	case "db.exec":
		if f.dbDown {
			return errors.New("db unavailable")
		}
		a := args.(map[string]any)
		return f.exec(a["sql"].(string), a["args"].([]any))
	case "db.query":
		if f.dbDown {
			return errors.New("db unavailable")
		}
		a := args.(map[string]any)
		return f.query(a["sql"].(string), a["args"].([]any), out)
	}
	f.t.Fatalf("unexpected host call %q", fn)
	return nil
}

func roundTrip(in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func parseStamp(v any) time.Time {
	tm, _ := time.Parse(time.RFC3339Nano, v.(string))
	return tm
}

func keySet(v any) map[string]bool {
	var keys []string
	_ = json.Unmarshal([]byte(v.(string)), &keys)
	set := map[string]bool{}
	for _, k := range keys {
		set[k] = true
	}
	return set
}

func (f *fakeHost) exec(sql string, a []any) error {
	switch {
	case strings.HasPrefix(sql, "INSERT INTO feeds"):
		r := f.feeds[a[0].(string)]
		r.Key, r.URL, r.Name, r.Group = a[0].(string), a[1].(string), a[2].(string), a[3].(int)
		f.feeds[r.Key] = r
	case strings.HasPrefix(sql, "DELETE FROM items WHERE feed_key NOT IN"):
		keep := keySet(a[0])
		for k, it := range f.items {
			if !keep[it.FeedKey] {
				delete(f.items, k)
			}
		}
	case strings.HasPrefix(sql, "DELETE FROM feeds WHERE key NOT IN"):
		keep := keySet(a[0])
		for k := range f.feeds {
			if !keep[k] {
				delete(f.feeds, k)
			}
		}
	case strings.HasPrefix(sql, "UPDATE feeds SET last_attempt = NULL"):
		for k := range keySet(a[0]) {
			if r, ok := f.feeds[k]; ok {
				r.LastAttempt = time.Time{}
				f.feeds[k] = r
			}
		}
	case strings.HasPrefix(sql, "UPDATE feeds SET last_attempt = $2::timestamptz WHERE"):
		if r, ok := f.feeds[a[0].(string)]; ok {
			r.LastAttempt = parseStamp(a[1])
			f.feeds[r.Key] = r
		}
	case strings.HasPrefix(sql, "UPDATE feeds SET last_attempt = $2"):
		r, ok := f.feeds[a[0].(string)]
		if !ok {
			return nil
		}
		r.LastAttempt, r.Status, r.ETag, r.LastModified = parseStamp(a[1]), a[2].(string), a[3].(string), a[4].(string)
		r.SiteURL, r.ItemCount = a[5].(string), a[6].(int)
		f.feeds[r.Key] = r
	case strings.HasPrefix(sql, "INSERT INTO items"):
		if f.itemsErr != nil {
			return f.itemsErr
		}
		var docs []itemDoc
		if err := json.Unmarshal([]byte(a[1].(string)), &docs); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, d := range docs {
			if seen[d.GUID] {
				return errors.New("fake db: ON CONFLICT cannot affect a row a second time")
			}
			seen[d.GUID] = true
			k := a[0].(string) + "/" + d.GUID
			it, ok := f.items[k]
			if !ok {
				it = fakeItem{FeedKey: a[0].(string), GUID: d.GUID, FetchedAt: f.clock}
			}
			it.Title, it.Link, it.Excerpt = d.Title, d.Link, d.Excerpt
			it.Published = time.Time{}
			if d.Published != nil {
				it.Published = parseStamp(*d.Published)
			}
			f.items[k] = it
		}
	case strings.HasPrefix(sql, "DELETE FROM items WHERE feed_key = $1 AND guid NOT IN"):
		key, limit := a[0].(string), a[1].(int)
		mine := f.itemsOf(key)
		for i, it := range mine {
			if i >= limit {
				delete(f.items, it.FeedKey+"/"+it.GUID)
			}
		}
	case strings.HasPrefix(sql, "DELETE FROM items WHERE feed_key = $1 AND coalesce"):
		cutoff := parseStamp(a[1])
		for _, it := range f.itemsOf(a[0].(string)) {
			if it.sortTime().Before(cutoff) {
				delete(f.items, it.FeedKey+"/"+it.GUID)
			}
		}
	default:
		return fmt.Errorf("fake db: unknown statement %.50q", sql)
	}
	return nil
}

// itemsOf returns a feed's items newest first (ties by guid).
func (f *fakeHost) itemsOf(key string) []fakeItem {
	var out []fakeItem
	for _, it := range f.items {
		if it.FeedKey == key {
			out = append(out, it)
		}
	}
	sortItems(out)
	return out
}

func sortItems(s []fakeItem) {
	sort.Slice(s, func(i, j int) bool {
		if !s[i].sortTime().Equal(s[j].sortTime()) {
			return s[i].sortTime().After(s[j].sortTime())
		}
		return s[i].GUID < s[j].GUID
	})
}

func (f *fakeHost) query(sql string, a []any, out any) error {
	var doc any
	switch {
	case strings.Contains(sql, "FROM feeds ORDER BY name"):
		rows := []feedRow{}
		for _, r := range f.feeds {
			rows = append(rows, r)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Name != rows[j].Name {
				return rows[i].Name < rows[j].Name
			}
			return rows[i].Key < rows[j].Key
		})
		doc = rows
	case strings.Contains(sql, "count(*)::integer AS n FROM items"):
		n := len(f.itemsOf(a[0].(string)))
		doc = []map[string]int{{"n": n}}
	case strings.Contains(sql, "FROM items i JOIN feeds f"):
		group, limit := a[0].(int), a[1].(int)
		var all []fakeItem
		for _, it := range f.items {
			if fr, ok := f.feeds[it.FeedKey]; ok && (group == 0 || fr.Group == group) {
				all = append(all, it)
			}
		}
		sortItems(all)
		rows := []itemRow{}
		for _, it := range all {
			if len(rows) == limit {
				break
			}
			fr := f.feeds[it.FeedKey]
			rows = append(rows, itemRow{Title: it.Title, Link: it.Link, Excerpt: it.Excerpt, Published: it.Published,
				FetchedAt: it.FetchedAt, FeedName: fr.Name, FeedURL: fr.URL, FeedSiteURL: fr.SiteURL})
		}
		doc = rows
	default:
		return fmt.Errorf("fake db: unknown query %.50q", sql)
	}
	cell, _ := json.Marshal(doc)
	return roundTrip(map[string]any{"rows": [][]json.RawMessage{{cell}}}, out)
}
