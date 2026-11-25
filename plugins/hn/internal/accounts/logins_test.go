package accounts

import (
	"strings"
	"testing"
)

func TestLoginsCreateAndFind(t *testing.T) {
	f := installFake(t)
	ok, err := createAccount("key1", "Ann", "acc1", "hash1")
	if err != nil || !ok {
		t.Fatalf("createAccount = %v, %v", ok, err)
	}
	l, err := loginByName("aNN")
	if err != nil || l == nil || l.Username != "Ann" || l.Account != "acc1" || l.PwHash != "hash1" {
		t.Fatalf("loginByName = %+v, %v", l, err)
	}
	if f.byKey["key1"] == nil || f.byKey["key1"].Username != "Ann" {
		t.Fatalf("no users row for the key: %+v", f.byKey)
	}
	byID, err := loginByUser(l.UserID)
	if err != nil || byID == nil || byID.Account != "acc1" {
		t.Fatalf("loginByUser = %+v, %v", byID, err)
	}
}

func TestLoginsCreateTakenName(t *testing.T) {
	f := installFake(t)
	f.add("key1", "Ann")
	ok, err := createAccount("key2", "ann", "acc2", "hash2")
	if err != nil || ok {
		t.Fatalf("createAccount on a taken name = %v, %v", ok, err)
	}
	if len(f.logins) != 0 || f.byKey["key2"] != nil {
		t.Fatalf("rows left behind: %+v %+v", f.logins, f.byKey)
	}
}

func TestLoginsNoneForMemberWithoutLogin(t *testing.T) {
	f := installFake(t)
	u := f.add("key1", "Ann")
	if l, err := loginByUser(u.ID); err != nil || l != nil {
		t.Fatalf("loginByUser = %+v, %v", l, err)
	}
	if l, err := loginByName("ann"); err != nil || l != nil {
		t.Fatalf("loginByName = %+v, %v", l, err)
	}
}

func TestLoginsAddSetHashSetEmail(t *testing.T) {
	f := installFake(t)
	u := f.add("key1", "Ann")
	if ok, err := addLogin(u.ID, "acc1", "h1"); err != nil || !ok {
		t.Fatalf("addLogin = %v, %v", ok, err)
	}
	if ok, _ := addLogin(u.ID, "acc2", "h2"); ok {
		t.Error("second login for one member added")
	}
	if err := setHash(u.ID, "h3"); err != nil {
		t.Fatal(err)
	}
	if err := setEmail(u.ID, "a@b.c"); err != nil {
		t.Fatal(err)
	}
	l, _ := loginByUser(u.ID)
	if l == nil || l.PwHash != "h3" || l.Email != "a@b.c" || l.Account != "acc1" {
		t.Fatalf("login = %+v", l)
	}
}

func TestLoginsCleanEmail(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"  ", "", true},
		{" a@b.c ", "a@b.c", true},
		{"a b@c", "", false},
		{"@b", "", false},
		{"a@", "", false},
		{"a@@b", "", false},
		{"<a@b>", "", false},
		{"a\"@b", "", false},
		{strings.Repeat("a", 253) + "@b", "", false},
		{strings.Repeat("a", 252) + "@b", strings.Repeat("a", 252) + "@b", true},
	} {
		got, ok := cleanEmail(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("cleanEmail(%.20q) = %.20q, %v; want %.20q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
