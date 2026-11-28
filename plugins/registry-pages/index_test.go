package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2030, 1, 5, 12, 0, 0, 0, time.UTC)

const testSHA = "0000000000000000000000000000000000000000000000000000000000000007"

// pubB64 is a valid signer key for developer entries.
func pubB64(seed byte) string {
	k := ed25519.NewKeyFromSeed(slices.Repeat([]byte{seed}, 32))
	return base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
}

// pkg builds a valid plugin entry map, overridden by kv pairs.
func pkg(kv ...any) map[string]any {
	m := map[string]any{"kind": "plugin", "name": "demo", "version": "1.0.0", "url": "p/demo.hmp", "sha256": testSHA}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// signedIndex signs an index of the given packages (and revocations) with the pin key.
func signedIndex(t *testing.T, issued, expires string, packages []map[string]any, revoked ...map[string]any) []byte {
	t.Helper()
	doc := map[string]any{"version": 1, "issued_at": issued, "expires_at": expires, "packages": packages}
	if len(revoked) > 0 {
		doc["revoked"] = revoked
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return sign(t, string(b), pinPriv())
}

func parseOK(t *testing.T, packages []map[string]any, revoked ...map[string]any) *index {
	t.Helper()
	doc := signedIndex(t, "2030-01-01T00:00:00Z", "2030-02-01T00:00:00Z", packages, revoked...)
	idx, err := parseIndex(doc, pinTrust(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestParseIndexRefusesOtherVersion(t *testing.T) {
	b := `{"version":2,"issued_at":"2030-01-01T00:00:00Z","expires_at":"2030-02-01T00:00:00Z","packages":[]}`
	if _, err := parseIndex(sign(t, b, pinPriv()), pinTrust(), testNow); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("err %v", err)
	}
}

func TestCheckFresh(t *testing.T) {
	cases := []struct {
		name, issued, expires string
		now                   time.Time
		ok                    bool
	}{
		{"malformed", "yesterday", "2030-02-01T00:00:00Z", testNow, false},
		{"expires before issued", "2030-02-01T00:00:00Z", "2030-01-01T00:00:00Z", testNow, false},
		{"lifetime over 180 days", "2030-01-01T00:00:00Z", "2030-07-01T00:00:00Z", testNow, false},
		{"lifetime 180 days", "2030-01-01T00:00:00Z", "2030-06-30T00:00:00Z", testNow, true},
		{"issued in the future", "2030-01-05T12:11:00Z", "2030-02-01T00:00:00Z", testNow, false},
		{"issued within skew", "2030-01-05T12:09:00Z", "2030-02-01T00:00:00Z", testNow, true},
		{"now equals expires", "2030-01-01T00:00:00Z", "2030-02-01T00:00:00Z", time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC), false},
		{"now before expires", "2030-01-01T00:00:00Z", "2030-02-01T00:00:00Z", time.Date(2030, 1, 31, 23, 59, 59, 0, time.UTC), true},
	}
	for _, c := range cases {
		if err := checkFresh(c.issued, c.expires, c.now); (err == nil) != c.ok {
			t.Errorf("%s: err %v, ok want %v", c.name, err, c.ok)
		}
	}
}

func TestParseIndexRefusesExpired(t *testing.T) {
	doc := signedIndex(t, "2030-01-01T00:00:00Z", "2030-02-01T00:00:00Z", nil)
	if _, err := parseIndex(doc, pinTrust(), time.Date(2030, 3, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expired index accepted")
	}
}

func TestParseIndexSanitisers(t *testing.T) {
	long := strings.Repeat("x", maxAbout+1)
	feats := []any{}
	for i := 0; i < 12; i++ {
		feats = append(feats, fmt.Sprintf("feature %d", i))
	}
	feats[0] = strings.Repeat("y", maxFeature+1)
	idx := parseOK(t, []map[string]any{
		pkg("name", "tier-bad", "tier", "platinum"),
		pkg("name", "dev-nokey", "tier", "developer"),
		pkg("name", "dev-key", "tier", "developer", "signer_key", pubB64(3)),
		pkg("name", "shot-bad", "screenshot", "../x.png"),
		pkg("name", "shot-ok", "kind", "theme", "screenshot", "screenshots/shot-ok.png"),
		pkg("name", "about-long", "about", long),
		pkg("name", "about-paras", "about", strings.Repeat("p\n", 10)+"p"),
		pkg("name", "about-ctrl", "about", "a\x07b"),
		pkg("name", "about-ok", "about", "one\ntwo"),
		pkg("name", "features", "features", feats),
		pkg("name", "Bad_Name"),
		pkg("name", "short-sha", "sha256", "abc"),
		pkg("name", "no-url", "url", ""),
		pkg("name", "a-kit", "kind", "kit"),
		pkg("name", "odd-kind", "kind", "widget"),
		pkg("name", "odd-members", "size", 1e3, "tags", "x", "site_type", 5, "flags", "dark", "category", []int{1}),
	})
	by := map[string]entry{}
	for _, e := range idx.Packages {
		by[e.Name] = e
	}
	for _, gone := range []string{"dev-nokey", "Bad_Name", "short-sha", "no-url", "odd-kind"} {
		if _, ok := by[gone]; ok {
			t.Errorf("%s kept", gone)
		}
	}
	for _, kept := range []string{"dev-key", "a-kit", "odd-members"} {
		if _, ok := by[kept]; !ok {
			t.Errorf("%s dropped", kept)
		}
	}
	if by["tier-bad"].Tier != "" {
		t.Errorf("unknown tier kept: %q", by["tier-bad"].Tier)
	}
	if by["shot-bad"].Screenshot != "" || by["shot-ok"].Screenshot != "screenshots/shot-ok.png" {
		t.Errorf("screenshots %q %q", by["shot-bad"].Screenshot, by["shot-ok"].Screenshot)
	}
	for _, n := range []string{"about-long", "about-paras", "about-ctrl"} {
		if by[n].About != "" {
			t.Errorf("%s about kept", n)
		}
	}
	if by["about-ok"].About != "one\ntwo" {
		t.Errorf("about %q", by["about-ok"].About)
	}
	f := by["features"].Features
	if len(f) != maxFeatures || f[0] != "feature 1" {
		t.Errorf("features %v", f)
	}
}

func TestParseIndexCleansRevocations(t *testing.T) {
	idx := parseOK(t, nil,
		map[string]any{"name": "old", "reason": "bad\x07 " + strings.Repeat("r", 300)},
		map[string]any{"signer_key": "not a key"},
		map[string]any{"signer_key": pubB64(4)},
	)
	if len(idx.Revoked) != 2 || len([]rune(idx.Revoked[0].Reason)) != maxReason || strings.Contains(idx.Revoked[0].Reason, "\x07") {
		t.Fatalf("revoked %+v", idx.Revoked)
	}
}

func TestFilterDeveloper(t *testing.T) {
	main := &index{Packages: []entry{{Kind: kindPlugin, Name: "shadowed"}}}
	dev := &index{Version: 1, IssuedAt: "i", ExpiresAt: "e", Packages: []entry{
		{Kind: kindPlugin, Name: "shadowed", SignerKey: pubB64(5)},
		{Kind: kindTheme, Name: "shadowed", SignerKey: pubB64(5), Tier: "community"},
		{Kind: kindPlugin, Name: "searchconsole", SignerKey: pubB64(5)},
		{Kind: kindPlugin, Name: "nokey"},
		{Kind: kindPlugin, Name: "fine", SignerKey: pubB64(5), Tier: "first-party"},
	}}
	out := filterDeveloper(dev, main)
	var got []string
	for _, e := range out.Packages {
		got = append(got, e.Kind+"/"+e.Name+"/"+e.Tier)
	}
	if want := []string{"theme/shadowed/developer", "plugin/fine/developer"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if out.ExpiresAt != "e" {
		t.Errorf("window lost: %+v", out)
	}
	if n := len(filterDeveloper(dev, nil).Packages); n != 3 {
		t.Errorf("nil main kept %d, want 3", n)
	}
}

func TestRevoked(t *testing.T) {
	key := pubB64(6)
	idx := &index{Revoked: []revocation{
		{SignerKey: key},
		{Name: "pinned", Version: "1.0.0"},
		{Name: "gone"},
	}}
	cases := []struct {
		e    entry
		want bool
	}{
		{entry{Name: "any", Version: "9", SignerKey: key}, true},
		{entry{Name: "pinned", Version: "1.0.0"}, true},
		{entry{Name: "pinned", Version: "1.0.1"}, false},
		{entry{Name: "gone", Version: "3.0.0"}, true},
		{entry{Name: "other", Version: "1.0.0", SignerKey: pubB64(8)}, false},
		{entry{Name: "other", Version: "1.0.0"}, false},
	}
	for _, c := range cases {
		if got := revoked(idx, c.e); got != c.want {
			t.Errorf("%+v: revoked %v, want %v", c.e, got, c.want)
		}
	}
	if revoked(nil, entry{Name: "gone"}) {
		t.Error("nil index revokes")
	}
}
