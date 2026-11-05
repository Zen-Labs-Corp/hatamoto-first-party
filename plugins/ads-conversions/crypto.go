package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// Tokens are stored in kv encrypted with AES-256-GCM. The key is derived from
// the platform's client secret, a write-only setting. Be clear about what that
// buys: the settings live in Hatamoto's own database, so a full dump holds both
// the ciphertext and the key material. The encryption protects the plugin's
// own kv rows (a stray read of that table), not the database as a whole.
// Changing the client secret makes the stored tokens unreadable, which shows
// as "Reconnect".

var errNoKeyMaterial = errors.New("the client secret is not set")

func deriveKey(clientSecret string) ([]byte, error) {
	if clientSecret == "" {
		return nil, errNoKeyMaterial
	}
	h := sha256.New()
	h.Write([]byte("hatamoto:ads-conversions:tokens:v1\x00"))
	h.Write([]byte(clientSecret))
	return h.Sum(nil), nil
}

// seal returns base64(nonce || ciphertext) of plain.
func seal(key []byte, plain string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// open reverses seal; a wrong key or altered text is an error.
func open(key []byte, sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("stored token is too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("stored token cannot be decrypted")
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// randomToken is n random bytes in hex.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the host's random source failing is not recoverable
	}
	return hex.EncodeToString(b)
}

// pkcePair returns a PKCE code verifier and its S256 challenge (RFC 7636).
func pkcePair() (verifier, challenge string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}
