package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func publish(t *testing.T, id string) error {
	t.Helper()
	in, _ := json.Marshal(sdk.EntityEvent{ID: id, Status: "published"})
	_, err := guest.Handle(sdk.EntityPublished.Export(), in)
	return err
}

func TestPublishedReplacesPageLinks(t *testing.T) {
	f := newFakeHost(t, func(c call, out any) error {
		switch c.Fn {
		case "entities.get":
			if c.Args["id"] != "e1" || len(c.Args) != 1 {
				t.Fatalf("entities.get args %v", c.Args)
			}
			return json.Unmarshal([]byte(`{"id":"e1","name":"Pricing","path":"/blog/pricing","url":"https://example.org/blog/pricing",
				"attributes":{"content":"<a href=\"/about\">a</a> <a href=\"https://example.org/team\">t</a> <a href=\"https://example.com/x\">x</a> <a href=\"/members/a\">m</a>"}}`), out)
		case "settings.get":
			return json.Unmarshal([]byte(`{"exclude":["/members"]}`), out)
		}
		return nil
	})
	if err := publish(t, "e1"); err != nil {
		t.Fatal(err)
	}
	if f.count("http.fetch") != 0 || f.count("redirects.list") != 0 {
		t.Fatal("the hook must not use the network or the redirect list")
	}
	var upsert, prune call
	var known []string
	for _, c := range f.calls {
		switch {
		case strings.HasPrefix(c.sql(), "INSERT INTO links"):
			upsert = c
		case strings.HasPrefix(c.sql(), "DELETE FROM link_pages"):
			prune = c
		case strings.HasPrefix(c.sql(), "INSERT INTO known_paths"):
			known = append(known, c.params()[0].(string))
		}
	}
	// Same-origin absolute links are internal; excluded ones are not stored.
	want := `[{"url":"/about","kind":"internal"},{"url":"/team","kind":"internal"},{"url":"https://example.com/x","kind":"outbound"}]`
	if upsert.params()[0] != want || prune.params()[0] != "e1" || prune.params()[1] != want {
		t.Fatalf("upsert %v prune %v", upsert.params(), prune.params())
	}
	if len(known) != 1 || known[0] != `["/blog"]` {
		t.Fatalf("known paths %v", known)
	}
	// New links stay pending: the insert sets no status, the table default does.
	if strings.Contains(upsert.sql(), "status") {
		t.Fatal("new links must take the default status (pending)")
	}
}

func TestPublishedRemovedLinksDropOff(t *testing.T) {
	f := newFakeHost(t, func(c call, out any) error {
		if c.Fn == "entities.get" {
			return json.Unmarshal([]byte(`{"id":"e1","name":"P","path":"/p","url":"/p","attributes":{"content":"no links left"}}`), out)
		}
		return nil
	})
	if err := publish(t, "e1"); err != nil {
		t.Fatal(err)
	}
	var pruned bool
	for _, c := range f.calls {
		if strings.HasPrefix(c.sql(), "DELETE FROM link_pages") && c.params()[1] == "[]" {
			pruned = true
		}
		if strings.HasPrefix(c.sql(), "INSERT INTO links") {
			t.Fatal("nothing to insert")
		}
	}
	if !pruned {
		t.Fatal("the page's old links must be removed")
	}
}

func TestPublishedAlwaysReturnsNil(t *testing.T) {
	for name, fail := range map[string]string{
		"entities.get": "entities.get", "db": "db.exec", "settings": "settings.get",
	} {
		f := newFakeHost(t, func(c call, out any) error {
			if c.Fn == fail {
				return &sdk.HostError{Code: sdk.ErrUnavailable}
			}
			if c.Fn == "entities.get" {
				return json.Unmarshal([]byte(`{"id":"e1","path":"/p","attributes":{"a":"<a href=\"/x\">x</a>"}}`), out)
			}
			return errors.New("not wanted")
		})
		if err := onPublished(sdk.EntityEvent{ID: "e1"}); err != nil {
			t.Errorf("%s: hook returned %v", name, err)
		}
		if f.count("http.fetch") != 0 {
			t.Errorf("%s: no network", name)
		}
	}
}
