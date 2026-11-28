package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

// The registry host and the two signed indexes the plugin reads, as core does.
const (
	registryURL  = "https://hatamoto.dev/registry.json"
	developerURL = "https://hatamoto.dev/p/devportal/registry.json"

	// Public keys: the first-party signer of the main index and the signer of
	// the developer index (listing key).
	firstPartyKey       = "HsfRxf3cApV2VddoUEfOM76UrOWbr2yGT2nD/xeesAo="
	developerListingKey = "y6GSscyMTBF34YKufnzvSGQDb5do6Dl01EDMOyfFjEo="
)

// Index refusals.
var (
	errUntrustedKey = errors.New("signed by a key that is not trusted")
	errBadSignature = errors.New("signature does not match")
	errUnsigned     = errors.New("the index is not signed")
)

// trustSet is the signer keys an index may be signed by, by key id.
type trustSet map[string]ed25519.PublicKey

// mainTrust and devTrust are package vars so tests can swap in a test key.
var (
	mainTrust = mustTrust(firstPartyKey)
	devTrust  = mustTrust(developerListingKey)
)

// pinnedUnmarshal is core's decode option set: the same bytes decode the same
// way on both sides of the signature.
var pinnedUnmarshal = jsonv2.JoinOptions(
	jsonv2.MatchCaseInsensitiveNames(true),
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
)

func mustTrust(b64 string) trustSet {
	pub, err := parsePublicKey(b64)
	if err != nil {
		panic(err)
	}
	return trustSet{keyID(pub): pub}
}

// keyID names a public key: the first 16 hex characters of its SHA-256.
func keyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// parsePublicKey decodes a base64 ed25519 public key.
func parsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not an ed25519 public key (want 32 bytes, base64)")
	}
	return ed25519.PublicKey(b), nil
}

// envelope is registry.json: the index and a signature over its compact JSON.
type envelope struct {
	Index     jsontext.Value `json:"index"`
	Signer    string         `json:"signer"`
	Algorithm string         `json:"algorithm,omitempty"`
	Signature string         `json:"signature"`
}

// compactIndex strips the whitespace of an index document without escaping or
// rewriting anything else: the signature covers the compact form as embedded.
func compactIndex(index []byte) ([]byte, error) {
	v := jsontext.Value(bytes.Clone(index))
	if err := v.Compact(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true)); err != nil {
		return nil, err
	}
	return v, nil
}

// verifyIndex checks a registry.json envelope against the trusted keys (never
// the key the envelope names beyond the lookup) and returns the compact index.
func (t trustSet) verifyIndex(doc []byte) (jsontext.Value, error) {
	var env envelope
	if err := jsonv2.Unmarshal(doc, &env, pinnedUnmarshal); err != nil {
		return nil, fmt.Errorf("registry index: %w", err)
	}
	compact, err := compactIndex(env.Index)
	if err != nil || len(compact) == 0 {
		return nil, errors.New("registry index: missing index")
	}
	if env.Signature == "" {
		return nil, fmt.Errorf("registry index: %w", errUnsigned)
	}
	if env.Algorithm != "" && env.Algorithm != "ed25519" {
		return nil, fmt.Errorf("registry index: unsupported algorithm %q", env.Algorithm)
	}
	pub, ok := t[env.Signer]
	if !ok {
		return nil, fmt.Errorf("registry index: %w (key %s)", errUntrustedKey, env.Signer)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil || !ed25519.Verify(pub, compact, sig) {
		return nil, fmt.Errorf("registry index: %w", errBadSignature)
	}
	return compact, nil
}
