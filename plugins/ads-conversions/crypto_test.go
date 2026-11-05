package main

import (
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key, err := deriveKey("client-secret")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := seal(key, "token-value")
	if err != nil || strings.Contains(sealed, "token-value") {
		t.Fatalf("sealed = %q, err %v", sealed, err)
	}
	if got, err := open(key, sealed); err != nil || got != "token-value" {
		t.Fatalf("open = %q, %v", got, err)
	}
	other, _ := deriveKey("other-secret")
	if _, err := open(other, sealed); err == nil {
		t.Fatal("a different secret must not open the token")
	}
	if _, err := open(key, "!!"); err == nil {
		t.Fatal("garbage must not open")
	}
	if _, err := deriveKey(""); err == nil {
		t.Fatal("an empty secret must not derive a key")
	}
}

func TestPKCEAndRandomToken(t *testing.T) {
	v, c := pkcePair()
	if len(v) < 43 || len(c) != 43 || v == c {
		t.Fatalf("verifier %q challenge %q", v, c)
	}
	if a, b := randomToken(16), randomToken(16); len(a) != 32 || a == b {
		t.Fatalf("tokens %q %q", a, b)
	}
}
