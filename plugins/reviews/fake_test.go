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

// fakeHost is an in-memory host: settings, a tiny interpreter for the
// statements store.go issues, an http.fetch hook and a log capture.
type fakeHost struct {
	t        *testing.T
	settings map[string]any
	reviews  map[string]reviewRow // key source + "/" + ext_id
	sources  map[string]sourceRow
	clock    time.Time

	dbDown      bool
	settingsErr error
	queries     int // db.query calls

	// fetch answers http.fetch; the default is a host error.
	fetch    func(url string, headers map[string]string) (int, string, error)
	requests []fetchReq
	logs     []string
}

type fetchReq struct {
	URL     string
	Headers map[string]string
}

func newFake(t *testing.T) *fakeHost {
	t.Helper()
	f := &fakeHost{
		t:        t,
		settings: map[string]any{},
		reviews:  map[string]reviewRow{},
		sources:  map[string]sourceRow{},
		clock:    time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
	}
	callHost = f.call
	now = func() time.Time { return f.clock }
	resetCache()
	t.Cleanup(func() {
		callHost = nil
		now = time.Now
		resetCache()
	})
	return f
}

func (f *fakeHost) set(k string, v any) { f.settings[k] = v }

func (f *fakeHost) setBoth() {
	f.set("google_api_key", "G-SECRET")
	f.set("google_place_id", "PLACE1")
	f.set("trustpilot_api_key", "T-SECRET")
	f.set("trustpilot_business_unit_id", "UNIT1")
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
		h, _ := a["headers"].(map[string]string)
		u, _ := a["url"].(string)
		f.requests = append(f.requests, fetchReq{URL: u, Headers: h})
		if f.fetch == nil {
			return errors.New("blocked")
		}
		status, body, err := f.fetch(u, h)
		if err != nil {
			return err
		}
		return roundTrip(map[string]any{"status": status, "body": body}, out)
	case "db.exec":
		if f.dbDown {
			return errors.New("db unavailable")
		}
		a := args.(map[string]any)
		return f.exec(a["sql"].(string), a["args"].([]any))
	case "db.query":
		f.queries++
		if f.dbDown {
			return errors.New("db unavailable")
		}
		a := args.(map[string]any)
		return f.query(a["sql"].(string), out)
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

func (f *fakeHost) exec(sql string, a []any) error {
	switch {
	case strings.HasPrefix(sql, "INSERT INTO reviews"):
		r := reviewRow{Source: a[0].(string), ExtID: a[1].(string), Author: a[2].(string), AuthorURL: a[3].(string),
			PhotoURL: a[4].(string), Rating: a[5].(int), Body: a[6].(string), Link: a[8].(string)}
		r.Published, _ = time.Parse(time.RFC3339Nano, a[7].(string))
		f.reviews[r.Source+"/"+r.ExtID] = r
	case strings.HasPrefix(sql, "DELETE FROM reviews WHERE source = $1 AND ext_id NOT IN"):
		var keep []string
		if err := json.Unmarshal([]byte(a[1].(string)), &keep); err != nil {
			return err
		}
		for k, r := range f.reviews {
			if r.Source != a[0].(string) {
				continue
			}
			found := false
			for _, id := range keep {
				found = found || id == r.ExtID
			}
			if !found {
				delete(f.reviews, k)
			}
		}
	case strings.HasPrefix(sql, "DELETE FROM reviews WHERE source = $1"):
		for k, r := range f.reviews {
			if r.Source == a[0].(string) {
				delete(f.reviews, k)
			}
		}
	case strings.HasPrefix(sql, "INSERT INTO sources (source, rating"):
		at, _ := time.Parse(time.RFC3339Nano, a[5].(string))
		rating := a[1].(float64)
		f.sources[a[0].(string)] = sourceRow{Source: a[0].(string), Rating: &rating, Count: a[2].(int),
			ProfileURL: a[3].(string), Name: a[4].(string), AttemptedAt: at, OKAt: at}
	case strings.HasPrefix(sql, "INSERT INTO sources (source, attempted_at"):
		s := f.sources[a[0].(string)]
		s.Source = a[0].(string)
		s.AttemptedAt, _ = time.Parse(time.RFC3339Nano, a[1].(string))
		s.LastError = a[2].(string)
		f.sources[s.Source] = s
	case strings.HasPrefix(sql, "DELETE FROM sources"):
		delete(f.sources, a[0].(string))
	default:
		return fmt.Errorf("fake db: unknown statement %.40q", sql)
	}
	return nil
}

func (f *fakeHost) query(sql string, out any) error {
	var doc any
	switch {
	case strings.Contains(sql, "FROM sources"):
		rows := []sourceRow{}
		for _, s := range f.sources {
			rows = append(rows, s)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Source < rows[j].Source })
		doc = rows
	case strings.Contains(sql, "FROM reviews"):
		rows := []reviewRow{}
		for _, r := range f.reviews {
			rows = append(rows, r)
		}
		sort.Slice(rows, func(i, j int) bool {
			if !rows[i].Published.Equal(rows[j].Published) {
				return rows[i].Published.After(rows[j].Published)
			}
			return rows[i].Source+rows[i].ExtID < rows[j].Source+rows[j].ExtID
		})
		doc = rows
	default:
		return fmt.Errorf("fake db: unknown query %.40q", sql)
	}
	cell, _ := json.Marshal(doc)
	return roundTrip(map[string]any{"rows": [][]json.RawMessage{{cell}}}, out)
}

// seed stores a successful refresh directly.
func (f *fakeHost) seed(t *testing.T, src string, rating float64, count int, rs ...review) {
	t.Helper()
	if err := replaceSource(src, sourceInfo{Rating: rating, Count: count, ProfileURL: "https://example.test/" + src}, rs); err != nil {
		t.Fatal(err)
	}
}
