package main

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func syncHost(t *testing.T) (*fakeHost, *entityStore) {
	t.Helper()
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour,
		entry{Kind: kindPlugin, Name: "tagger", Title: "Tagger", Description: "Tags pages", Tier: tierFirstParty},
		entry{Kind: kindPlugin, Name: "mailer", Description: "Sends mail", Tier: tierCommunity, Author: "acme"},
		entry{Kind: kindTheme, Name: "boreal", Description: "Calm", Tier: tierFirstParty},
	)
	h.publish(developerURL, 24*time.Hour) // deletes need a merged developer index
	return h, newEntityStore(h)
}

func runSync(t *testing.T) error {
	t.Helper()
	return syncCron(sdk.CronEvent{})
}

func entityByName(s *entityStore, slug string) entity {
	for _, e := range s.items {
		if e.Slug == slug {
			return e
		}
	}
	return entity{}
}

func TestSyncFirstRunCreates(t *testing.T) {
	_, s := syncHost(t)
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if want := []string{"boreal", "mailer", "tagger"}; !slices.Equal(s.slugs(), want) {
		t.Fatalf("slugs %v", s.slugs())
	}
	tg := entityByName(s, "tagger")
	if tg.Name != "Tagger" || tg.Attributes["kind"] != "plugin" || tg.Attributes["meta_title"] != "Tagger plugin" ||
		tg.Attributes["meta_description"] != "Tags pages" || tg.Attributes["canonical_url"] != "/plugins/tagger" {
		t.Errorf("tagger %+v", tg)
	}
	bo := entityByName(s, "boreal")
	if bo.Name != "boreal" || bo.Attributes["kind"] != "theme" || bo.Attributes["meta_title"] != "boreal theme" || bo.Attributes["canonical_url"] != "/themes/boreal" {
		t.Errorf("boreal %+v", bo)
	}
	if len(tg.Attributes) != 4 {
		t.Errorf("attributes %v", tg.Attributes)
	}
}

func TestSyncSendsNoStatusAndNoParent(t *testing.T) {
	_, s := syncHost(t)
	s.errs = func(fn string, _ int, args map[string]any) error {
		for _, k := range []string{"status", "parent", "parent_id"} {
			if _, bad := args[k]; bad {
				t.Errorf("%s sends %q", fn, k)
			}
		}
		return nil
	}
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
}

func TestSyncSecondRunIsQuiet(t *testing.T) {
	_, s := syncHost(t)
	runSync(t)
	s.writes = 0
	if err := runSync(t); err != nil || s.writes != 0 {
		t.Fatalf("err %v, writes %d", err, s.writes)
	}
}

func TestSyncUpdatesAChangedEntryOnly(t *testing.T) {
	h, s := syncHost(t)
	runSync(t)
	s.writes, s.log = 0, nil
	h.publish(registryURL, 24*time.Hour,
		entry{Kind: kindPlugin, Name: "tagger", Title: "Tagger", Description: "Tags every page", Tier: tierFirstParty},
		entry{Kind: kindPlugin, Name: "mailer", Description: "Sends mail", Tier: tierCommunity, Author: "acme"},
		entry{Kind: kindTheme, Name: "boreal", Description: "Calm", Tier: tierFirstParty},
	)
	h.advance(dirTTL)
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.log, []string{"update tagger"}) {
		t.Fatalf("log %v", s.log)
	}
	if got := entityByName(s, "tagger").Attributes["meta_description"]; got != "Tags every page" {
		t.Errorf("description %v", got)
	}
}

func TestSyncDeletesANameThatLeftTheRegistry(t *testing.T) {
	h, s := syncHost(t)
	runSync(t)
	s.log = nil
	h.publish(registryURL, 24*time.Hour,
		entry{Kind: kindPlugin, Name: "tagger", Title: "Tagger", Description: "Tags pages", Tier: tierFirstParty},
		entry{Kind: kindTheme, Name: "boreal", Description: "Calm", Tier: tierFirstParty},
	)
	h.advance(dirTTL)
	runSync(t)
	if !slices.Equal(s.log, []string{"delete mailer"}) {
		t.Fatalf("log %v", s.log)
	}
}

func TestSyncWritesNothingWithoutAVerifiedIndex(t *testing.T) {
	h, s := syncHost(t)
	runSync(t)
	s.writes, s.counts = 0, map[string]int{}
	h.fail(registryURL)
	h.advance(24*time.Hour + time.Minute) // the last good index has expired too
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if s.writes != 0 || s.counts["entities.list"] != 0 || len(s.items) != 3 {
		t.Fatalf("writes %d, lists %d, items %d", s.writes, s.counts["entities.list"], len(s.items))
	}
}

func TestSyncEmptyThemesDeleteThemeEntries(t *testing.T) {
	h, s := syncHost(t)
	runSync(t)
	h.publish(registryURL, 24*time.Hour, entry{Kind: kindPlugin, Name: "tagger", Title: "Tagger", Description: "Tags pages", Tier: tierFirstParty},
		entry{Kind: kindPlugin, Name: "mailer", Description: "Sends mail", Tier: tierCommunity, Author: "acme"})
	h.advance(dirTTL)
	runSync(t)
	if want := []string{"mailer", "tagger"}; !slices.Equal(s.slugs(), want) {
		t.Fatalf("slugs %v", s.slugs())
	}
}

func TestSyncLeavesCollidingNamesAlone(t *testing.T) {
	h, s := syncHost(t)
	h.publish(registryURL, 24*time.Hour,
		entry{Kind: kindPlugin, Name: "twin", Description: "plugin twin", Tier: tierFirstParty},
		entry{Kind: kindTheme, Name: "twin", Description: "theme twin", Tier: tierFirstParty},
		entry{Kind: kindPlugin, Name: "tagger", Tier: tierFirstParty})
	runSync(t)
	if want := []string{"tagger"}; !slices.Equal(s.slugs(), want) {
		t.Fatalf("created %v", s.slugs())
	}
	// An existing entry of the colliding slug is neither updated nor deleted.
	s.seed(want{Slug: "twin", Name: "twin", Attrs: map[string]string{"kind": "plugin"}})
	s.writes = 0
	runSync(t)
	if s.writes != 0 || !slices.Contains(s.slugs(), "twin") {
		t.Fatalf("writes %d, slugs %v", s.writes, s.slugs())
	}
}

func TestSyncNotOwnedIsSkipped(t *testing.T) {
	h, s := syncHost(t)
	runSync(t)
	s.log = nil
	h.publish(registryURL, 24*time.Hour,
		entry{Kind: kindPlugin, Name: "tagger", Title: "Tagger", Description: "Changed", Tier: tierFirstParty},
		entry{Kind: kindPlugin, Name: "mailer", Description: "Changed too", Tier: tierCommunity, Author: "acme"},
	) // boreal left the registry
	h.advance(dirTTL)
	s.errs = func(fn string, n int, args map[string]any) error {
		if fn == "entities.update" && n == 1 || fn == "entities.delete" {
			return &sdk.HostError{Code: sdk.ErrNotOwned}
		}
		return nil
	}
	if err := runSync(t); err != nil {
		t.Fatalf("not_owned must not fail the tick: %v", err)
	}
	if !slices.Equal(s.log, []string{"update tagger"}) && !slices.Equal(s.log, []string{"update mailer"}) {
		t.Fatalf("the other update did not run: %v", s.log)
	}
	if !slices.Contains(s.slugs(), "boreal") {
		t.Fatal("a not_owned delete removed the entry")
	}
}

func TestSyncRateLimitedStopsTheTickAndResumes(t *testing.T) {
	_, s := syncHost(t)
	limited := true
	s.errs = func(fn string, n int, args map[string]any) error {
		if limited && fn == "entities.create" && n == 2 {
			return &sdk.HostError{Code: sdk.ErrRateLimited}
		}
		return nil
	}
	if err := runSync(t); err != nil {
		t.Fatalf("rate_limited must not fail the tick: %v", err)
	}
	if len(s.items) != 1 || s.counts["entities.create"] != 2 {
		t.Fatalf("items %d, creates %d (the tick must stop at the limit)", len(s.items), s.counts["entities.create"])
	}
	limited = false
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if len(s.items) != 3 {
		t.Fatalf("items %v", s.slugs())
	}
}

func TestSyncOtherErrorsContinueAndAreReturned(t *testing.T) {
	_, s := syncHost(t)
	s.errs = func(fn string, n int, args map[string]any) error {
		if fn == "entities.create" && n == 1 {
			return &sdk.HostError{Code: sdk.ErrInternal, Message: "x"}
		}
		return nil
	}
	if err := runSync(t); err == nil {
		t.Fatal("the first failure must be returned")
	}
	if len(s.items) != 2 {
		t.Fatalf("items %v", s.slugs())
	}
}

func TestSyncSlugCollisionIsDeletedAndBlocked(t *testing.T) {
	h, s := syncHost(t)
	s.slugFor = func(w string) string {
		if w == "tagger" {
			return "tagger-2"
		}
		return w
	}
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if want := []string{"boreal", "mailer"}; !slices.Equal(s.slugs(), want) {
		t.Fatalf("slugs %v", s.slugs())
	}
	if !slices.Contains(s.log, "delete tagger-2") {
		t.Fatalf("log %v", s.log)
	}
	if it := h.kv[blockedKey]; it.exp != h.clock.Add(6*time.Hour) {
		t.Errorf("blocked kv %+v", it)
	}
	s.counts, s.log = map[string]int{}, nil
	runSync(t)
	if s.counts["entities.create"] != 0 {
		t.Fatalf("blocked name retried: %v", s.log)
	}
}

func TestSyncReadsEveryPage(t *testing.T) {
	_, s := syncHost(t)
	s.pageSize = 200
	var ws []want
	for i := 0; i < 250; i++ {
		ws = append(ws, newWant(kindPlugin, "plugins", "old-"+strconv.Itoa(i), "", ""))
	}
	s.seed(ws...)
	have, err := listOwn()
	if err != nil || len(have) != 250 || s.counts["entities.list"] != 2 {
		t.Fatalf("read %d, lists %d, err %v", len(have), s.counts["entities.list"], err)
	}
}

func TestSyncWriteBudget(t *testing.T) {
	h, s := syncHost(t)
	s.errs = func(fn string, n int, args map[string]any) error {
		if fn == "entities.create" {
			h.advance(4 * time.Second) // each write is slow
		}
		return nil
	}
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if s.counts["entities.create"] != 2 {
		t.Fatalf("creates %d, want 2 (no write starts after 7 s)", s.counts["entities.create"])
	}
}

func TestSyncListErrorIsReturned(t *testing.T) {
	_, s := syncHost(t)
	s.errs = func(fn string, n int, args map[string]any) error {
		if fn == "entities.list" {
			return &sdk.HostError{Code: sdk.ErrInternal}
		}
		return nil
	}
	if err := runSync(t); err == nil || s.writes != 0 {
		t.Fatalf("err %v, writes %d", err, s.writes)
	}
}

func TestSyncKeepsEntriesWhileTheDeveloperIndexIsDown(t *testing.T) {
	h, s := syncHost(t)
	h.publish(developerURL, 24*time.Hour, devEntry("devplug", kindPlugin))
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.slugs(), "devplug") {
		t.Fatalf("developer entry not created: %v", s.slugs())
	}
	h.fail(developerURL)
	h.advance(dirTTL)
	s.counts = map[string]int{}
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if s.counts["entities.delete"] != 0 || !slices.Contains(s.slugs(), "devplug") {
		t.Fatalf("deletes %d, slugs %v", s.counts["entities.delete"], s.slugs())
	}
	h.publish(developerURL, 24*time.Hour) // back, without the entry
	h.advance(dirTTL)
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(s.slugs(), "devplug") {
		t.Fatalf("a real removal was not deleted: %v", s.slugs())
	}
}

func TestSyncRemembersNotOwnedAndStillDeletes(t *testing.T) {
	h := newFakeHost(t)
	h.publish(developerURL, 24*time.Hour)
	s := newEntityStore(h)
	var entries []entry
	for i := range 25 {
		name := "plug" + strconv.Itoa(100+i)
		entries = append(entries, entry{Kind: kindPlugin, Name: name, Description: "new", Tier: tierFirstParty})
		s.seed(newWant(kindPlugin, "plugins", name, "", "old")) // the owner changed these
	}
	s.seed(newWant(kindPlugin, "plugins", "gone", "", "x"))
	h.publish(registryURL, 24*time.Hour, entries...)

	// A burst of 20 writes per tick, refused ones included, as the host limits.
	var tokens int
	s.errs = func(fn string, n int, args map[string]any) error {
		if fn == "entities.list" {
			return nil
		}
		if tokens++; tokens > 20 {
			return &sdk.HostError{Code: sdk.ErrRateLimited}
		}
		if fn == "entities.update" {
			return &sdk.HostError{Code: sdk.ErrNotOwned}
		}
		return nil
	}
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(s.slugs(), "gone") {
		t.Fatal("the removed name was not deleted in the first tick")
	}
	for range 2 { // the refusals are remembered, so the other 5 are tried next
		tokens = 0
		h.advance(time.Minute)
		if err := runSync(t); err != nil {
			t.Fatal(err)
		}
	}
	tokens, s.counts = 0, map[string]int{}
	h.advance(time.Minute)
	if err := runSync(t); err != nil {
		t.Fatal(err)
	}
	if s.counts["entities.update"] != 0 || s.counts["entities.delete"] != 0 {
		t.Fatalf("refused entries were tried again: %v", s.counts)
	}
}
