package main

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// metaFake is a one-row meta table.
func metaFake(t *testing.T, key *string, fail bool) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		if fail {
			return errors.New("db down")
		}
		a := args.(map[string]any)
		sql := a["sql"].(string)
		switch {
		case strings.HasPrefix(sql, "INSERT INTO meta"):
			if *key == "" {
				*key = a["args"].([]any)[1].(string)
			}
			return nil
		case strings.Contains(sql, "FROM meta"):
			return jsonInto(`{"rows":[["`+*key+`"]]}`, out)
		}
		t.Fatalf("unexpected sql %q", sql)
		return nil
	}
}

func TestHMACSecret(t *testing.T) {
	var stored string
	metaFake(t, &stored, false)
	a, err := hmacSecret()
	if err != nil || len(a) != 32 {
		t.Fatalf("first: %v %d", err, len(a))
	}
	b, err := hmacSecret()
	if err != nil || hex.EncodeToString(a) != hex.EncodeToString(b) {
		t.Errorf("the stored key must be read back: %v", err)
	}
	stored = "zz"
	if _, err := hmacSecret(); err == nil {
		t.Error("an unusable stored key must fail")
	}
	metaFake(t, &stored, true)
	if _, err := hmacSecret(); err == nil {
		t.Error("a db failure must fail")
	}
}

func TestSignAndVerify(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	code, err := newCode()
	if err != nil || !regexpHex32.MatchString(code) {
		t.Fatalf("code %q %v", code, err)
	}
	if other, _ := newCode(); other == code {
		t.Error("two codes must differ")
	}
	tok := signCode(key, kindTicket, code)
	if got, ok := verifyToken(key, kindTicket, tok); !ok || got != code {
		t.Fatalf("round trip: %q %v", got, ok)
	}
	if _, ok := verifyToken(key, kindWait, tok); ok {
		t.Error("a ticket token must not pass as a waitlist token")
	}
	if _, ok := verifyToken([]byte("another key another key another!"), kindTicket, tok); ok {
		t.Error("another key must fail")
	}
	for i := range tok {
		if tok[i] == '.' {
			continue
		}
		bad := []byte(tok)
		bad[i] = flipHex(bad[i])
		if _, ok := verifyToken(key, kindTicket, string(bad)); ok {
			t.Fatalf("tampered at %d accepted", i)
		}
	}
	for _, bad := range []string{"", "abc", code, code + ".", tok + "0", strings.ToUpper(tok), "../" + tok} {
		if _, ok := verifyToken(key, kindTicket, bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func flipHex(c byte) byte {
	if c == '0' {
		return '1'
	}
	return '0'
}

func TestTokenFresh(t *testing.T) {
	base := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return base }
	t.Cleanup(func() { now = time.Now })
	key := []byte("0123456789abcdef0123456789abcdef")
	issue := func(at time.Time) string {
		issued := itoa(at.UnixMilli())
		return issued + "." + tokenMAC(key, issued)
	}
	for name, c := range map[string]struct {
		tok  string
		want bool
	}{
		"too fast":   {issue(base.Add(-time.Second)), false},
		"ok":         {issue(base.Add(-10 * time.Second)), true},
		"too old":    {issue(base.Add(-2 * time.Hour)), false},
		"forged":     {itoa(base.Add(-time.Minute).UnixMilli()) + ".00", false},
		"no dot":     {"123", false},
		"not hex":    {"123.zz", false},
		"signed nan": {"abc." + tokenMAC(key, "abc"), false},
	} {
		if got := tokenFresh(key, c.tok); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
