package main

import (
	"strings"
	"testing"
)

// Known SHA-256 vectors, written out so the test does not use the code under test.
// gmailDomain is the one domain (with googlemail.com) Google folds dots for.
const gmailDomain = "gmail.com"

const (
	hashEmpty       = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	hashABC         = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	hashEmail       = "08168cd80dfd534ab0f10af10f1303fe00af2d43ab5c1432360d137f8197e17a" // a@example.com
	hashGmailDots   = "831f6494ad6be4fcb3a724c3d5fef22d3ceffa3c62ef3a7984e45a0ea177f982" // dotted local part at the Gmail domain
	hashGmailNoDots = "d6117306485ed0e50afab3ac871e98f81699151f30281527d63ff5f233656c69" // the same without dots
	hashPhoneDE     = "8b4e479827e2bbd1b1883e7a808e7f18d2e834c2830b8451f7e47659b8ff1257" // +491512345678
	hashPhoneDE9    = "663a4322addcdcc2be9435bfd2505066dc1048b89d33e48f363119f18bdd71e8" // +4915123456789
	hashPhoneUS     = "40d3f4e02db27d66cf4cfdda506c2c945f115a7955cc8491dda98ce5beabcda0" // +14155550100
)

func TestShaVectors(t *testing.T) {
	if sha("") != hashEmpty || sha("abc") != hashABC || sha("a@example.com") != hashEmail {
		t.Fatal("sha vectors")
	}
}

func TestNormEmail(t *testing.T) {
	cases := []struct {
		in   any
		want string
		ok   bool
	}{
		{"  A@Example.COM ", "a@example.com", true},
		{"a@example.com", "a@example.com", true},
		{"a@@example.com", "", false},
		{"example.com", "", false},
		{"@example.com", "", false},
		{"a@", "", false},
		{"", "", false},
		{true, "", false},
		{[]any{"a@example.com"}, "", false},
		{map[string]any{"x": 1}, "", false},
		{nil, "", false},
		{12.0, "", false},
	}
	for _, c := range cases {
		got, ok := normEmail(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("normEmail(%v) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestGoogleEmailStripsDotsForGmailOnly(t *testing.T) {
	cases := map[string]string{
		"jane.doe@" + gmailDomain:      "janedoe@" + gmailDomain,
		"j.a.n.e@google" + "mail.com":  "jane@google" + "mail.com",
		"jane.doe@example.com":         "jane.doe@example.com",
		"jane.doe@mail." + gmailDomain: "jane.doe@mail." + gmailDomain,
		"nodot@" + gmailDomain:         "nodot@" + gmailDomain,
	}
	for in, want := range cases {
		if got := googleEmail(in); got != want {
			t.Errorf("googleEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormPhone(t *testing.T) {
	cases := []struct {
		in   any
		cc   string
		want string
	}{
		{"+49 151 234 5678", "", "+491512345678"},
		{" +49 (151) 234-5678 ", "33", "+491512345678"},
		{"+49 (0)151 234 5678", "", "+491512345678"},
		{"+49 (0) 151 234 5678", "", "+491512345678"},
		{"0049 151 234 5678", "", "+491512345678"},
		{"0151 234 5678", "49", "+491512345678"},
		{"151 234 5678", "49", "+491512345678"},
		{"0151 234 5678", "", ""},      // no country code known
		{"+1234567", "", ""},           // 7 digits
		{"+12345678", "", "+12345678"}, // 8 digits
		{"+123456789012345", "", "+123456789012345"},
		{"+1234567890123456", "", ""}, // 16 digits
		{"0049", "", ""},
		{"call me", "49", ""},
		{"", "49", ""},
		{4915123456789.0, "", ""}, // a number has no "+": needs a code
		{4915123456789.0, "49", "+494915123456789"},
		{true, "49", ""},
		{[]any{"+491512345678"}, "49", ""},
		{nil, "49", ""},
	}
	for _, c := range cases {
		got, ok := normPhone(c.in, c.cc)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("normPhone(%v, %q) = %q, %v; want %q", c.in, c.cc, got, ok, c.want)
		}
	}
}

func TestBuildIdentityHashesWithPlus(t *testing.T) {
	f := map[string]any{"email": " Jane.Doe@" + strings.ToUpper(gmailDomain[:1]) + gmailDomain[1:] + " ", "phone": "+49 151 234 5678", "name": "Jane Doe"}
	id := buildIdentity(f, "email", "phone", "")
	if id.Em != hashGmailDots || id.EmGoogle != hashGmailNoDots || id.Ph != hashPhoneDE {
		t.Fatalf("identity = %+v", id)
	}
	// The hash is over the E.164 text including "+", not the bare digits.
	if id.Ph == sha("491512345678") {
		t.Fatal("phone hashed without the +")
	}
	plain := buildIdentity(map[string]any{"email": "a@example.com", "phone": "0049 15123 45 6789"}, "email", "phone", "")
	if plain.Em != hashEmail || plain.EmGoogle != hashEmail || plain.Ph != hashPhoneDE9 {
		t.Fatalf("identity = %+v", plain)
	}
	us := buildIdentity(map[string]any{"phone": "(415) 555-0100"}, "email", "phone", "1")
	if us.Ph != hashPhoneUS {
		t.Fatalf("default country code: %+v", us)
	}
}

func TestBuildIdentityInvalidInputYieldsNothing(t *testing.T) {
	for _, f := range []map[string]any{
		nil,
		{},
		{"email": "not-an-email", "phone": "12"},
		{"email": []any{"a@example.com"}, "phone": map[string]any{"n": "+491512345678"}},
		{"email": true, "phone": nil},
	} {
		if id := buildIdentity(f, "email", "phone", ""); id != (identity{}) {
			t.Errorf("buildIdentity(%v) = %+v", f, id)
		}
	}
}

func TestIdentityHoldsOnlyHexHashes(t *testing.T) {
	id := buildIdentity(map[string]any{"email": "a@example.com", "phone": "+491512345678"}, "email", "phone", "")
	for _, h := range []string{id.Em, id.EmGoogle, id.Ph} {
		if len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
			t.Errorf("not a lower-case hex SHA-256: %q", h)
		}
	}
}
