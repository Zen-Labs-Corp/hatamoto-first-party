package main

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func plug(name string, kv ...any) entry {
	e := entry{Kind: kindPlugin, Name: name, Tier: tierFirstParty}
	for i := 0; i+1 < len(kv); i += 2 {
		switch kv[i] {
		case "tier":
			e.Tier = kv[i+1].(string)
		case "author":
			e.Author = kv[i+1].(string)
		case "signer":
			e.SignerKey = kv[i+1].(string)
		case "kind":
			e.Kind = kv[i+1].(string)
		case "version":
			e.Version = kv[i+1].(string)
		}
	}
	return e
}

func devEntry(name, kind string) entry {
	return entry{Kind: kind, Name: name, Tier: tierDeveloper, Author: "Dev", SignerKey: pubB64(3)}
}

func pluginNames(s snapshot) []string {
	var out []string
	for _, c := range s.Plugins.Cards {
		out = append(out, c.Name)
	}
	return out
}

func TestViewsEmptyIndex(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, time.Hour)
	s := views()
	if s.Plugins.State != stateEmpty || s.Themes.State != stateEmpty {
		t.Fatalf("states %q %q", s.Plugins.State, s.Themes.State)
	}
}

func TestViewsCacheForThreeMinutes(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour, plug("zeta"))
	if got := pluginNames(views()); !slices.Equal(got, []string{"zeta"}) {
		t.Fatalf("cards %v", got)
	}
	h.advance(dirTTL - time.Second)
	views()
	if h.hits[registryURL] != 1 {
		t.Fatalf("fetches within the TTL = %d, want 1", h.hits[registryURL])
	}
	h.publish(registryURL, 24*time.Hour, plug("zeta"), plug("alpha"))
	h.advance(time.Second)
	if got := pluginNames(views()); !slices.Equal(got, []string{"alpha", "zeta"}) || h.hits[registryURL] != 2 {
		t.Fatalf("after the TTL: cards %v, fetches %d", got, h.hits[registryURL])
	}
}

func TestViewsUnavailable(t *testing.T) {
	h := newFakeHost(t)
	other := h.devPriv // not the main signer
	cases := map[string]func(){
		"http error": func() { h.fail(registryURL) },
		"host error": func() { h.pages[registryURL] = &page{err: errHostDown} },
		"untrusted": func() {
			h.publish(registryURL, time.Hour, plug("alpha"))
			h.mainPriv = other
			h.publish(registryURL, time.Hour, plug("alpha"))
		},
		"expired": func() {
			h.publishWindow(registryURL, h.clock.Add(-2*time.Hour), h.clock.Add(-time.Hour), nil, plug("alpha"))
		},
		"truncated":  func() { h.publish(registryURL, time.Hour, plug("alpha")); h.pages[registryURL].truncated = true },
		"not served": func() { delete(h.pages, registryURL) },
		"garbage":    func() { h.pages[registryURL] = &page{status: 200, body: []byte("<html>")} },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			saved := h.mainPriv
			defer func() { h.mainPriv = saved }()
			mem = snapshot{}
			h.kv = map[string]kvItem{}
			setup()
			s := views()
			if s.Plugins.State != stateUnavailable || s.Themes.State != stateUnavailable || len(s.Plugins.Cards) != 0 {
				t.Fatalf("snapshot %+v", s)
			}
		})
	}
}

func TestViewsBase64BodyAccepted(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, time.Hour, plug("zeta"))
	h.pages[registryURL].b64 = true
	if got := pluginNames(views()); !slices.Equal(got, []string{"zeta"}) {
		t.Fatalf("cards %v", got)
	}
}

func TestViewsStaleWhileErrorBoundedByExpiry(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 10*time.Minute, plug("zeta"))
	views()
	h.fail(registryURL)
	h.advance(dirTTL)
	s := views() // refresh fails: last good kept
	if !slices.Equal(pluginNames(s), []string{"zeta"}) || h.hits[registryURL] != 2 {
		t.Fatalf("kept cards %v, fetches %d", pluginNames(s), h.hits[registryURL])
	}
	h.advance(failureTTL - time.Second)
	views()
	if h.hits[registryURL] != 2 {
		t.Fatalf("retried within the failure TTL: %d fetches", h.hits[registryURL])
	}
	h.advance(time.Second)
	views()
	if h.hits[registryURL] != 3 {
		t.Fatalf("no retry after the failure TTL: %d fetches", h.hits[registryURL])
	}
	// Never past the index's expiry: the retry that lands after it is unavailable.
	h.advance(8*time.Minute + 30*time.Second) // 11.5 min after publish, expiry at 10
	s = views()
	if s.Plugins.State != stateUnavailable {
		t.Fatalf("served an expired index: %+v", s.Plugins)
	}
}

func TestViewsNeverServedAtExpiry(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, time.Minute, plug("zeta"))
	s := views()
	if want := h.clock.Add(time.Minute); !s.IndexExpires.Equal(want) || !s.Until.Equal(want) {
		t.Fatalf("until %v, expires %v, want %v", s.Until, s.IndexExpires, want)
	}
	h.fail(registryURL)
	h.advance(time.Minute) // now == expires_at
	if s := views(); s.Plugins.State != stateUnavailable {
		t.Fatalf("served at expiry: %+v", s.Plugins)
	}
}

func TestViewsFailureIsRemembered(t *testing.T) {
	h := newFakeHost(t)
	h.fail(registryURL)
	views()
	h.advance(failureTTL - time.Second)
	views()
	if h.hits[registryURL] != 1 {
		t.Fatalf("fetches = %d, want 1", h.hits[registryURL])
	}
	h.advance(time.Second)
	views()
	if h.hits[registryURL] != 2 {
		t.Fatalf("fetches = %d, want 2", h.hits[registryURL])
	}
}

func TestViewsFetchBudget(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, time.Hour, plug("alpha"))
	h.publish(developerURL, time.Hour, devEntry("dev-one", kindPlugin))
	views()
	if got := h.timeout[registryURL]; len(got) != 1 || got[0] != 5000 {
		t.Fatalf("main timeout_ms %v, want [5000]", got)
	}
	if got := h.timeout[developerURL]; len(got) != 1 || got[0] > 5000 || got[0] < 1 {
		t.Fatalf("developer timeout_ms %v", got)
	}
}

func TestViewsDeveloperGetsTheRemainder(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, time.Hour, plug("alpha"))
	h.publish(developerURL, time.Hour, devEntry("dev-one", kindPlugin))
	inner := callHost
	callHost = func(fn string, args, out any) error {
		if fn == sdk.FnHTTPFetch && h.hits[registryURL] == 0 { // the main fetch uses 2 s
			defer h.advance(2 * time.Second)
		}
		return inner(fn, args, out)
	}
	views()
	if got := h.timeout[developerURL]; len(got) != 1 || got[0] != 3000 {
		t.Fatalf("developer timeout_ms %v, want [3000]", got)
	}
}

func TestViewsDeveloperSkippedWhenBudgetSpent(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, time.Hour, plug("alpha"))
	h.publish(developerURL, time.Hour, devEntry("dev-one", kindPlugin))
	inner := callHost
	callHost = func(fn string, args, out any) error {
		if fn == sdk.FnHTTPFetch && h.hits[registryURL] == 0 { // the main fetch burns the whole budget
			defer h.advance(fetchBudget)
		}
		return inner(fn, args, out)
	}
	if got := pluginNames(views()); !slices.Equal(got, []string{"alpha"}) || h.hits[developerURL] != 0 {
		t.Fatalf("cards %v, developer fetches %d", got, h.hits[developerURL])
	}
}

func TestViewsSharedThroughKV(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, time.Hour, plug("zeta"))
	views()
	h.newInstance()
	if got := pluginNames(views()); !slices.Equal(got, []string{"zeta"}) {
		t.Fatalf("cards %v", got)
	}
	if h.hits[registryURL] != 1 {
		t.Fatalf("fetches = %d, want 1 (second instance reads kv)", h.hits[registryURL])
	}
	if it := h.kv[snapshotKey]; it.exp != h.clock.Add(time.Hour) {
		t.Errorf("kv ttl: expires %v, want %v", it.exp, h.clock.Add(time.Hour))
	}
}

func TestViewsKVErrorsAreIgnored(t *testing.T) {
	h := newFakeHost(t)
	h.kvErr = errHostDown
	h.publish(registryURL, time.Hour, plug("zeta"))
	if got := pluginNames(views()); !slices.Equal(got, []string{"zeta"}) {
		t.Fatalf("cards %v", got)
	}
	views()
	if h.hits[registryURL] != 1 {
		t.Fatalf("fetches = %d, want 1 (memory serves)", h.hits[registryURL])
	}
}

func TestViewsKVFailureSnapshotIsShared(t *testing.T) {
	h := newFakeHost(t)
	h.fail(registryURL)
	views()
	h.newInstance()
	views()
	if h.hits[registryURL] != 1 {
		t.Fatalf("fetches = %d, want 1 (failure shared through kv)", h.hits[registryURL])
	}
}

func TestViewsDeveloperMergedAndOrdered(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour, plug("zeta"), plug("comm", "tier", tierCommunity, "author", "acme"))
	h.publish(developerURL, 24*time.Hour, devEntry("aaa-dev", kindPlugin), devEntry("shiny", kindTheme))
	s := views()
	if got := pluginNames(s); !slices.Equal(got, []string{"zeta", "comm", "aaa-dev"}) {
		t.Fatalf("plugins %v", got)
	}
	if len(s.Themes.Cards) != 1 || s.Themes.Cards[0].Name != "shiny" || s.Themes.Cards[0].TierLabel != "Developer" {
		t.Fatalf("themes %+v", s.Themes)
	}
}

func TestViewsDeveloperFailureKeepsMain(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour, plug("zeta"))
	h.fail(developerURL)
	if s := views(); s.Plugins.State != stateOK || !slices.Equal(pluginNames(s), []string{"zeta"}) {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestViewsDeveloperExpiredIsIgnored(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour, plug("zeta"))
	h.publishWindow(developerURL, h.clock.Add(-2*time.Hour), h.clock.Add(-time.Hour), nil, devEntry("dev-one", kindPlugin))
	if got := pluginNames(views()); !slices.Equal(got, []string{"zeta"}) {
		t.Fatalf("plugins %v", got)
	}
}

func TestViewsOnlyDeveloperEntriesIsOK(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour)
	h.publish(developerURL, 24*time.Hour, devEntry("dev-one", kindPlugin))
	s := views()
	if s.Plugins.State != stateOK || s.Themes.State != stateEmpty {
		t.Fatalf("states %q %q", s.Plugins.State, s.Themes.State)
	}
}

func TestViewsDeveloperRevokedIsHidden(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour)
	h.publishWindow(developerURL, h.clock.Add(-time.Minute), h.clock.Add(24*time.Hour),
		[]revocation{{Name: "bad"}, {SignerKey: pubB64(9)}},
		devEntry("bad", kindPlugin), devEntry("good", kindPlugin),
		entry{Kind: kindPlugin, Name: "badkey", Tier: tierDeveloper, SignerKey: pubB64(9)})
	if got := pluginNames(views()); !slices.Equal(got, []string{"good"}) {
		t.Fatalf("plugins %v", got)
	}
}

func TestViewsMergedExpiryIsTheEarlier(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour, plug("zeta"))
	h.publish(developerURL, 2*time.Hour, devEntry("dev-one", kindPlugin))
	s := views()
	if want := h.clock.Add(2 * time.Hour); !s.IndexExpires.Equal(want) {
		t.Fatalf("IndexExpires %v, want %v", s.IndexExpires, want)
	}
}

func TestViewsDeveloperDropsShadowingAndFirstParty(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour, plug("zeta"))
	h.publish(developerURL, 24*time.Hour, devEntry("zeta", kindPlugin), devEntry("searchconsole", kindPlugin), devEntry("zeta", kindTheme))
	s := views()
	if got := pluginNames(s); !slices.Equal(got, []string{"zeta"}) || s.Plugins.Cards[0].Tier != tierFirstParty {
		t.Fatalf("plugins %v", got)
	}
	if len(s.Themes.Cards) != 1 {
		t.Fatalf("a theme may reuse a plugin name: %+v", s.Themes)
	}
}

func TestViewsNeverPanics(t *testing.T) {
	h := newFakeHost(t)
	callHost = func(string, any, any) error { panic("boom") }
	_ = h
	if s := views(); s.Plugins.State != stateUnavailable {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestViewsSharedFailureDoesNotReplaceGoodMemory(t *testing.T) {
	h := newFakeHost(t)
	h.publish(registryURL, time.Hour, plug("zeta"))
	views()
	h.advance(dirTTL)
	raw, err := json.Marshal(unavailable(h.clock)) // another instance failed just now
	if err != nil {
		t.Fatal(err)
	}
	h.kv[snapshotKey] = kvItem{value: raw, exp: h.clock.Add(failureTTL)}
	h.fail(registryURL)
	s := views()
	if !slices.Equal(pluginNames(s), []string{"zeta"}) || h.hits[registryURL] != 1 {
		t.Fatalf("cards %v, fetches %d", pluginNames(s), h.hits[registryURL])
	}
	h.advance(failureTTL + time.Second)
	if s := views(); !slices.Equal(pluginNames(s), []string{"zeta"}) {
		t.Fatalf("after the failure TTL: cards %v", pluginNames(s))
	}
}
