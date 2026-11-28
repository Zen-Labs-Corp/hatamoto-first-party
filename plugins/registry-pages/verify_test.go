package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk/registry"
)

// pinPriv is a throwaway deterministic signing key (ed25519 is deterministic).
// It guards nothing.
func pinPriv() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("hatamoto-json-v2-4-pin"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func trustFor(priv ed25519.PrivateKey) trustSet {
	pub := priv.Public().(ed25519.PublicKey)
	return trustSet{keyID(pub): pub}
}

func pinTrust() trustSet { return trustFor(pinPriv()) }

// pinIndexInput is the index JSON the fixture envelope was signed from: escaped
// \u0026 \u003c \u2028, an exponent number and free whitespace.
const pinIndexInput = "{\n  \"version\": 1,\n  \"issued_at\": \"2030-01-01T00:00:00Z\",\n  \"expires_at\": \"2030-02-01T00:00:00Z\",\n  \"packages\": [ {\"kind\":\"plugin\",\"name\":\"hello\",\"version\":\"1.0.0\",\"url\":\"p/hello.hmp\",\"sha256\":\"0000000000000000000000000000000000000000000000000000000000000007\",\"description\":\"a \\u0026 b \\u003c c \\u003e d \\u2028 e  f\",\"size\":1e3} ]\n}\n"

// pinCompactIndex is the compact form of pinIndexInput: the bytes that are signed.
const pinCompactIndex = `{"version":1,"issued_at":"2030-01-01T00:00:00Z","expires_at":"2030-02-01T00:00:00Z","packages":[{"kind":"plugin","name":"hello","version":"1.0.0","url":"p/hello.hmp","sha256":"0000000000000000000000000000000000000000000000000000000000000007","description":"a \u0026 b \u003c c \u003e d \u2028 e  f","size":1e3}]}`

func readFixture(t *testing.T) []byte {
	t.Helper()
	doc, err := os.ReadFile("testdata/head_registry.json")
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func sign(t *testing.T, index string, priv ed25519.PrivateKey) []byte {
	t.Helper()
	doc, err := registry.SignIndex([]byte(index), priv)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestFixtureRegistryVerifies(t *testing.T) {
	raw, err := pinTrust().verifyIndex(readFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != pinCompactIndex {
		t.Fatalf("compact index changed:\n got %s\nwant %s", raw, pinCompactIndex)
	}
}

func TestSDKSignerReproducesFixture(t *testing.T) {
	doc := sign(t, pinIndexInput, pinPriv())
	want := readFixture(t)
	if !bytes.Equal(bytes.TrimSpace(doc), bytes.TrimSpace(want)) {
		t.Fatalf("envelope differs:\n got %s\nwant %s", doc, want)
	}
}

func TestRawHTMLCharsVerify(t *testing.T) {
	idx := "{\"version\":1,\"issued_at\":\"2030-01-01T00:00:00Z\",\"expires_at\":\"2030-02-01T00:00:00Z\",\"packages\":[{\"description\":\"a & b < c > d \u2028 e\"}]}"
	if _, err := pinTrust().verifyIndex(sign(t, idx, pinPriv())); err != nil {
		t.Fatalf("signed index does not verify: %v", err)
	}
}

func TestPinnedKeysParse(t *testing.T) {
	if len(mainTrust) != 1 || len(devTrust) != 1 {
		t.Fatalf("trust sets %d, %d", len(mainTrust), len(devTrust))
	}
}

func TestVerifyRefusals(t *testing.T) {
	good := string(readFixture(t))
	if !strings.Contains(good, `"name": "hello"`) {
		t.Fatal("fixture layout changed")
	}
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	cases := map[string]struct {
		doc  []byte
		want string
	}{
		"flipped index byte": {[]byte(strings.Replace(good, `"name": "hello"`, `"name": "hellp"`, 1)), "does not match"},
		"untrusted signer":   {sign(t, pinIndexInput, other), "not trusted"},
		"algorithm rsa":      {[]byte(strings.Replace(good, `"signature"`, `"algorithm": "rsa", "signature"`, 1)), "algorithm"},
		"empty signature":    {[]byte(`{"index":{"version":1},"signer":"x","signature":""}`), "not signed"},
		"missing index":      {[]byte(`{"signer":"x","signature":"AAAA"}`), "missing index"},
		"bad base64":         {[]byte(strings.Replace(good, `"signature": "`, `"signature": "!!`, 1)), "does not match"},
		"not json":           {[]byte(`nope`), "registry index"},
	}
	for name, c := range cases {
		_, err := pinTrust().verifyIndex(c.doc)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want it to contain %q", name, err, c.want)
		}
	}
}

func TestParseIndexRefusesBadSignature(t *testing.T) {
	_, err := parseIndex(readFixture(t), trustFor(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))), time.Date(2030, 1, 5, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("an index of an untrusted signer was accepted")
	}
}
