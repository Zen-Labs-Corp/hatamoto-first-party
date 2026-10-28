package main

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestSealOpenRoundTripAndWrongKey(t *testing.T) {
	key, err := deriveKey("extra", "client-secret")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := seal(key, "refresh-token-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "refresh-token-value") {
		t.Fatal("the token is stored in the clear")
	}
	again, _ := seal(key, "refresh-token-value")
	if again == sealed {
		t.Fatal("two seals of one value must differ (random nonce)")
	}
	if got, err := open(key, sealed); err != nil || got != "refresh-token-value" {
		t.Fatalf("open: %q %v", got, err)
	}
	other, _ := deriveKey("", "client-secret") // the encryption key changed
	if _, err := open(other, sealed); err == nil {
		t.Fatal("a different key must not open the token")
	}
	if _, err := deriveKey("", ""); err == nil {
		t.Fatal("no key material must be refused")
	}
}

func TestPKCEChallengeIsS256OfVerifier(t *testing.T) {
	v, c := pkcePair()
	sum := sha256.Sum256([]byte(v))
	if c != base64.RawURLEncoding.EncodeToString(sum[:]) || len(v) < 43 {
		t.Fatalf("verifier %q challenge %q", v, c)
	}
	if v2, _ := pkcePair(); v2 == v {
		t.Fatal("verifiers must be random")
	}
}
