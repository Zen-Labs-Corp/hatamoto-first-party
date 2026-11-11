package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/registry"
)

// listingKey makes a key pair, points listingPubB64 at it and returns the
// private key in both accepted setting formats.
func listingKey(t *testing.T) (priv ed25519.PrivateKey, file, bare string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := listingPubB64
	t.Cleanup(func() { listingPubB64 = old })
	listingPubB64 = base64.StdEncoding.EncodeToString(pub)
	bare = base64.StdEncoding.EncodeToString(priv)
	doc, _ := json.Marshal(map[string]string{"key_id": registry.KeyID(pub), "public": listingPubB64, "private": bare})
	return priv, string(doc), bare
}

func TestParseListingKey(t *testing.T) {
	_, file, bare := listingKey(t)
	for name, in := range map[string]string{"key file": file, "bare": bare, "padded": "\n " + bare + "\n"} {
		if _, err := parseListingKey(in); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, otherFile, _ := listingKey(t) // a valid key, but not the one core trusts
	listingPubB64 = base64.StdEncoding.EncodeToString(make([]byte, 32))
	for name, in := range map[string]string{"empty": "", "wrong key": otherFile, "not base64": "!!!", "short": "AQID", "bad json": "{"} {
		_, err := parseListingKey(in)
		if err == nil {
			t.Errorf("%s accepted", name)
			continue
		}
		if strings.Contains(err.Error(), bare) || strings.Contains(err.Error(), in) && in != "" {
			t.Errorf("%s: error carries the input: %v", name, err)
		}
	}
}

func TestEntryFrom(t *testing.T) {
	s := submission{Kind: "plugin", Name: "acme-seo", Version: "1.2.0", URL: releaseURL, SHA256: "ab", Size: 5, Permissions: []string{"db"},
		Source: "https://github.com/acme/acme-seo", About: "Fast", Category: "SEO", SiteType: "blog", Screenshot: "https://example.org/s.png", Description: "d"}
	e := entryFrom(s, "acme", "PUB")
	if e.Kind != "plugin" || e.Title != "acme-seo" || e.Author != "acme" || e.Tier != "developer" || e.SignerKey != "PUB" || e.Category != "SEO" ||
		e.SiteType != "" || e.Screenshot != "" || e.Description != "d" || e.SHA256 != "ab" || e.Size != 5 || e.About != "Fast" || e.Source == "" {
		t.Errorf("plugin entry %+v", e)
	}
	s.Kind, s.Title = "theme", "Acme"
	e = entryFrom(s, "acme", "PUB")
	if e.Title != "Acme" || e.SiteType != "blog" || e.Category != "" || e.Screenshot != "https://example.org/s.png" {
		t.Errorf("theme entry %+v", e)
	}
	s.Screenshot = "http://example.org/s.png"
	if entryFrom(s, "acme", "PUB").Screenshot != "" {
		t.Error("a non https screenshot was kept")
	}
}

// indexHost answers the index queries and records the published row.
type indexHost struct {
	*fakeHost
	entries, revs string
	published     []any
}

func newIndexHost(t *testing.T, key string) *indexHost {
	t.Helper()
	h := &indexHost{entries: `[]`, revs: `[]`}
	h.fakeHost = &fakeHost{settings: `{"listing_key":` + jsonStr(key) + `}`, db: func(c hostCall, out any) error {
		switch {
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM listings l JOIN accounts a"):
			return cell(out, h.entries)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM revocations"):
			return cell(out, h.revs)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM published"):
			return cell(out, h.entries)
		case c.fn == "db.exec" && strings.HasPrefix(c.sql, "INSERT INTO published"):
			h.published = c.args
		}
		return affected(out, 1)
	}}
	h.install(t)
	return h
}

func jsonStr(v any) string { b, _ := json.Marshal(v); return string(b) }

// execs returns the recorded statements that start with prefix.
func (h *indexHost) execs(prefix string) []hostCall {
	var out []hostCall
	for _, c := range h.calls {
		if c.fn == "db.exec" && strings.HasPrefix(c.sql, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func TestPublishSignsTheIndexAndStoresTheEnvelope(t *testing.T) {
	fixedNow(t, t0)
	priv, file, _ := listingKey(t)
	h := newIndexHost(t, file)
	h.entries = `[{"kind":"plugin","name":"acme-seo","version":"1.2.0","url":"` + releaseURL + `","sha256":"ab","tier":"developer","signer_key":"PUB"}]`
	long := strings.Repeat("é", 250)
	h.revs = `[{"signer_key":"","name":"acme-seo","version":"1.0.0","reason":"bad"},{"signer_key":"KEY","name":"","version":"","reason":"` + long + `"},` +
		`{"signer_key":"KEY","name":"acme-seo","version":"0.9.0","reason":"x"}]`
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	if len(h.published) != 3 {
		t.Fatalf("published %v", h.published)
	}
	body := h.published[0].(string)
	var env struct {
		Index     json.RawMessage `json:"index"`
		Signer    string          `json:"signer"`
		Algorithm string          `json:"algorithm"`
		Signature string          `json:"signature"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	sig, _ := base64.StdEncoding.DecodeString(env.Signature)
	var compact bytes.Buffer // the signature covers the compact form of the index
	if err := json.Compact(&compact, env.Index); err != nil {
		t.Fatal(err)
	}
	if env.Signer != registry.KeyID(priv.Public().(ed25519.PublicKey)) || env.Algorithm != "ed25519" || !ed25519.Verify(priv.Public().(ed25519.PublicKey), compact.Bytes(), sig) {
		t.Fatalf("envelope does not verify: %s", body)
	}
	var idx registry.Index
	if err := json.Unmarshal(env.Index, &idx); err != nil {
		t.Fatal(err)
	}
	issued, _ := time.Parse(time.RFC3339, idx.IssuedAt)
	expires, _ := time.Parse(time.RFC3339, idx.ExpiresAt)
	if idx.Version != 1 || !issued.Equal(t0.Add(-time.Minute)) || expires.Sub(issued) != 30*24*time.Hour+time.Minute || h.published[1] != idx.IssuedAt || h.published[2] != idx.ExpiresAt {
		t.Errorf("index %+v published %v", idx, h.published)
	}
	if len(idx.Packages) != 1 || idx.Packages[0].Name != "acme-seo" || idx.Packages[0].Tier != "developer" {
		t.Errorf("packages %+v", idx.Packages)
	}
	if len(idx.Revoked) != 3 || idx.Revoked[0].Name != "acme-seo" || idx.Revoked[0].Version != "1.0.0" || idx.Revoked[0].SignerKey != "" {
		t.Errorf("revoked %+v", idx.Revoked)
	}
	if idx.Revoked[1].SignerKey != "KEY" || idx.Revoked[1].Name != "" || len([]rune(idx.Revoked[1].Reason)) != 200 {
		t.Errorf("key revocation %+v", idx.Revoked[1])
	}
	if idx.Revoked[2].SignerKey != "" || idx.Revoked[2].Version != "0.9.0" {
		t.Errorf("a version revocation must not carry the signer key: %+v", idx.Revoked[2])
	}
	requireGuard(t, h.fakeHost)
}

func TestPublishWithoutListings(t *testing.T) {
	_, file, _ := listingKey(t)
	h := newIndexHost(t, file)
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.published[0].(string), `"packages": []`) {
		t.Errorf("empty index must list [] not null: %v", h.published[0])
	}
}

func TestPublishRefusals(t *testing.T) {
	listingKey(t)
	for name, c := range map[string]struct {
		key  string
		want error
	}{
		"no key":  {"", errNoListingKey},
		"bad key": {"AQID", errBadListing},
	} {
		h := newIndexHost(t, c.key)
		if err := publish(); err != c.want || h.published != nil {
			t.Errorf("%s: %v published %v", name, err, h.published)
		}
	}
	// A key that is not the one core trusts.
	_, otherFile, _ := listingKey(t)
	listingPubB64 = base64.StdEncoding.EncodeToString(make([]byte, 32))
	h := newIndexHost(t, otherFile)
	if err := publish(); err != errWrongListing || h.published != nil {
		t.Errorf("wrong key: %v", err)
	}
	// Too big.
	_, file, _ := listingKey(t)
	h = newIndexHost(t, file)
	h.entries = `[` + strings.TrimSuffix(strings.Repeat(`{"kind":"plugin","name":"x-`+strings.Repeat("a", 500)+`","version":"1","url":"u","sha256":"s"},`, 2000), ",") + `]`
	if err := publish(); err != errIndexTooBig || h.published != nil {
		t.Errorf("big index: %v", err)
	}
}

func TestSettingNeverReachesTheLog(t *testing.T) {
	_, file, bare := listingKey(t)
	h := newIndexHost(t, file)
	x := &hostX{}
	x.install(t)
	// a key that is wrong for core: the log line names the problem, not the key
	listingPubB64 = base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := resignCron(sdk.CronEvent{}); err == nil {
		t.Fatal("expected an error")
	}
	for _, l := range x.logs {
		if strings.Contains(l, bare) || strings.Contains(l, file) {
			t.Errorf("log carries the key: %s", l)
		}
	}
	if len(x.logs) != 1 || h.published != nil {
		t.Errorf("logs %v", x.logs)
	}
}

func TestResignCronPublishes(t *testing.T) {
	_, file, _ := listingKey(t)
	h := newIndexHost(t, file)
	if err := resignCron(sdk.CronEvent{}); err != nil || h.published == nil {
		t.Fatalf("%v %v", err, h.published)
	}
}

func TestHandleRegistry(t *testing.T) {
	h := newIndexHost(t, "")
	h.entries = `[]`
	r := handleRegistry()
	if r.Status != 503 || !strings.Contains(r.Body, "not published yet") || !strings.HasPrefix(r.Headers["Content-Type"], "text/plain") {
		t.Errorf("nothing published: %+v", r)
	}
	h.entries = `[{"body":"{\"index\":{}}"}]`
	r = handleRegistry()
	if r.Status != 200 || r.Body != `{"index":{}}` || r.Headers["Content-Type"] != "application/json" || r.Headers["Cache-Control"] != "public, max-age=300" {
		t.Errorf("published: %+v", r)
	}
	h.db = func(hostCall, any) error { return errDB }
	if r := handleRegistry(); r.Status != 503 || strings.Contains(r.Body, "db down") {
		t.Errorf("db error: %+v", r)
	}
}

// recheckHost serves two listings and records what the cron does.
func recheckEnv(t *testing.T, rows string, x *hostX) *indexHost {
	_, file, _ := listingKey(t)
	h := newIndexHost(t, file)
	inner := h.fakeHost.db
	h.fakeHost.db = func(c hostCall, out any) error {
		switch {
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM listings l JOIN accounts a ON a.id = l.account WHERE l.status = 'active' ORDER"):
			return cell(out, rows)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM mail_queue"):
			return cell(out, `[]`)
		}
		return inner(c, out)
	}
	x.install(t)
	return h
}

func listingRow(id int, name, sha, key string) string {
	return `{"id":` + itoa(id) + `,"name":"` + name + `","kind":"plugin","email":"dev@example.org","entry":{"kind":"plugin","name":"` + name +
		`","version":"1.0.0","url":"` + releaseURL + `","sha256":"` + sha + `","signer_key":"` + key + `"}}`
}

func TestRecheckDropsAChangedFileAndMailsTheDeveloper(t *testing.T) {
	fixedNow(t, t0)
	pub, keyID, _ := parseKey(goodKey)
	x := &hostX{inspect: sdk.InspectResult{SHA256: "new", SignerKeyID: keyID, SignatureOK: true}}
	h := recheckEnv(t, `[`+listingRow(1, "acme-seo", "old", pub)+`]`, x)
	if err := recheckCron(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	if len(x.inspected) != 1 || x.inspected[0].SignerKey != pub || x.inspected[0].URL != releaseURL {
		t.Errorf("inspect %+v", x.inspected)
	}
	up := h.execs("UPDATE listings SET status = 'dropped'")
	if len(up) != 1 || up[0].args[0] != int64(1) || up[0].args[1] != "the file changed" {
		t.Fatalf("drop %+v", up)
	}
	if q := h.execs("INSERT INTO mail_queue"); len(q) != 1 || q[0].args[0] != "dev@example.org" {
		t.Errorf("mail %+v", q)
	}
	if len(h.published) != 3 {
		t.Error("the index was not published after a drop")
	}
	requireGuard(t, h.fakeHost)
}

func TestRecheckDropsAChangedSigner(t *testing.T) {
	pub, _, _ := parseKey(goodKey)
	x := &hostX{inspect: sdk.InspectResult{SHA256: "same", SignerKeyID: "0123456789abcdef"}}
	h := recheckEnv(t, `[`+listingRow(1, "acme-seo", "same", pub)+`]`, x)
	if err := recheckCron(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	if up := h.execs("UPDATE listings SET status = 'dropped'"); len(up) != 1 || up[0].args[1] != "the signer changed" {
		t.Fatalf("%+v", up)
	}
}

func TestRecheckKeepsAListingWhenInspectFailsOrProblemsAppear(t *testing.T) {
	pub, keyID, _ := parseKey(goodKey)
	for name, x := range map[string]*hostX{
		"error":    {inspectErr: hostErr("timeout")},
		"problems": {inspect: sdk.InspectResult{SHA256: "same", SignerKeyID: keyID, Problems: []string{"download answered 503"}}},
		"download": {inspect: sdk.InspectResult{Problems: []string{"download answered 503"}}},
		"empty":    {inspect: sdk.InspectResult{}},
	} {
		h := recheckEnv(t, `[`+listingRow(1, "acme-seo", "same", pub)+`]`, x)
		if err := recheckCron(sdk.CronEvent{}); err != nil {
			t.Fatal(err)
		}
		if len(h.execs("UPDATE listings SET status = 'dropped'")) != 0 || len(h.execs("UPDATE listings SET checked_at = now(), check_failures = check_failures + 1")) != 1 || h.published != nil {
			t.Errorf("%s: %+v", name, h.calls)
		}
		if len(h.execs("INSERT INTO mail_queue")) != 0 {
			t.Errorf("%s: mail queued", name)
		}
	}
}

func TestRecheckGoodListingResetsFailures(t *testing.T) {
	pub, keyID, _ := parseKey(goodKey)
	x := &hostX{inspect: sdk.InspectResult{SHA256: "same", SignerKeyID: keyID, SignatureOK: true}}
	h := recheckEnv(t, `[`+listingRow(1, "acme-seo", "same", pub)+`]`, x)
	if err := recheckCron(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	if len(h.execs("UPDATE listings SET checked_at = now(), check_failures = 0")) != 1 || h.published != nil {
		t.Errorf("%+v", h.calls)
	}
}

func TestRecheckAsksForTwoAndStopsAfterTheBudget(t *testing.T) {
	pub, keyID, _ := parseKey(goodKey)
	x := &hostX{inspect: sdk.InspectResult{SHA256: "same", SignerKeyID: keyID, SignatureOK: true}}
	rows := `[` + listingRow(1, "acme-seo", "same", pub) + `,` + listingRow(2, "acme-two", "same", pub) + `]`
	h := recheckEnv(t, rows, x)
	var limit any
	inner := h.fakeHost.db
	h.fakeHost.db = func(c hostCall, out any) error {
		if c.fn == "db.query" && strings.Contains(c.sql, "ORDER BY l.checked_at") {
			limit = c.args[0]
		}
		return inner(c, out)
	}
	// The clock jumps 6 seconds with every reading: the second check never starts.
	cur := t0
	old := now
	t.Cleanup(func() { now = old })
	now = func() time.Time { cur = cur.Add(6 * time.Second); return cur }
	if err := recheckCron(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	if limit != recheckPerRun || len(x.inspected) != 0 {
		t.Errorf("limit %v inspected %d", limit, len(x.inspected))
	}
	now = func() time.Time { return t0 }
	if err := recheckCron(sdk.CronEvent{}); err != nil || len(x.inspected) != 2 {
		t.Errorf("%v inspected %d", err, len(x.inspected))
	}
}

func TestRecheckReturnsAnErrorOnlyWhenTheListCannotBeRead(t *testing.T) {
	x := &hostX{}
	h := recheckEnv(t, `[]`, x)
	if err := recheckCron(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	h.fakeHost.db = func(hostCall, any) error { return errDB }
	if err := recheckCron(sdk.CronEvent{}); err == nil {
		t.Fatal("no error")
	}
}
