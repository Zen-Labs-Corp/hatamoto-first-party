package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"hatamoto.dev/sdk"
)

// Cache timing, the same as core's directory: a registry publish shows within
// minutes, a failed fetch is remembered only briefly, and an index is never
// served at or after its own expires_at.
const (
	dirTTL      = 3 * time.Minute
	fetchBudget = 5 * time.Second
	failureTTL  = 15 * time.Second
	snapshotKey = "snapshot"
)

// now is the clock; tests swap it.
var now = time.Now

// snapshot is the verified directory of both kinds and how long it may be served.
type snapshot struct {
	Plugins      pluginDir `json:"plugins"`
	Themes       themeDir  `json:"themes"`
	Until        time.Time `json:"until"`         // serve without a refresh before this
	IndexExpires time.Time `json:"index_expires"` // expires_at of the index behind the cards; zero when unavailable
}

// mem is this wasm instance's copy; kv shares the snapshot between instances.
var mem snapshot

type fetchArgs struct {
	Method    string `json:"method"`
	URL       string `json:"url"`
	TimeoutMS int    `json:"timeout_ms"`
}

type fetchResp struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	BodyB64   string `json:"body_b64"`
	Truncated bool   `json:"truncated"`
}

func unavailable(t time.Time) snapshot {
	return snapshot{
		Plugins: pluginDir{State: stateUnavailable},
		Themes:  themeDir{State: stateUnavailable},
		Until:   t.Add(failureTTL),
	}
}

// verified reports whether the snapshot holds cards of a verified index.
func (s snapshot) verified() bool { return !s.IndexExpires.IsZero() }

// views returns the directory for both kinds. It never fails and never
// panics: every problem is the unavailable state.
func views() (s snapshot) {
	t := now()
	defer func() {
		if p := recover(); p != nil {
			s = unavailable(t)
		}
	}()
	if t.Before(mem.Until) {
		return mem
	}
	shared, found := loadSnapshot()
	if found && t.Before(shared.Until) {
		mem = shared
		return mem
	}
	last := mem
	if found && shared.IndexExpires.After(last.IndexExpires) {
		last = shared
	}
	s = refresh(t, last)
	mem = s
	storeSnapshot(s, t)
	return s
}

// refresh fetches once. On success the new cards are served for up to dirTTL
// (never past the index's expiry). On failure the last good cards are kept
// until their own expiry and retried after failureTTL; without them both kinds
// are unavailable.
func refresh(t time.Time, last snapshot) snapshot {
	idx, err := fetchDirectory(t.Add(fetchBudget))
	if err == nil {
		var exp time.Time
		if exp, err = time.Parse(time.RFC3339, idx.ExpiresAt); err == nil {
			t = now()
			pd, td := buildDirs(idx)
			return snapshot{Plugins: pd, Themes: td, Until: minTime(t.Add(dirTTL), exp), IndexExpires: exp}
		}
	}
	t = now()
	if last.verified() && last.IndexExpires.After(t) {
		last.Until = minTime(t.Add(failureTTL), last.IndexExpires)
		return last
	}
	return unavailable(t)
}

// fetchDirectory reads the main index and merges the developer index into it.
func fetchDirectory(deadline time.Time) (idx *index, err error) {
	defer func() {
		if p := recover(); p != nil {
			idx, err = nil, fmt.Errorf("registry fetch panicked: %v", p)
		}
	}()
	main, err := fetchIndex(registryURL, mainTrust, deadline)
	if err != nil {
		return nil, err
	}
	return withDeveloper(main, deadline), nil
}

// fetchIndex GETs a signed index within the remaining budget and verifies it.
func fetchIndex(url string, trust trustSet, deadline time.Time) (*index, error) {
	remaining := deadline.Sub(now())
	if remaining <= 0 {
		return nil, errors.New("registry fetch: out of time")
	}
	var res fetchResp
	args := fetchArgs{Method: "GET", URL: url, TimeoutMS: int(max(remaining.Milliseconds(), 1))}
	if err := callHost(sdk.FnHTTPFetch, args, &res); err != nil {
		return nil, fmt.Errorf("registry fetch: %w", err)
	}
	if res.Status != 200 {
		return nil, fmt.Errorf("registry fetch: status %d", res.Status)
	}
	if res.Truncated {
		return nil, errors.New("registry fetch: response too large")
	}
	body := []byte(res.Body)
	if res.BodyB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(res.BodyB64)
		if err != nil {
			return nil, fmt.Errorf("registry fetch: %w", err)
		}
		body = raw
	}
	return parseIndex(body, trust, now())
}

// withDeveloper adds the developer entries to main. The developer index is
// optional: when it is unreadable or expired, main is returned as is.
// Otherwise the result expires with the earlier of the two indexes.
func withDeveloper(main *index, deadline time.Time) *index {
	dev, err := fetchIndex(developerURL, devTrust, deadline)
	if err != nil {
		return main
	}
	mainExp, err1 := time.Parse(time.RFC3339, main.ExpiresAt)
	devExp, err2 := time.Parse(time.RFC3339, dev.ExpiresAt)
	if err1 != nil || err2 != nil || !devExp.After(now()) {
		return main
	}
	merged := *main
	merged.Packages = slices.Clone(main.Packages)
	for _, e := range filterDeveloper(dev, main).Packages {
		if !revoked(dev, e) {
			merged.Packages = append(merged.Packages, e)
		}
	}
	if devExp.Before(mainExp) {
		merged.ExpiresAt = dev.ExpiresAt
	}
	return &merged
}

// loadSnapshot reads the shared snapshot; any problem is "not found".
func loadSnapshot() (s snapshot, found bool) {
	var res struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := callHost(sdk.FnKVGet, map[string]string{"key": snapshotKey}, &res); err != nil || !res.Found {
		return snapshot{}, false
	}
	if err := json.Unmarshal(res.Value, &s); err != nil {
		return snapshot{}, false
	}
	return s, true
}

// storeSnapshot shares s with the other instances. It lives as long as the
// index it holds (an unavailable one only failureTTL); errors are ignored.
func storeSnapshot(s snapshot, t time.Time) {
	ttl := int(failureTTL / time.Second)
	if s.verified() {
		ttl = max(1, int(math.Ceil(s.IndexExpires.Sub(t).Seconds())))
	}
	_ = callHost(sdk.FnKVSet, map[string]any{"key": snapshotKey, "value": s, "ttl_seconds": ttl}, nil)
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
