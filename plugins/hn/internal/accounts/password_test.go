package accounts

import (
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func countCompares(t *testing.T) *int {
	t.Helper()
	n := 0
	orig := compareHash
	compareHash = func(h, p []byte) error { n++; return orig(h, p) }
	t.Cleanup(func() { compareHash = orig })
	return &n
}

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !checkPassword(h, "correct horse") {
		t.Error("right password refused")
	}
	if checkPassword(h, "wrong horse!") {
		t.Error("wrong password accepted")
	}
}

func TestPasswordDummyHashCost(t *testing.T) {
	c, err := bcrypt.Cost([]byte(dummyHash))
	if err != nil || c != hashCost {
		t.Fatalf("dummyHash cost = %d, %v; want %d", c, err, hashCost)
	}
}

func TestPasswordEmptyHashOneCompare(t *testing.T) {
	n := countCompares(t)
	if checkPassword("", "whatever1") {
		t.Error("empty hash accepted")
	}
	if *n != 1 {
		t.Errorf("%d compares, want 1", *n)
	}
}

func TestPasswordTooLongOneCompare(t *testing.T) {
	h, err := bcrypt.GenerateFromPassword([]byte(strings.Repeat("a", 72)), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	n := countCompares(t)
	if checkPassword(string(h), strings.Repeat("a", 73)) {
		t.Error("73-byte password accepted")
	}
	if *n != 1 {
		t.Errorf("%d compares, want 1", *n)
	}
}

func TestPasswordValidBounds(t *testing.T) {
	for n, want := range map[int]bool{7: false, 8: true, 72: true, 73: false} {
		if got := validPassword(strings.Repeat("x", n)); got != want {
			t.Errorf("validPassword(%d bytes) = %v, want %v", n, got, want)
		}
	}
}

func TestPasswordNeedsRehash(t *testing.T) {
	low, _ := bcrypt.GenerateFromPassword([]byte("password1"), hashCost-1)
	if !needsRehash(string(low)) {
		t.Error("cost below hashCost not flagged")
	}
	if needsRehash(dummyHash) {
		t.Error("current cost flagged")
	}
	if needsRehash("garbage") {
		t.Error("invalid hash flagged")
	}
}

func TestAccountIDShape(t *testing.T) {
	a, err := newAccountID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newAccountID()
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(a) || a == b {
		t.Errorf("ids %q %q", a, b)
	}
}
