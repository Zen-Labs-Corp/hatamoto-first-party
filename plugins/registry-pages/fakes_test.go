package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// page is what the fake serves for one URL.
type page struct {
	status    int
	body      []byte
	err       error
	truncated bool
	b64       bool // deliver the body as body_b64
}

type kvItem struct {
	value json.RawMessage
	exp   time.Time // zero: never
}

// fakeHost stands in for the host: http.fetch per URL, an in-memory kv that
// honours ttl against the swapped clock, and an entities hook for the sync tests.
type fakeHost struct {
	t       *testing.T
	clock   time.Time
	pages   map[string]*page
	hits    map[string]int
	timeout map[string][]int // timeout_ms of every fetch, by URL
	kv      map[string]kvItem
	kvErr   error
	// entities answers entities.* calls (nil: not found error).
	entities func(fn string, args map[string]any, out any) error
	calls    []string

	mainPriv, devPriv ed25519.PrivateKey
}

// newFakeHost installs a fake host, test trust keys, a fixed clock and a fresh
// per-instance snapshot, and undoes all of it when the test ends.
func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	h := &fakeHost{
		t:       t,
		clock:   time.Date(2030, 1, 5, 12, 0, 0, 0, time.UTC),
		pages:   map[string]*page{},
		hits:    map[string]int{},
		timeout: map[string][]int{},
		kv:      map[string]kvItem{},
	}
	h.mainPriv = ed25519.NewKeyFromSeed(seed(1))
	h.devPriv = ed25519.NewKeyFromSeed(seed(2))
	oldMain, oldDev, oldNow, oldMem, oldHost := mainTrust, devTrust, now, mem, callHost
	t.Cleanup(func() { mainTrust, devTrust, now, mem, callHost = oldMain, oldDev, oldNow, oldMem, oldHost })
	mainTrust, devTrust = trustFor(h.mainPriv), trustFor(h.devPriv)
	now = func() time.Time { return h.clock }
	mem = snapshot{}
	callHost = h.call
	return h
}

func seed(b byte) []byte { return []byte(strings.Repeat(string([]byte{b}), 32)) }

func (h *fakeHost) advance(d time.Duration) { h.clock = h.clock.Add(d) }

// newInstance forgets the per-instance snapshot, as a second wasm instance would.
func (h *fakeHost) newInstance() { mem = snapshot{} }

// publish serves a signed index at url, valid from a minute ago for life.
func (h *fakeHost) publish(url string, life time.Duration, entries ...entry) {
	h.t.Helper()
	h.publishWindow(url, h.clock.Add(-time.Minute), h.clock.Add(life), nil, entries...)
}

func (h *fakeHost) publishWindow(url string, issued, expires time.Time, revoked []revocation, entries ...entry) {
	h.t.Helper()
	pkgs := make([]entry, len(entries))
	for i, e := range entries {
		e.URL, e.SHA256 = "p/"+e.Name+".hmp", strings.Repeat("a", 64)
		if e.Version == "" {
			e.Version = "1.0.0"
		}
		pkgs[i] = e
	}
	idx := index{Version: 1, IssuedAt: issued.Format(time.RFC3339), ExpiresAt: expires.Format(time.RFC3339), Packages: pkgs, Revoked: revoked}
	raw, err := json.Marshal(idx)
	if err != nil {
		h.t.Fatal(err)
	}
	priv := h.mainPriv
	if url == developerURL {
		priv = h.devPriv
	}
	h.pages[url] = &page{status: 200, body: sign(h.t, string(raw), priv)}
}

// fail makes url answer 500.
func (h *fakeHost) fail(url string) { h.pages[url] = &page{status: 500, body: []byte("boom")} }

func (h *fakeHost) call(fn string, args, out any) error {
	h.calls = append(h.calls, fn)
	m := map[string]any{}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			h.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			h.t.Fatal(err)
		}
	}
	switch fn {
	case sdk.FnHTTPFetch:
		return h.fetch(m, out)
	case sdk.FnKVGet:
		return h.kvGet(m, out)
	case sdk.FnKVSet:
		return h.kvSet(m)
	}
	if h.entities != nil && strings.HasPrefix(fn, "entities.") {
		return h.entities(fn, m, out)
	}
	return &sdk.HostError{Code: sdk.ErrNotFound, Message: "no fake for " + fn}
}

func (h *fakeHost) fetch(m map[string]any, out any) error {
	url := m["url"].(string)
	h.hits[url]++
	h.timeout[url] = append(h.timeout[url], int(m["timeout_ms"].(float64)))
	p, ok := h.pages[url]
	if !ok {
		return &sdk.HostError{Code: sdk.ErrBlocked, Message: "not served"}
	}
	if p.err != nil {
		return p.err
	}
	res := map[string]any{"status": p.status, "truncated": p.truncated}
	if p.b64 {
		res["body_b64"] = base64.StdEncoding.EncodeToString(p.body)
	} else {
		res["body"] = string(p.body)
	}
	return reply(res, out)
}

func (h *fakeHost) kvGet(m map[string]any, out any) error {
	if h.kvErr != nil {
		return h.kvErr
	}
	it, ok := h.kv[m["key"].(string)]
	if !ok || (!it.exp.IsZero() && !h.clock.Before(it.exp)) {
		return reply(map[string]any{"found": false}, out)
	}
	return reply(map[string]any{"found": true, "value": it.value}, out)
}

func (h *fakeHost) kvSet(m map[string]any) error {
	if h.kvErr != nil {
		return h.kvErr
	}
	raw, err := json.Marshal(m["value"])
	if err != nil {
		return err
	}
	it := kvItem{value: raw}
	if ttl, ok := m["ttl_seconds"].(float64); ok {
		it.exp = h.clock.Add(time.Duration(ttl) * time.Second)
	}
	h.kv[m["key"].(string)] = it
	return nil
}

func reply(res map[string]any, out any) error {
	if out == nil {
		return nil
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

var errHostDown = errors.New("host down")

// entityStore is an in-memory registry-entry store behind the fake host's
// entities.* functions.
type entityStore struct {
	h        *fakeHost
	items    []entity
	nextID   int
	pageSize int // entities.list page size (0: the requested limit)

	// errs returns the error of a call (nil: succeed); n counts calls of fn from 1.
	errs func(fn string, n int, args map[string]any) error
	// slugFor overrides the slug a create ends up with.
	slugFor func(want string) string

	counts map[string]int
	writes int
	log    []string
}

// newEntityStore attaches an empty store to the fake host.
func newEntityStore(h *fakeHost) *entityStore {
	s := &entityStore{h: h, counts: map[string]int{}}
	h.entities = s.call
	return s
}

func (s *entityStore) call(fn string, args map[string]any, out any) error {
	s.counts[fn]++
	if s.errs != nil {
		if err := s.errs(fn, s.counts[fn], args); err != nil {
			return err
		}
	}
	switch fn {
	case "entities.list":
		return s.list(args, out)
	case "entities.create":
		s.writes++
		s.nextID++
		slug, _ := args["slug"].(string)
		if s.slugFor != nil {
			slug = s.slugFor(slug)
		}
		e := entity{ID: "e" + strconv.Itoa(s.nextID), Slug: slug, Name: args["name"].(string), Attributes: args["attributes"].(map[string]any)}
		s.items = append(s.items, e)
		s.log = append(s.log, "create "+slug)
		return reply(map[string]any{"id": e.ID, "slug": e.Slug}, out)
	case "entities.update":
		s.writes++
		id := args["id"].(string)
		for i := range s.items {
			if s.items[i].ID == id {
				s.items[i].Name = args["name"].(string)
				s.items[i].Attributes = args["attributes"].(map[string]any)
				s.log = append(s.log, "update "+s.items[i].Slug)
				return nil
			}
		}
		return &sdk.HostError{Code: sdk.ErrNotFound}
	case "entities.delete":
		s.writes++
		id := args["id"].(string)
		for i := range s.items {
			if s.items[i].ID == id {
				s.log = append(s.log, "delete "+s.items[i].Slug)
				s.items = slices.Delete(s.items, i, i+1)
				return nil
			}
		}
		return &sdk.HostError{Code: sdk.ErrNotFound}
	}
	return &sdk.HostError{Code: sdk.ErrUnknownFunction}
}

func (s *entityStore) list(args map[string]any, out any) error {
	if args["schema_slug"] != "registry-entry" {
		return &sdk.HostError{Code: sdk.ErrInvalidArgument}
	}
	size := int(args["limit"].(float64))
	if s.pageSize > 0 {
		size = min(size, s.pageSize)
	}
	from := 0
	if c, ok := args["cursor"].(string); ok {
		from, _ = strconv.Atoi(c)
	}
	to := min(from+size, len(s.items))
	res := map[string]any{"items": s.items[from:to], "next_cursor": nil}
	if to < len(s.items) {
		res["next_cursor"] = strconv.Itoa(to)
	}
	return reply(res, out)
}

// seed adds existing entries as the plugin would have made them.
func (s *entityStore) seed(ws ...want) {
	for _, w := range ws {
		s.nextID++
		attrs := map[string]any{}
		for k, v := range w.Attrs {
			attrs[k] = v
		}
		s.items = append(s.items, entity{ID: "e" + strconv.Itoa(s.nextID), Slug: w.Slug, Name: w.Name, Attributes: attrs})
	}
}

func (s *entityStore) slugs() []string {
	var out []string
	for _, e := range s.items {
		out = append(out, e.Slug)
	}
	slices.Sort(out)
	return out
}
