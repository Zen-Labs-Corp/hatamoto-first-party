package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"hatamoto.dev/sdk/registry"
)

const maxKeyInput = 4096

// errPrivateKey is shown to the user as is: it tells them what to do next.
var errPrivateKey = errors.New("that is a private key, never paste it")

// parseKey validates a pasted ed25519 public key: base64 of the 32 raw bytes,
// or the JSON of a .pub file written by `hatamoto keys generate`. It returns
// the canonical base64 and the 16 hex character key id.
func parseKey(in string) (b64, keyID string, err error) {
	if len(in) > maxKeyInput {
		return "", "", errors.New("that is too long to be a public key")
	}
	in = strings.TrimSpace(in)
	if strings.HasPrefix(in, "{") {
		var doc struct {
			Public  string `json:"public"`
			Private string `json:"private"`
			Secret  string `json:"secret"`
			Seed    string `json:"seed"`
		}
		if json.Unmarshal([]byte(in), &doc) != nil {
			return "", "", errors.New("could not read that key file")
		}
		if doc.Private != "" || doc.Secret != "" || doc.Seed != "" {
			return "", "", errPrivateKey
		}
		in = strings.TrimSpace(doc.Public)
	}
	raw, err := base64.StdEncoding.DecodeString(in)
	if err != nil {
		return "", "", errors.New("not base64")
	}
	switch len(raw) {
	case ed25519.PublicKeySize:
	case ed25519.PrivateKeySize:
		return "", "", errPrivateKey
	default:
		return "", "", errors.New("not an ed25519 public key (want 32 bytes, base64)")
	}
	if allZero(raw) {
		return "", "", errors.New("that is not a usable public key")
	}
	return base64.StdEncoding.EncodeToString(raw), registry.KeyID(ed25519.PublicKey(raw)), nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
