package main

import "testing"

func TestNormaliseEmail(t *testing.T) {
	got, ok := normEmail("  John_Smith@example.com ")
	if !ok || got != "john_smith@example.com" {
		t.Fatalf("normEmail = %q, %v", got, ok)
	}
	// printf '%s' 'john_smith@example.com' | sha256sum
	const want = "5ac3cbb303be9ca0f0ca5bdd9dde9f2d74309d33022cf5fa9bc90bbdbe9ea428"
	if h := sha(got); h != want {
		t.Fatalf("sha = %s", h)
	}
	for _, bad := range []any{"no-at.example.com", "a@@example.com", "a@b@example.com", "@example.com", "user@", "", 12.0, true, nil, []any{"a@example.com"}} {
		if v, ok := normEmail(bad); ok {
			t.Errorf("normEmail(%v) accepted: %q", bad, v)
		}
	}
}

func TestNormalisePhone(t *testing.T) {
	cases := []struct {
		in   any
		want string
		ok   bool
	}{
		{"+61 (04) 1234-5678", "610412345678", true},
		{"0412 345 678", "412345678", true},
		{"00 1234567", "1234567", true},
		{"123-456", "", false},
		{"0001234", "", false},
		{"abc", "", false},
		{float64(61412345678), "61412345678", true},
		{float64(4.1234567e9), "4123456700", true},
		{true, "", false},
		{[]any{"1234567"}, "", false},
		{map[string]any{"a": "1234567"}, "", false},
		{nil, "", false},
	}
	for _, c := range cases {
		got, ok := normPhone(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("normPhone(%v) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormaliseNames(t *testing.T) {
	cases := []struct {
		in          any
		first, last string
	}{
		{"Mary-Jane O'Neil", "maryjane", "oneil"},
		{"  Mary   ", "mary", ""},
		{"Zoë Müller", "zoë", "müller"},
		{"Ann Marie Lee", "ann", "lee"},
		{"Dr. Who", "dr", "who"},
		{"...", "", ""},
		{"", "", ""},
		{[]any{"Mary Lee"}, "", ""},
		{map[string]any{"a": "b"}, "", ""},
		{nil, "", ""},
		{true, "", ""},
	}
	for _, c := range cases {
		f, l := normNames(c.in)
		if f != c.first || l != c.last {
			t.Errorf("normNames(%v) = %q, %q; want %q, %q", c.in, f, l, c.first, c.last)
		}
	}
}

func TestNormaliseBuildUserData(t *testing.T) {
	fields := map[string]any{
		"email": "John_Smith@example.com",
		"phone": "+61 (04) 1234-5678",
		"name":  "Zoë Smith",
	}
	u := buildUserData(fields, "email", "phone", "name")
	if len(u.Em) != 1 || len(u.Ph) != 1 || len(u.Fn) != 1 || len(u.Ln) != 1 {
		t.Fatalf("userData = %+v", u)
	}
	if u.Em[0] != sha("john_smith@example.com") || u.Ph[0] != sha("610412345678") ||
		u.Fn[0] != sha("zoë") || u.Ln[0] != sha("smith") {
		t.Fatalf("hashes do not match the normalised values: %+v", u)
	}
	if !u.hasContact() {
		t.Fatal("hasContact = false")
	}
}

func TestNormaliseNoContact(t *testing.T) {
	u := buildUserData(map[string]any{"email": "nope", "phone": "12", "name": "Mary"}, "email", "phone", "name")
	if u.hasContact() {
		t.Fatalf("hasContact with %+v", u)
	}
	if len(u.Fn) != 1 || len(u.Em) != 0 || len(u.Ph) != 0 {
		t.Fatalf("userData = %+v", u)
	}
	empty := buildUserData(nil, "email", "phone", "name")
	if empty.hasContact() || len(empty.Fn) != 0 {
		t.Fatalf("empty = %+v", empty)
	}
}
