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
			Public string `json:"public"`
		}
		if json.Unmarshal([]byte(in), &doc) != nil {
			return "", "", errors.New("could not read that key file")
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
		return "", "", errors.New("that is a private key, never paste it")
	default:
		return "", "", errors.New("not an ed25519 public key (want 32 bytes, base64)")
	}
	return base64.StdEncoding.EncodeToString(raw), registry.KeyID(ed25519.PublicKey(raw)), nil
}
