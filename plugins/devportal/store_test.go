package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

func fixedNow(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	t.Cleanup(func() { now = old })
	now = func() time.Time { return at }
}

func requireGuard(t *testing.T, f *fakeHost) {
	t.Helper()
	for _, c := range f.sqlCalls() {
		if !sqlGuardOK(c.sql) {
			t.Errorf("sql fails the guard: %s", c.sql)
		}
	}
}

func TestCreateSessionStoresOnlyTheHash(t *testing.T) {
	fixedNow(t, t0)
	f := &fakeHost{}
	f.install(t)
	acct := int64(7)
	token, csrf, err := createSession(&acct, ghIdentity{})
	if err != nil || token == "" || csrf == "" || token == csrf {
		t.Fatalf("%q %q %v", token, csrf, err)
	}
	requireGuard(t, f)
	c := f.sqlCalls()[0]
	if c.args[0] != hashToken(token) || c.args[0] == token {
		t.Errorf("stored id %v", c.args[0])
	}
	if c.args[1] != &acct || c.args[2] != csrf || c.args[3] != nil || c.args[5] != nil {
		t.Errorf("args %v", c.args)
	}
	if c.args[6] != "2026-12-11T09:00:00Z" {
		t.Errorf("expires %v", c.args[6])
	}
	for _, a := range c.args {
		if a == token {
			t.Fatal("raw token reached the database")
		}
	}
}

func TestCreateSessionWithoutAccountCarriesGitHub(t *testing.T) {
	fixedNow(t, t0)
	f := &fakeHost{}
	f.install(t)
	if _, _, err := createSession(nil, ghIdentity{ID: 42, Login: "acme", Emails: []string{"a@b.io"}}); err != nil {
		t.Fatal(err)
	}
	c := f.sqlCalls()[0]
	if c.args[3] != int64(42) || c.args[4] != "acme" || c.args[5] != `["a@b.io"]` {
		t.Errorf("args %v", c.args)
	}
}

const sessionRow = `[{"id":"h","account":7,"csrf":"c","gh_id":null,"gh_login":null,"gh_emails":null,` +
	`"expires_at":"%s","status":"active","login":"acme","display_name":"Acme","email":"a@b.io","plan":"free",` +
	`"key_id":"3f9a1c2b7d4e5f60","key_added":"2026-11-09T10:00:00.5+00:00"}]`

func TestLookupSession(t *testing.T) {
	fixedNow(t, t0)
	for name, tc := range map[string]struct {
		expires   string
		wantSlide bool
	}{
		"fresh":  {"2026-12-11T08:30:00+00:00", false},
		"stale":  {"2026-12-11T07:00:00.123456+00:00", true},
		"almost": {"2026-11-12T09:00:00+00:00", true},
	} {
		f := &fakeHost{db: func(c hostCall, out any) error {
			if c.fn == "db.exec" {
				return affected(out, 1)
			}
			return cell(out, strings.Replace(sessionRow, "%s", tc.expires, 1))
		}}
		f.install(t)
		s, err := lookupSession("tok")
		if err != nil || s == nil {
			t.Fatalf("%s: %v %v", name, s, err)
		}
		if s.AccountID == nil || *s.AccountID != 7 || s.Login != "acme" || s.Status != "active" ||
			s.KeyID != "3f9a1c2b7d4e5f60" || s.KeyAdded.Year() != 2026 || s.CSRF != "c" || s.Plan != "free" {
			t.Errorf("%s: %+v", name, s)
		}
		requireGuard(t, f)
		q := f.sqlCalls()[0]
		if q.args[0] != hashToken("tok") || q.args[1] != "2026-11-11T09:00:00Z" {
			t.Errorf("%s: query args %v", name, q.args)
		}
		slid := len(f.sqlCalls()) == 2
		if slid != tc.wantSlide {
			t.Errorf("%s: slid = %v", name, slid)
		}
		if slid && f.sqlCalls()[1].args[1] != "2026-12-11T09:00:00Z" {
			t.Errorf("%s: slide args %v", name, f.sqlCalls()[1].args)
		}
	}
}

func TestLookupSessionIgnoresUnknownEmptyAndFailedSlide(t *testing.T) {
	fixedNow(t, t0)
	f := &fakeHost{db: func(hostCall, any) error { t.Error("db call for an empty token"); return nil }}
	f.install(t)
	if s, err := lookupSession(""); s != nil || err != nil {
		t.Fatalf("empty token: %v %v", s, err)
	}
	(&fakeHost{db: func(_ hostCall, out any) error { return cell(out, `[]`) }}).install(t)
	if s, err := lookupSession("gone"); s != nil || err != nil {
		t.Fatalf("expired token: %v %v", s, err)
	}
	// An expired row is excluded by the query itself.
	f = &fakeHost{db: func(_ hostCall, out any) error { return cell(out, `[]`) }}
	f.install(t)
	_, _ = lookupSession("gone")
	if q := f.sqlCalls()[0].sql; !strings.Contains(q, "expires_at > $2") {
		t.Errorf("no expiry check in %s", q)
	}
	// A failed slide keeps the session.
	(&fakeHost{db: func(c hostCall, out any) error {
		if c.fn == "db.exec" {
			return errors.New("db down")
		}
		return cell(out, strings.Replace(sessionRow, "%s", "2026-11-12T09:00:00+00:00", 1))
	}}).install(t)
	if s, err := lookupSession("tok"); s == nil || err != nil {
		t.Fatalf("failed slide: %v %v", s, err)
	}
}

func TestSignupSessionCarriesGitHubIdentity(t *testing.T) {
	fixedNow(t, t0)
	(&fakeHost{db: func(_ hostCall, out any) error {
		return cell(out, `[{"id":"h","account":null,"csrf":"c","gh_id":42,"gh_login":"acme","gh_emails":["a@b.io","c@d.io"],`+
			`"expires_at":"2026-12-11T09:00:00+00:00","status":null,"login":null,"display_name":null,"email":null,"plan":null,"key_id":null,"key_added":null}]`)
	}}).install(t)
	s, err := lookupSession("tok")
	if err != nil || s == nil || s.AccountID != nil || s.GH.ID != 42 || s.GH.Login != "acme" || len(s.GH.Emails) != 2 || s.Status != "" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestSignInRefreshesAChangedLogin(t *testing.T) {
	f := &fakeHost{db: func(c hostCall, out any) error {
		if c.fn == "db.exec" {
			return affected(out, 1)
		}
		return cell(out, `[{"id":9,"status":"active","login":"old-name"}]`)
	}}
	f.install(t)
	a, err := signInAccount(42, "new-name")
	if err != nil || a == nil || a.ID != 9 || a.Login != "new-name" {
		t.Fatalf("%+v %v", a, err)
	}
	requireGuard(t, f)
	calls := f.sqlCalls()
	if len(calls) != 2 || calls[0].args[0] != int64(42) {
		t.Fatalf("calls %+v", calls)
	}
	if u := calls[1]; !strings.HasPrefix(u.sql, "UPDATE accounts SET login") || u.args[0] != int64(42) || u.args[1] != "new-name" {
		t.Errorf("update %+v", u)
	}
	// Same login: no write. Unknown id: nil.
	f = &fakeHost{db: func(_ hostCall, out any) error { return cell(out, `[{"id":9,"status":"active","login":"same"}]`) }}
	f.install(t)
	if _, err := signInAccount(42, "same"); err != nil || len(f.sqlCalls()) != 1 {
		t.Fatalf("%v %d", err, len(f.sqlCalls()))
	}
	(&fakeHost{db: func(_ hostCall, out any) error { return cell(out, `[]`) }}).install(t)
	if a, err := signInAccount(1, "x"); a != nil || err != nil {
		t.Fatalf("%v %v", a, err)
	}
}

func TestCreateAccount(t *testing.T) {
	fixedNow(t, t0)
	f := &fakeHost{db: func(c hostCall, out any) error {
		if c.fn == "db.exec" {
			return affected(out, 1)
		}
		return cell(out, `[{"id":11,"status":"active","login":"acme"}]`)
	}}
	f.install(t)
	id, err := createAccount(ghIdentity{ID: 42, Login: "acme"}, "Acme Ltd", "a@b.io")
	if err != nil || id != 11 {
		t.Fatalf("%d %v", id, err)
	}
	requireGuard(t, f)
	c := f.sqlCalls()[0]
	want := []any{int64(42), "acme", "Acme Ltd", "a@b.io", "2026-11-11T09:00:00Z"}
	for i, w := range want {
		if c.args[i] != w {
			t.Fatalf("args %v, want %v", c.args, want)
		}
	}
	if !strings.Contains(c.sql, "'free'") || !strings.Contains(c.sql, "'active'") || !strings.Contains(c.sql, "ON CONFLICT (github_id) DO NOTHING") {
		t.Errorf("sql %s", c.sql)
	}
	(&fakeHost{db: func(_ hostCall, out any) error { return affected(out, 0) }}).install(t)
	if _, err := createAccount(ghIdentity{ID: 42}, "x", "y"); !errors.Is(err, errExists) {
		t.Fatalf("second create: %v", err)
	}
}

func keyHost(mine, taken, inserted int, insertErr error) *fakeHost {
	return &fakeHost{db: func(c hostCall, out any) error {
		if c.fn == "db.exec" {
			if insertErr != nil {
				return insertErr
			}
			return affected(out, inserted)
		}
		return cell(out, `[{"mine":`+string(rune('0'+mine))+`,"taken":`+string(rune('0'+taken))+`}]`)
	}}
}

func TestAddKey(t *testing.T) {
	dup := errors.New("duplicate key value violates unique constraint")
	for name, tc := range map[string]struct {
		host *fakeHost
		want error
	}{
		"ok":                  {keyHost(0, 0, 1, nil), nil},
		"account has a key":   {keyHost(1, 0, 1, nil), errHasKey},
		"key of someone else": {keyHost(0, 1, 1, nil), errKeyTaken},
		"suspended":           {keyHost(0, 0, 0, nil), errInactive},
	} {
		tc.host.install(t)
		err := addKey(5, "pub", "3f9a1c2b7d4e5f60")
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
		requireGuard(t, tc.host)
	}
	// Lost race: the insert fails on the unique index; the re-check names why.
	n := 0
	f := &fakeHost{db: func(c hostCall, out any) error {
		if c.fn == "db.exec" {
			return dup
		}
		n++
		if n == 1 {
			return cell(out, `[{"mine":0,"taken":0}]`)
		}
		return cell(out, `[{"mine":0,"taken":1}]`)
	}}
	f.install(t)
	if err := addKey(5, "pub", "id"); !errors.Is(err, errKeyTaken) {
		t.Errorf("race: %v", err)
	}
	// The insert checks the account status itself.
	c := keyHost(0, 0, 1, nil)
	c.install(t)
	_ = addKey(5, "pub", "id")
	ins := c.sqlCalls()[1]
	if !strings.Contains(ins.sql, "status = 'active'") || ins.args[0] != int64(5) || ins.args[1] != "pub" || ins.args[2] != "id" {
		t.Errorf("insert %+v", ins)
	}
}

func TestDeleteAndAttach(t *testing.T) {
	f := &fakeHost{}
	f.install(t)
	_ = deleteSession("tok")
	_ = deleteAccountSessions(3)
	_ = attachAccount("hash", 3)
	requireGuard(t, f)
	c := f.sqlCalls()
	if c[0].args[0] != hashToken("tok") || c[1].args[0] != int64(3) || c[2].args[0] != "hash" || c[2].args[1] != int64(3) {
		t.Errorf("calls %+v", c)
	}
	if !strings.Contains(c[2].sql, "account IS NULL") {
		t.Errorf("attach must only touch signup sessions: %s", c[2].sql)
	}
}

func TestTokens(t *testing.T) {
	a, b := newToken(), newToken()
	if a == b || len(a) != 43 || strings.ContainsAny(a, "+/=") {
		t.Errorf("tokens %q %q", a, b)
	}
	if !ctEq(a, a) || ctEq(a, b) || ctEq("", a) {
		t.Error("ctEq")
	}
	if hashToken(a) == a || len(hashToken(a)) != 64 {
		t.Error("hashToken")
	}
}
