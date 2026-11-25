package accounts

import (
	"errors"
	"strings"
	"unicode"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// login is a user_logins row joined to its member. Never put it in a page or a JSON answer.
type login struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Account  string `json:"account"`
	PwHash   string `json:"pw_hash"`
	Email    string `json:"email"`
}

const (
	sqlLoginByName = `SELECT to_jsonb(x) FROM (SELECT u.id AS user_id, u.username, l.account, l.pw_hash, l.email FROM users u JOIN user_logins l ON l.user_id = u.id WHERE u.username_lower = lower($1)) x`
	sqlLoginByUser = `SELECT to_jsonb(x) FROM (SELECT u.id AS user_id, u.username, l.account, l.pw_hash, l.email FROM users u JOIN user_logins l ON l.user_id = u.id WHERE u.id = $1) x`
	// One statement: no users row without its login (a conflict on username_lower or commenter_key inserts nothing).
	sqlCreateAccount = `WITH u AS (INSERT INTO users (commenter_key, username, username_lower) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING RETURNING id) INSERT INTO user_logins (user_id, account, pw_hash) SELECT id, $4, $5 FROM u`
	sqlAddLogin      = `INSERT INTO user_logins (user_id, account, pw_hash) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`
	sqlSetHash       = `UPDATE user_logins SET pw_hash = $1, pw_changed_at = now() WHERE user_id = $2`
	sqlSetEmail      = `UPDATE user_logins SET email = $1 WHERE user_id = $2`
)

func oneLogin(sql string, arg any) (*login, error) {
	var l login
	if err := core.QueryJSON(sql, &l, arg); err != nil {
		if errors.Is(err, core.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// loginByName returns the login of the member with that username (case-insensitive), or nil.
func loginByName(name string) (*login, error) { return oneLogin(sqlLoginByName, name) }

// loginByUser returns the login of a member, or nil when the member has none.
func loginByUser(id int64) (*login, error) { return oneLogin(sqlLoginByUser, id) }

// createAccount inserts the member and the login in one statement; false when the name or key is taken.
func createAccount(key, name, account, hash string) (bool, error) {
	n, err := core.Exec(sqlCreateAccount, key, name, strings.ToLower(name), account, hash)
	return n == 1, err
}

// addLogin gives an existing member a login; false when the member or the account already has one.
func addLogin(userID int64, account, hash string) (bool, error) {
	n, err := core.Exec(sqlAddLogin, userID, account, hash)
	return n == 1, err
}

func setHash(userID int64, hash string) error {
	_, err := core.Exec(sqlSetHash, hash, userID)
	return err
}

func setEmail(userID int64, email string) error {
	_, err := core.Exec(sqlSetEmail, email, userID)
	return err
}

// cleanEmail trims s. "" is valid (it clears the email). Otherwise the address is at most 254
// bytes with exactly one "@" and text on both sides, and no space, control character, quote or angle bracket.
func cleanEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	if len(s) > 254 || strings.Count(s, "@") != 1 || strings.HasPrefix(s, "@") || strings.HasSuffix(s, "@") {
		return "", false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`"'<>`, r) {
			return "", false
		}
	}
	return s, true
}
