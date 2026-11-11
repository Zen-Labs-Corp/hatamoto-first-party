package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/registry"
)

// listingPubB64 is the public half of the key core trusts for the developer
// index (core: DeveloperListingKey). A var so tests can use their own key.
var listingPubB64 = "y6GSscyMTBF34YKufnzvSGQDb5do6Dl01EDMOyfFjEo="

const (
	indexLifetime = 30 * 24 * time.Hour
	indexSkew     = time.Minute
	maxIndexBytes = 900 << 10
	maxReasonLen  = 200
)

var (
	errNoListingKey = errors.New("the listing key is not set")
	errBadListing   = errors.New("the listing key is not valid")
	errWrongListing = errors.New("the listing key is not the key core trusts")
	errIndexTooBig  = errors.New("the index is too big to publish")
)

// parseListingKey reads the listing private key from the setting: the JSON key
// file or its bare base64. The key must be the one core trusts. Errors never
// carry key material.
func parseListingKey(in string) (ed25519.PrivateKey, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return nil, errNoListingKey
	}
	if strings.HasPrefix(in, "{") {
		var doc struct {
			Private string `json:"private"`
		}
		if json.Unmarshal([]byte(in), &doc) != nil {
			return nil, errBadListing
		}
		in = strings.TrimSpace(doc.Private)
	}
	raw, err := base64.StdEncoding.DecodeString(in)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, errBadListing
	}
	priv := ed25519.PrivateKey(raw)
	want, err := base64.StdEncoding.DecodeString(listingPubB64)
	if err != nil || len(want) != ed25519.PublicKeySize {
		return nil, errBadListing
	}
	if registry.KeyID(priv.Public().(ed25519.PublicKey)) != registry.KeyID(ed25519.PublicKey(want)) {
		return nil, errWrongListing
	}
	return priv, nil
}

// entryFrom builds the index entry of an approved submission.
func entryFrom(s submission, login, pub string) registry.Entry {
	title := s.Title
	if title == "" {
		title = s.Name
	}
	e := registry.Entry{
		Kind: s.Kind, Name: s.Name, Version: s.Version, Description: s.Description, URL: s.URL, SHA256: s.SHA256, Size: s.Size,
		Permissions: s.Permissions, Title: title, Author: login, Source: s.Source, Tier: "developer", About: s.About, SignerKey: pub,
	}
	if s.Kind == "theme" {
		e.SiteType = s.SiteType
		if strings.HasPrefix(s.Screenshot, "https://") {
			e.Screenshot = s.Screenshot
		}
	} else {
		e.Category = s.Category
	}
	return e
}

// cutRunes cuts s to at most n runes.
func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// buildIndex reads the active listings of active accounts and the revocations
// into the document core reads.
func buildIndex(at time.Time) (registry.Index, error) {
	idx := registry.Index{
		Version:   1,
		IssuedAt:  at.Add(-indexSkew).UTC().Format(time.RFC3339),
		ExpiresAt: at.Add(indexLifetime).UTC().Format(time.RFC3339),
		Packages:  []registry.Entry{},
	}
	err := dbJSON(`SELECT coalesce(jsonb_agg(l.entry ORDER BY l.name, l.kind), '[]'::jsonb) FROM listings l `+
		`JOIN accounts a ON a.id = l.account WHERE l.status = 'active' AND a.status = 'active'`, &idx.Packages)
	if err != nil {
		return idx, err
	}
	if idx.Packages == nil {
		idx.Packages = []registry.Entry{}
	}
	var revs []registry.Revocation
	err = dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('signer_key', signer_key, 'name', name, 'version', version, 'reason', reason) ORDER BY id), '[]'::jsonb) FROM revocations`, &revs)
	if err != nil {
		return idx, err
	}
	for _, r := range revs {
		if r.Name != "" {
			r.SignerKey = "" // core reads a signer key as a revocation of the whole key
		}
		r.Reason = cutRunes(r.Reason, maxReasonLen)
		idx.Revoked = append(idx.Revoked, r)
	}
	return idx, nil
}

// publish signs the current index and stores it for GET /registry.json.
func publish() error {
	st, err := loadSettings()
	if err != nil {
		return err
	}
	priv, err := parseListingKey(st.ListingKey)
	if err != nil {
		return err
	}
	at := now()
	idx, err := buildIndex(at)
	if err != nil {
		return err
	}
	doc, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if len(doc) > maxIndexBytes {
		return errIndexTooBig
	}
	body, err := registry.SignIndex(doc, priv)
	if err != nil {
		return err
	}
	return dbExec(`INSERT INTO published (id, body, issued_at, expires_at) VALUES (1, $1, $2::timestamptz, $3::timestamptz) `+
		`ON CONFLICT (id) DO UPDATE SET body = EXCLUDED.body, issued_at = EXCLUDED.issued_at, expires_at = EXCLUDED.expires_at`,
		string(body), idx.IssuedAt, idx.ExpiresAt)
}

// handleRegistry serves the signed index exactly as stored.
func handleRegistry() sdk.HTTPResponse {
	text := func(code int, msg string) sdk.HTTPResponse {
		return sdk.HTTPResponse{Status: code, Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store"}, Body: msg + "\n"}
	}
	var rows []struct {
		Body string `json:"body"`
	}
	if err := dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('body', body)), '[]'::jsonb) FROM published WHERE id = 1`, &rows); err != nil {
		return text(http.StatusServiceUnavailable, "unavailable")
	}
	if len(rows) == 0 || rows[0].Body == "" {
		return text(http.StatusServiceUnavailable, "not published yet")
	}
	return sdk.HTTPResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "public, max-age=300"},
		Body:    rows[0].Body,
	}
}

// resignAfter is how old the stored index may get before the cron signs it
// again. The index lives 30 days; the cron runs hourly because core restarts
// every cron timer when it loads the plugin, so a long period could never fire.
const resignAfter = 20 * time.Hour

// resignCron signs the index again so it never expires. It does nothing while
// the stored index is younger than resignAfter.
func resignCron(sdk.CronEvent) error {
	var fresh []struct {
		ID int `json:"id"`
	}
	if err := dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('id', id)), '[]'::jsonb) FROM published WHERE id = 1 AND issued_at > $1::timestamptz`,
		&fresh, now().Add(-resignAfter).UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if len(fresh) > 0 {
		return nil
	}
	err := publish()
	if err != nil {
		switch {
		case errors.Is(err, errNoListingKey), errors.Is(err, errBadListing), errors.Is(err, errWrongListing), errors.Is(err, errIndexTooBig):
			logf("warn", "resign skipped: %v", err) // these messages carry no key material
		default:
			logf("warn", "resign failed")
		}
	}
	return err
}
