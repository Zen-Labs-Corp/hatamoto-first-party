package accounts

import (
	"crypto/rand"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// hashCost is the bcrypt cost. 11 measured ~180 ms per hash in wazero (the runtime core uses).
const hashCost = 11

// Password length bounds in bytes; bcrypt refuses more than 72.
const (
	minPassword = 8
	maxPassword = 72
)

// dummyHash is a bcrypt hash (cost hashCost) of a random string nobody knows. Login compares
// against it when the username has no login, so an unknown user costs the same as a wrong password.
const dummyHash = "$2a$11$8kXxwnSh6Q5ar4nJ0eYDae8oQIGBKb3h4areAA3dzlm2z.PHa3xF."

func validPassword(pw string) bool { return len(pw) >= minPassword && len(pw) <= maxPassword }

func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), hashCost)
	return string(h), err
}

// compareHash is a var so tests can count calls; production is bcrypt.CompareHashAndPassword.
var compareHash = bcrypt.CompareHashAndPassword

// checkPassword always runs exactly one compare. An empty hash, or a password over maxPassword,
// compares against dummyHash and returns false, so timing does not tell those cases from a wrong password.
func checkPassword(hash, pw string) bool {
	if hash == "" {
		_ = compareHash([]byte(dummyHash), []byte(pw[:min(len(pw), maxPassword)]))
		return false
	}
	if len(pw) > maxPassword {
		_ = compareHash([]byte(dummyHash), []byte(pw[:maxPassword]))
		return false
	}
	return compareHash([]byte(hash), []byte(pw)) == nil
}

// needsRehash reports a stored hash made with a cost below hashCost.
func needsRehash(hash string) bool {
	c, err := bcrypt.Cost([]byte(hash))
	return err == nil && c < hashCost
}

// newAccountID returns 16 random bytes as 32 hex chars: the plugin-owned account id.
func newAccountID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
