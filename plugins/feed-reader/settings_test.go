package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// settingsHost answers settings.get with the given JSON document.
func settingsHost(t *testing.T, doc string) {
	t.Helper()
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		return json.Unmarshal([]byte(doc), out)
	}
	t.Cleanup(func() { callHost = nil })
}

func TestLoadFeedsMissingOrNullIsEmpty(t *testing.T) {
	for _, doc := range []string{`{}`, `{"feeds":null}`, `{"feeds":[]}`} {
		settingsHost(t, doc)
		got, err := loadFeeds()
		if err != nil || len(got) != 0 {
			t.Fatalf("%s: got %+v, %v", doc, got, err)
		}
	}
}

func TestLoadFeedsClampsAndDefaults(t *testing.T) {
	settingsHost(t, `{"feeds":[
		{"name":"A","url":"https://example.com/a.xml","max_items":500,"group":3},
		{"name":"B","url":"https://example.com/b.xml","max_items":0,"group":9},
		{"name":"C","url":"https://example.com/c.xml","max_items":-4,"group":-1},
		{"name":" ","url":"http://example.org/d.xml"}]}`)
	got, err := loadFeeds()
	if err != nil || len(got) != 4 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got[0].MaxItems != 200 || got[0].Group != 3 {
		t.Fatalf("A = %+v", got[0])
	}
	if got[1].MaxItems != 50 || got[1].Group != 0 {
		t.Fatalf("B = %+v", got[1])
	}
	if got[2].MaxItems != 50 || got[2].Group != 0 {
		t.Fatalf("C = %+v", got[2])
	}
	if got[3].Name != "http://example.org/d.xml" {
		t.Fatalf("empty name should fall back to the URL: %+v", got[3])
	}
	if len(got[0].Key) != 64 || got[0].Key == got[1].Key {
		t.Fatalf("keys %q %q", got[0].Key, got[1].Key)
	}
}

func TestLoadFeedsSkipsBadURLsAndDuplicates(t *testing.T) {
	settingsHost(t, `{"feeds":[
		{"name":"ftp","url":"ftp://example.com/feed"},
		{"name":"js","url":"javascript:alert(1)"},
		{"name":"cred","url":"https://user:pass@example.com/feed"},
		{"name":"rel","url":"/feed.xml"},
		{"name":"nohost","url":"https:///feed"},
		{"name":"empty","url":""},
		{"name":"ok","url":"https://example.com/feed"},
		{"name":"dup","url":"https://example.com/feed"}]}`)
	got, err := loadFeeds()
	if err != nil || len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestLoadFeedsCapsAtTwenty(t *testing.T) {
	var feeds []map[string]any
	for i := 0; i < 25; i++ {
		feeds = append(feeds, map[string]any{"name": fmt.Sprint("f", i), "url": fmt.Sprintf("https://example.com/%d.xml", i)})
	}
	raw, _ := json.Marshal(map[string]any{"feeds": feeds})
	settingsHost(t, string(raw))
	got, _ := loadFeeds()
	if len(got) != 20 || got[19].Name != "f19" {
		t.Fatalf("len %d", len(got))
	}
}

func TestLoadFeedsHostErrorIsReturned(t *testing.T) {
	callHost = func(string, any, any) error { return fmt.Errorf("down") }
	t.Cleanup(func() { callHost = nil })
	if _, err := loadFeeds(); err == nil {
		t.Fatal("want the host error so the caller can log it")
	}
}
