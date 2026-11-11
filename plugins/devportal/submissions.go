package main

import (
	"errors"
	"time"
)

// submission is one row of the submissions table: what a developer sent, what
// the check found, and where it stands.
type submission struct {
	ID, Account                                                        int64
	Kind, Name, Version, URL, Source, Title, About, Category, SiteType string
	Screenshot, SHA256, Description, SignerKey, Status, Reason         string
	Size                                                               int64
	Permissions, AllowedHosts                                          []string
	Checks                                                             []check
}

// check is one line of the result shown to the developer.
type check struct {
	OK   bool   `json:"ok"`
	Text string `json:"text"`
}

var errInactiveAccount = errors.New("account is not active")

// accountPubkey returns the account's key (base64) and its key id, both empty
// when it has none.
func accountPubkey(id int64) (pub, keyID string, err error) {
	var rows []struct {
		Pubkey string `json:"pubkey"`
		KeyID  string `json:"key_id"`
	}
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (SELECT pubkey, key_id FROM keys WHERE account = $1) t`, &rows, id)
	if err != nil || len(rows) == 0 {
		return "", "", err
	}
	return rows[0].Pubkey, rows[0].KeyID, nil
}

// nameOwner returns the account that owns a name, nil when it is free.
func nameOwner(name string) (*int64, error) {
	var rows []struct {
		Owner int64 `json:"owner"`
	}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (SELECT owner FROM names WHERE name = $1) t`, &rows, name)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0].Owner, nil
}

// listedVersion is the version currently listed (or suspended) for a package,
// "" when there is none.
func listedVersion(kind, name string) (string, error) {
	var rows []struct {
		Version string `json:"version"`
	}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (SELECT version FROM listings `+
		`WHERE kind = $1 AND name = $2 AND status IN ('active', 'suspended')) t`, &rows, kind, name)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].Version, nil
}

// recentSubmissions counts the account's submissions since a time.
func recentSubmissions(account int64, since time.Time) (int, error) {
	var doc struct {
		N int `json:"n"`
	}
	err := dbJSON(`SELECT jsonb_build_object('n', count(id)) FROM submissions WHERE account = $1 AND created_at > $2::timestamptz`,
		&doc, account, timeArg(since))
	return doc.N, err
}

// keyRevoked reports whether the key is on the revocation list.
func keyRevoked(pub string) (bool, error) {
	var doc struct {
		N int `json:"n"`
	}
	err := dbJSON(`SELECT jsonb_build_object('n', count(id)) FROM revocations WHERE signer_key = $1`, &doc, pub)
	return doc.N > 0, err
}

// insertSubmission stores a submission and returns its id. The insert checks
// that the account is still active, so a suspension between page load and POST
// wins (errInactiveAccount).
func insertSubmission(s submission) (int64, error) {
	var rows []struct {
		ID int64 `json:"id"`
	}
	perms, hosts, checks := jsonList(s.Permissions), jsonList(s.AllowedHosts), jsonChecks(s.Checks)
	err := dbJSON(`WITH ins AS (INSERT INTO submissions (account, kind, name, version, url, source, title, about, category, site_type, screenshot, `+
		`sha256, size, permissions, allowed_hosts, description, signer_key, checks, status, reason) `+
		`SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15::jsonb, $16, $17, $18::jsonb, $19, $20 `+
		`WHERE EXISTS (SELECT 1 FROM accounts WHERE id = $1 AND status = 'active') RETURNING id) `+
		`SELECT coalesce(jsonb_agg(jsonb_build_object('id', ins.id)), '[]'::jsonb) FROM ins`,
		&rows, s.Account, s.Kind, s.Name, s.Version, s.URL, s.Source, s.Title, s.About, s.Category, s.SiteType, s.Screenshot,
		s.SHA256, s.Size, perms, hosts, s.Description, s.SignerKey, checks, s.Status, s.Reason)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, errInactiveAccount
	}
	return rows[0].ID, nil
}
