package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"hatamoto.dev/sdk"
)

const (
	blockedKey  = "sync_blocked"   // names whose create came back under another slug
	refusedKey  = "sync_not_owned" // IDs of entries the owner took over
	blockedTTL  = 6 * 60 * 60      // seconds, for both sets
	writeBudget = 7 * time.Second
	listLimit   = 200
)

// attrKeys are the entity fields the plugin writes, in manifest order.
var attrKeys = []string{"kind", "meta_title", "meta_description", "canonical_url"}

// entity is one registry-entry as entities.list returns it.
type entity struct {
	ID         string         `json:"id"`
	Slug       string         `json:"slug"`
	Name       string         `json:"name"`
	Attributes map[string]any `json:"attributes"`
}

// want is the registry-entry a registry name should have.
type want struct {
	Slug, Name string
	Attrs      map[string]string
}

// update is an existing entry that differs from what it should be.
type update struct {
	ID string
	want
}

// desired maps a verified snapshot to one wanted entry per name, and the names
// listed as both a plugin and a theme (their slug would collide, so they are
// left alone). ok is false for a snapshot that holds no verified index.
func desired(s snapshot) (ws map[string]want, collisions map[string]bool, ok bool) {
	for _, st := range []string{s.Plugins.State, s.Themes.State} {
		if st != stateOK && st != stateEmpty {
			return nil, nil, false
		}
	}
	ws, collisions = map[string]want{}, map[string]bool{}
	for _, c := range s.Plugins.Cards {
		ws[c.Name] = newWant(kindPlugin, "plugins", c.Name, c.Title, c.Description)
	}
	for _, c := range s.Themes.Cards {
		if _, both := ws[c.Name]; both {
			collisions[c.Name] = true
			delete(ws, c.Name)
			continue
		}
		ws[c.Name] = newWant(kindTheme, "themes", c.Name, c.Title, c.Description)
	}
	return ws, collisions, true
}

func newWant(kind, dir, name, title, description string) want {
	label := cmp.Or(title, name)
	return want{Slug: name, Name: label, Attrs: map[string]string{
		"kind":             kind,
		"meta_title":       label + " " + kind,
		"meta_description": description,
		"canonical_url":    "/" + dir + "/" + name,
	}}
}

// differs reports whether the entry no longer matches w.
func (e entity) differs(w want) bool {
	if e.Name != w.Name {
		return true
	}
	for _, k := range attrKeys {
		if got, _ := e.Attributes[k].(string); got != w.Attrs[k] {
			return true
		}
	}
	return false
}

// diff compares the wanted entries with the existing ones. Names in blocked
// are never created again; colliding names are never touched; entries in
// refused (answered not_owned) are neither updated nor deleted.
func diff(ws map[string]want, collisions map[string]bool, have []entity, blocked, refused map[string]bool) (creates []want, updates []update, deletes []string) {
	byslug := map[string]entity{}
	for _, e := range have {
		byslug[e.Slug] = e
	}
	for slug, w := range ws {
		e, exists := byslug[slug]
		switch {
		case !exists && !blocked[slug]:
			creates = append(creates, w)
		case exists && e.differs(w) && !refused[e.ID]:
			updates = append(updates, update{ID: e.ID, want: w})
		}
	}
	for _, e := range have {
		if _, keep := ws[e.Slug]; !keep && !collisions[e.Slug] && !refused[e.ID] {
			deletes = append(deletes, e.ID)
		}
	}
	slices.SortFunc(creates, func(a, b want) int { return cmp.Compare(a.Slug, b.Slug) })
	slices.SortFunc(updates, func(a, b update) int { return cmp.Compare(a.Slug, b.Slug) })
	slices.Sort(deletes)
	return creates, updates, deletes
}

// listOwn reads every registry-entry the plugin can see, page by page.
func listOwn() ([]entity, error) {
	var all []entity
	cursor := ""
	for {
		args := map[string]any{"schema_slug": entitySchema, "limit": listLimit}
		if cursor != "" {
			args["cursor"] = cursor
		}
		var res struct {
			Items      []entity `json:"items"`
			NextCursor *string  `json:"next_cursor"`
		}
		if err := callHost(sdk.FnEntitiesList, args, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
		if res.NextCursor == nil || *res.NextCursor == "" || len(res.Items) == 0 {
			return all, nil
		}
		cursor = *res.NextCursor
	}
}

// loadSet reads a remembered set of names or IDs from kv.
func loadSet(key string) map[string]bool {
	out := map[string]bool{}
	var res struct {
		Found bool     `json:"found"`
		Value []string `json:"value"`
	}
	if err := callHost(sdk.FnKVGet, map[string]string{"key": key}, &res); err != nil || !res.Found {
		return out
	}
	for _, n := range res.Value {
		out[n] = true
	}
	return out
}

func storeSet(key string, set map[string]bool) {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	slices.Sort(names)
	_ = callHost(sdk.FnKVSet, map[string]any{"key": key, "value": names, "ttl_seconds": blockedTTL}, nil)
}

func hostCode(err error) string {
	var he *sdk.HostError
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

// syncer applies the writes of one tick within the write budget.
type syncer struct {
	start      time.Time
	first      error // first error that is not an expected refusal
	stopped    bool  // rate limited or out of time: no more writes this tick
	blocked    map[string]bool
	refused    map[string]bool // IDs that answered not_owned
	blockNew   bool
	refusedNew bool
}

// ready reports whether another write may start.
func (s *syncer) ready() bool {
	if !s.stopped && now().Sub(s.start) >= writeBudget {
		s.stopped = true
	}
	return !s.stopped
}

// note classifies the error of a write on entry id: not_owned is remembered
// and skipped (the owner took the entry over, and the refusal spends a rate
// limit token), rate_limited ends the tick, anything else is returned at the end.
func (s *syncer) note(id string, err error) {
	switch hostCode(err) {
	case sdk.ErrNotOwned:
		s.refused[id], s.refusedNew = true, true
	case sdk.ErrRateLimited:
		s.stopped = true
	default:
		if s.first == nil {
			s.first = err
		}
	}
}

func attrsJSON(attrs map[string]string) json.RawMessage {
	raw, _ := json.Marshal(attrs)
	return raw
}

func (s *syncer) create(w want) {
	var out entity
	err := callHost(sdk.FnEntitiesCreate, map[string]any{"name": w.Name, "slug": w.Slug, "attributes": attrsJSON(w.Attrs)}, &out)
	if err != nil {
		s.note("", err)
		return
	}
	if out.Slug != w.Slug && out.ID != "" {
		// An entry the list did not show (an owner draft) held the slug: the
		// page would sit at the wrong URL, so remove it and stop trying.
		if err := callHost(sdk.FnEntitiesDelete, sdk.EntityDeleteArgs{ID: out.ID}, nil); err != nil {
			s.note(out.ID, err)
		}
		s.blocked[w.Slug], s.blockNew = true, true
	}
}

func (s *syncer) update(u update) {
	args := map[string]any{"id": u.ID, "name": u.Name, "attributes": attrsJSON(u.Attrs)}
	if err := callHost(sdk.FnEntitiesUpdate, args, nil); err != nil {
		s.note(u.ID, err)
	}
}

func (s *syncer) delete(id string) {
	if err := callHost(sdk.FnEntitiesDelete, sdk.EntityDeleteArgs{ID: id}, nil); err != nil {
		s.note(id, err)
	}
}

// syncCron keeps one registry-entry per registry name: creates the missing
// ones, updates those that changed and deletes those whose name left the
// registry. It writes only from a verified directory, so a registry outage
// never removes pages.
func syncCron(sdk.CronEvent) error {
	snap := views()
	ws, collisions, ok := desired(snap)
	if !ok {
		return nil
	}
	have, err := listOwn()
	if err != nil {
		return err
	}
	s := &syncer{start: now(), blocked: loadSet(blockedKey), refused: loadSet(refusedKey)}
	creates, updates, deletes := diff(ws, collisions, have, s.blocked, s.refused)
	if !snap.DevOK {
		deletes = nil // developer entries may be missing from the cards, not gone
	}
	// Removals first: a published page of a withdrawn plugin matters more than
	// a cosmetic update, and must not wait behind refused or rate limited writes.
	for _, id := range deletes {
		if s.ready() {
			s.delete(id)
		}
	}
	for _, w := range creates {
		if s.ready() {
			s.create(w)
		}
	}
	for _, u := range updates {
		if s.ready() {
			s.update(u)
		}
	}
	if s.blockNew {
		storeSet(blockedKey, s.blocked)
	}
	if s.refusedNew {
		storeSet(refusedKey, s.refused)
	}
	return s.first
}
