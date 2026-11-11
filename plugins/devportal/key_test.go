package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseKey(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	sum := sha256.Sum256(raw)
	wantID := hex.EncodeToString(sum[:8])
	std := base64.StdEncoding.EncodeToString(raw)
	priv := base64.StdEncoding.EncodeToString(make([]byte, ed25519.PrivateKeySize))

	ok := map[string]string{
		"plain":      std,
		"whitespace": "  " + std + "\n",
		"pub json":   `{"key_id":"x","public":"` + std + `"}`,
	}
	for name, in := range ok {
		b64, id, err := parseKey(in)
		if err != nil || b64 != std || id != wantID || len(id) != 16 {
			t.Errorf("%s: %q %q %v", name, b64, id, err)
		}
	}

	bad := map[string]string{
		"unpadded":       strings.TrimRight(std, "="),
		"private key":    priv,
		"31 bytes":       base64.StdEncoding.EncodeToString(raw[:31]),
		"33 bytes":       base64.StdEncoding.EncodeToString(append(raw, 0)),
		"garbage":        "not a key!",
		"empty":          "",
		"bad json":       `{"public":`,
		"json no key":    `{"key_id":"x"}`,
		"private member": `{"public":"` + std + `","private":"` + priv + `"}`,
		"all zero":       base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"oversized":      strings.Repeat("A", maxKeyInput+1),
	}
	for name, in := range bad {
		if _, _, err := parseKey(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err := parseKey(priv); err == nil || !strings.Contains(err.Error(), "private") {
		t.Errorf("private key error %v", err)
	}
}
