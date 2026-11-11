package main

import (
	"encoding/json"
	"errors"
	"time"
)

// sessionTTL is how long a session lives after its last use.
const sessionTTL = 30 * 24 * time.Hour

// slideAfter is how old a session's last extension must be before it is
// extended again, so a page view does not write every time.
const slideAfter = time.Hour

var (
	errKeyTaken = errors.New("key already registered")
	errHasKey   = errors.New("account already has a key")
	errInactive = errors.New("account is not active")
	errExists   = errors.New("account already exists")
)

// ghIdentity is what GitHub told us about the person signing in.
type ghIdentity struct {
	ID     int64    `json:"id"`
	Login  string   `json:"login"`
	Emails []string `json:"emails"`
}

// session is a signed-in browser. AccountID is nil until the terms are
// accepted; the GitHub identity then rides on the session.
type session struct {
	ID          string // sha256 hex of the cookie token
	AccountID   *int64
	CSRF        string
	GH          ghIdentity
	Status      string // account status, "" without an account
	Login       string
	DisplayName string
	Email       string
	Plan        string
	KeyID       string
	KeyAdded    time.Time
}

// accountRef is the minimum the sign-in needs about an existing account.
type accountRef struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Login  string `json:"login"`
}

func timeArg(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// createSession stores a session and returns the cookie token and CSRF token.
func createSession(accountID *int64, gh ghIdentity) (token, csrf string, err error) {
	token, csrf = newToken(), newToken()
	var ghID, ghLogin, ghEmails any
	if accountID == nil {
		b, _ := json.Marshal(gh.Emails)
		ghID, ghLogin, ghEmails = gh.ID, gh.Login, string(b)
	}
	err = dbExec(`INSERT INTO sessions (id, account, csrf, gh_id, gh_login, gh_emails, expires_at) `+
		`VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::timestamptz)`,
		hashToken(token), accountID, csrf, ghID, ghLogin, ghEmails, timeArg(now().Add(sessionTTL)))
	return token, csrf, err
}

// lookupSession resolves a cookie token. An empty, unknown or expired token
// gives nil, nil. A session older than slideAfter is extended.
func lookupSession(token string) (*session, error) {
	if token == "" || len(token) > 128 {
		return nil, nil
	}
	var rows []struct {
		ID          string          `json:"id"`
		Account     *int64          `json:"account"`
		CSRF        string          `json:"csrf"`
		GHID        *int64          `json:"gh_id"`
		GHLogin     *string         `json:"gh_login"`
		GHEmails    json.RawMessage `json:"gh_emails"`
		ExpiresAt   string          `json:"expires_at"`
		Status      *string         `json:"status"`
		Login       *string         `json:"login"`
		DisplayName *string         `json:"display_name"`
		Email       *string         `json:"email"`
		Plan        *string         `json:"plan"`
		KeyID       *string         `json:"key_id"`
		KeyAdded    *string         `json:"key_added"`
	}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (`+
		`SELECT s.id, s.account, s.csrf, s.gh_id, s.gh_login, s.gh_emails, s.expires_at, `+
		`a.status, a.login, a.display_name, a.email, a.plan, k.key_id, k.created_at AS key_added `+
		`FROM sessions s LEFT JOIN accounts a ON a.id = s.account LEFT JOIN keys k ON k.account = a.id `+
		`WHERE s.id = $1 AND s.expires_at > $2::timestamptz) t`,
		&rows, hashToken(token), timeArg(now()))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	r := rows[0]
	s := &session{ID: r.ID, AccountID: r.Account, CSRF: r.CSRF}
	s.Status, s.Login, s.DisplayName = deref(r.Status), deref(r.Login), deref(r.DisplayName)
	s.Email, s.Plan, s.KeyID = deref(r.Email), deref(r.Plan), deref(r.KeyID)
	if r.GHID != nil {
		s.GH.ID = *r.GHID
	}
	s.GH.Login = deref(r.GHLogin)
	_ = json.Unmarshal(r.GHEmails, &s.GH.Emails)
	if t, err := time.Parse(time.RFC3339Nano, deref(r.KeyAdded)); err == nil {
		s.KeyAdded = t
	}
	if exp, err := time.Parse(time.RFC3339Nano, r.ExpiresAt); err == nil && exp.Sub(now()) < sessionTTL-slideAfter {
		// Sliding expiry is best effort: a failed write must not sign the user out.
		_ = dbExec(`UPDATE sessions SET expires_at = $2::timestamptz WHERE id = $1`, s.ID, timeArg(now().Add(sessionTTL)))
	}
	return s, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// deleteSession removes the session of a cookie token.
func deleteSession(token string) error {
	return dbExec(`DELETE FROM sessions WHERE id = $1`, hashToken(token))
}

// deleteAccountSessions signs an account out everywhere.
func deleteAccountSessions(accountID int64) error {
	return dbExec(`DELETE FROM sessions WHERE account = $1`, accountID)
}

// attachAccount turns a signup session into an account session.
func attachAccount(sessionID string, accountID int64) error {
	return dbExec(`UPDATE sessions SET account = $2, gh_id = NULL, gh_login = NULL, gh_emails = NULL `+
		`WHERE id = $1 AND account IS NULL`, sessionID, accountID)
}

// findAccount looks an account up by its GitHub id, the only stable identity.
func findAccount(ghID int64) (*accountRef, error) {
	var rows []accountRef
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (`+
		`SELECT id, status, login FROM accounts WHERE github_id = $1) t`, &rows, ghID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// signInAccount finds the account of a GitHub id and refreshes its login,
// which GitHub lets people change. nil, nil means no account yet.
func signInAccount(ghID int64, login string) (*accountRef, error) {
	a, err := findAccount(ghID)
	if err != nil || a == nil {
		return a, err
	}
	if a.Login != login && login != "" {
		if err := dbExec(`UPDATE accounts SET login = $2, updated_at = now() WHERE github_id = $1`, ghID, login); err != nil {
			return nil, err
		}
		a.Login = login
	}
	return a, nil
}

// createAccount stores a new free account for a GitHub id; the caller checked
// the terms box. errExists when the id already has one.
func createAccount(gh ghIdentity, display, email string) (int64, error) {
	n, err := dbExecN(`INSERT INTO accounts (github_id, login, display_name, email, plan, status, terms_accepted_at) `+
		`VALUES ($1, $2, $3, $4, 'free', 'active', $5::timestamptz) ON CONFLICT (github_id) DO NOTHING`,
		gh.ID, gh.Login, display, email, timeArg(now()))
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, errExists
	}
	a, err := findAccount(gh.ID)
	if err != nil {
		return 0, err
	}
	if a == nil {
		return 0, errors.New("account vanished after insert")
	}
	return a.ID, nil
}

// addKey registers the account's single key. It checks for conflicts first so
// the common refusals do not depend on database error text, then inserts; the
// insert itself checks that the account is active, so a suspension between page
// load and POST wins.
func addKey(accountID int64, pubkey, keyID string) error {
	if err := keyConflict(accountID, pubkey); err != nil {
		return err
	}
	n, err := dbExecN(`INSERT INTO keys (account, pubkey, key_id) `+
		`SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM accounts WHERE id = $1 AND status = 'active')`,
		accountID, pubkey, keyID)
	if err != nil {
		// A concurrent request may have won the unique index; name it if so.
		if cerr := keyConflict(accountID, pubkey); cerr != nil {
			return cerr
		}
		return err
	}
	if n == 0 {
		return errInactive
	}
	return nil
}

// keyConflict returns errHasKey, errKeyTaken, a query error or nil.
func keyConflict(accountID int64, pubkey string) error {
	var rows []struct {
		Mine  int `json:"mine"`
		Taken int `json:"taken"`
	}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (`+
		`SELECT (SELECT count(*) FROM keys WHERE account = $1) AS mine, `+
		`(SELECT count(*) FROM keys WHERE pubkey = $2) AS taken) t`, &rows, accountID, pubkey)
	switch {
	case err != nil:
		return err
	case len(rows) == 0:
		return nil
	case rows[0].Mine > 0:
		return errHasKey
	case rows[0].Taken > 0:
		return errKeyTaken
	}
	return nil
}
