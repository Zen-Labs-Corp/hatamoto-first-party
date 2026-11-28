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
	blockedKey  = "sync_blocked"
	blockedTTL  = 6 * 60 * 60 // seconds
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
// are never created again; colliding names are never touched.
func diff(ws map[string]want, collisions map[string]bool, have []entity, blocked map[string]bool) (creates []want, updates []update, deletes []string) {
	byslug := map[string]entity{}
	for _, e := range have {
		byslug[e.Slug] = e
	}
	for slug, w := range ws {
		e, exists := byslug[slug]
		switch {
		case !exists && !blocked[slug]:
			creates = append(creates, w)
		case exists && e.differs(w):
			updates = append(updates, update{ID: e.ID, want: w})
		}
	}
	for _, e := range have {
		if _, keep := ws[e.Slug]; !keep && !collisions[e.Slug] {
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

// loadBlocked reads the names whose create came back under another slug.
func loadBlocked() map[string]bool {
	out := map[string]bool{}
	var res struct {
		Found bool     `json:"found"`
		Value []string `json:"value"`
	}
	if err := callHost(sdk.FnKVGet, map[string]string{"key": blockedKey}, &res); err != nil || !res.Found {
		return out
	}
	for _, n := range res.Value {
		out[n] = true
	}
	return out
}

func storeBlocked(blocked map[string]bool) {
	names := make([]string, 0, len(blocked))
	for n := range blocked {
		names = append(names, n)
	}
	slices.Sort(names)
	_ = callHost(sdk.FnKVSet, map[string]any{"key": blockedKey, "value": names, "ttl_seconds": blockedTTL}, nil)
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
	start    time.Time
	first    error // first error that is not an expected refusal
	stopped  bool  // rate limited or out of time: no more writes this tick
	blocked  map[string]bool
	blockNew bool
}

// ready reports whether another write may start.
func (s *syncer) ready() bool {
	if !s.stopped && now().Sub(s.start) >= writeBudget {
		s.stopped = true
	}
	return !s.stopped
}

// note classifies the error of a write: not_owned is skipped (the owner took
// the entry over), rate_limited ends the tick, anything else is remembered.
func (s *syncer) note(err error) {
	switch hostCode(err) {
	case sdk.ErrNotOwned:
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
		s.note(err)
		return
	}
	if out.Slug != w.Slug && out.ID != "" {
		// An entry the list did not show (an owner draft) held the slug: the
		// page would sit at the wrong URL, so remove it and stop trying.
		if err := callHost(sdk.FnEntitiesDelete, sdk.EntityDeleteArgs{ID: out.ID}, nil); err != nil {
			s.note(err)
		}
		s.blocked[w.Slug], s.blockNew = true, true
	}
}

func (s *syncer) update(u update) {
	args := map[string]any{"id": u.ID, "name": u.Name, "attributes": attrsJSON(u.Attrs)}
	if err := callHost(sdk.FnEntitiesUpdate, args, nil); err != nil {
		s.note(err)
	}
}

func (s *syncer) delete(id string) {
	if err := callHost(sdk.FnEntitiesDelete, sdk.EntityDeleteArgs{ID: id}, nil); err != nil {
		s.note(err)
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
	s := &syncer{start: now(), blocked: loadBlocked()}
	creates, updates, deletes := diff(ws, collisions, have, s.blocked)
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
	if !snap.DevOK {
		deletes = nil // developer entries may be missing from the cards, not gone
	}
	for _, id := range deletes {
		if s.ready() {
			s.delete(id)
		}
	}
	if s.blockNew {
		storeBlocked(s.blocked)
	}
	return s.first
}
