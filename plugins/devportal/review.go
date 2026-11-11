package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"hatamoto.dev/sdk"
)

// The Review routes are owner-only admin routes (the host checks the role).
// A decision is committed first; mail and publishing come after and never
// change its outcome: their failures show up as a warning in the answer.

const reviewLimit = "100" // rows per list

const reviewList = `SELECT jsonb_build_object(` +
	`'waiting', coalesce((SELECT jsonb_agg(to_jsonb(w) ORDER BY w.created_at, w.id) FROM (` +
	`SELECT s.id, s.account, s.kind, s.name, s.version, s.url, s.source, s.title, s.about, s.size, s.permissions, s.allowed_hosts, s.checks, s.created_at, ` +
	`a.login, left(coalesce(k.key_id, ''), 16) AS key_id, (coalesce(a.email, '') <> '') AS email_present ` +
	`FROM submissions s JOIN accounts a ON a.id = s.account LEFT JOIN keys k ON k.account = s.account ` +
	`WHERE s.status = 'waiting' ORDER BY s.created_at, s.id LIMIT ` + reviewLimit + `) w), '[]'::jsonb), ` +
	`'listed', coalesce((SELECT jsonb_agg(to_jsonb(l) ORDER BY l.name, l.kind) FROM (` +
	`SELECT g.id, g.account, g.kind, g.name, g.version, g.status, g.reason, g.check_failures, g.checked_at, g.listed_at, ` +
	`g.entry ->> 'url' AS url, g.entry ->> 'source' AS source, g.entry ->> 'title' AS title, (g.entry ->> 'size')::bigint AS size, ` +
	`g.entry -> 'permissions' AS permissions, a.login, left(coalesce(k.key_id, ''), 16) AS key_id, (coalesce(a.email, '') <> '') AS email_present ` +
	`FROM listings g JOIN accounts a ON a.id = g.account LEFT JOIN keys k ON k.account = g.account ` +
	`WHERE g.status <> 'revoked' ORDER BY g.name, g.kind LIMIT ` + reviewLimit + `) l), '[]'::jsonb), ` +
	`'revoked', coalesce((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id DESC) FROM (` +
	`SELECT v.id, v.name, v.version, v.signer_key, v.reason, v.created_at FROM revocations v ORDER BY v.id DESC LIMIT ` + reviewLimit + `) r), '[]'::jsonb))`

// reasonOf reads the reason from the JSON body: 1 to 200 runes after control
// characters are dropped.
func reasonOf(r sdk.HTTPRequest) (string, bool) {
	var body struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(r.Body), &body) != nil {
		return "", false
	}
	reason := strings.TrimSpace(strings.Map(func(c rune) rune {
		switch {
		case unicode.IsSpace(c):
			return ' '
		case unicode.IsControl(c):
			return -1
		}
		return c
	}, body.Reason))
	reason = cutRunes(reason, maxReasonLen)
	return reason, reason != ""
}

func idOf(r sdk.HTTPRequest) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValues["id"], 10, 64)
	return id, err == nil && id > 0
}

func replyErr(code int, msg string) sdk.HTTPResponse {
	return reply(code, map[string]string{"error": msg})
}

func replyBusy() sdk.HTTPResponse {
	return replyErr(http.StatusServiceUnavailable, "database unavailable")
}

// handleReviewList lists what waits, what is listed and what was revoked.
func handleReviewList(sdk.HTTPRequest) sdk.HTTPResponse {
	var doc struct {
		Waiting []json.RawMessage `json:"waiting"`
		Listed  []json.RawMessage `json:"listed"`
		Revoked []json.RawMessage `json:"revoked"`
	}
	if err := dbJSON(reviewList, &doc); err != nil {
		return replyBusy()
	}
	for _, l := range []*[]json.RawMessage{&doc.Waiting, &doc.Listed, &doc.Revoked} {
		if *l == nil {
			*l = []json.RawMessage{}
		}
	}
	return reply(http.StatusOK, doc)
}

// reviewSub is a submission with its developer, as the review routes read it.
type reviewSub struct {
	ID            int64    `json:"id"`
	Account       int64    `json:"account"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	URL           string   `json:"url"`
	Source        string   `json:"source"`
	Title         string   `json:"title"`
	About         string   `json:"about"`
	Category      string   `json:"category"`
	SiteType      string   `json:"site_type"`
	Screenshot    string   `json:"screenshot"`
	SHA256        string   `json:"sha256"`
	Size          int64    `json:"size"`
	Permissions   []string `json:"permissions"`
	AllowedHosts  []string `json:"allowed_hosts"`
	Description   string   `json:"description"`
	SignerKey     string   `json:"signer_key"`
	Status        string   `json:"status"`
	Login         string   `json:"login"`
	AccountStatus string   `json:"account_status"`
	Email         string   `json:"email"`
}

func (s reviewSub) submission() submission {
	return submission{ID: s.ID, Account: s.Account, Kind: s.Kind, Name: s.Name, Version: s.Version, URL: s.URL, Source: s.Source, Title: s.Title,
		About: s.About, Category: s.Category, SiteType: s.SiteType, Screenshot: s.Screenshot, SHA256: s.SHA256, Description: s.Description,
		SignerKey: s.SignerKey, Status: s.Status, Size: s.Size, Permissions: s.Permissions, AllowedHosts: s.AllowedHosts}
}

func loadReviewSub(id int64) (*reviewSub, error) {
	var rows []reviewSub
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (`+
		`SELECT s.id, s.account, s.kind, s.name, s.version, s.url, s.source, s.title, s.about, s.category, s.site_type, s.screenshot, s.sha256, s.size, `+
		`s.permissions, s.allowed_hosts, s.description, s.signer_key, s.status, a.login, a.status AS account_status, coalesce(a.email, '') AS email `+
		`FROM submissions s JOIN accounts a ON a.id = s.account WHERE s.id = $1) t`, &rows, id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// publishResult reports the outcome of publish() as the answer's fields.
func publishResult(out map[string]any) {
	err := publish()
	if err == nil {
		out["published"] = true
		return
	}
	out["published"] = false
	switch {
	case errors.Is(err, errNoListingKey), errors.Is(err, errBadListing), errors.Is(err, errWrongListing), errors.Is(err, errIndexTooBig):
		out["warning"] = "The index was not published: " + err.Error() + "."
	default:
		out["warning"] = "The index could not be published. It is published again at the next daily run."
		logf("warn", "publish failed")
	}
}

// handleApprove lists a waiting submission. Every gate is one statement, and
// the status update is the one that decides who wins a double click.
func handleApprove(r sdk.HTTPRequest) sdk.HTTPResponse {
	id, ok := idOf(r)
	if !ok {
		return replyErr(http.StatusBadRequest, "invalid id")
	}
	s, err := loadReviewSub(id)
	if err != nil {
		return replyBusy()
	}
	if s == nil {
		return replyErr(http.StatusNotFound, "no such submission")
	}
	if s.Status != "waiting" {
		return replyErr(http.StatusConflict, "already decided")
	}
	if s.AccountStatus != "active" {
		return replyErr(http.StatusConflict, "the developer account is suspended")
	}
	if revoked, err := keyRevoked(s.SignerKey); err != nil {
		return replyBusy()
	} else if revoked {
		return replyErr(http.StatusConflict, "the developer key was revoked")
	}
	if owner, err := nameOwner(s.Name); err != nil {
		return replyBusy()
	} else if !ownsOrFree(owner, s.Account) {
		return replyErr(http.StatusConflict, "the name belongs to another developer")
	}
	n, err := dbExecN(`UPDATE submissions SET status = 'listed', decided_at = now() WHERE id = $1 AND status = 'waiting'`, id)
	if err != nil {
		return replyBusy()
	}
	if n == 0 {
		return replyErr(http.StatusConflict, "already decided")
	}
	undo := func() {
		_ = dbExec(`UPDATE submissions SET status = 'waiting', decided_at = NULL WHERE id = $1 AND status = 'listed'`, id)
	}
	if err := dbExec(`INSERT INTO names (name, owner) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`, s.Name, s.Account); err != nil {
		undo()
		return replyBusy()
	}
	if owner, err := nameOwner(s.Name); err != nil {
		undo()
		return replyBusy()
	} else if !ownsOrFree(owner, s.Account) {
		undo()
		return replyErr(http.StatusConflict, "the name belongs to another developer")
	}
	entry, err := json.Marshal(entryFrom(s.submission(), s.Login, s.SignerKey))
	if err != nil {
		undo()
		return replyErr(http.StatusInternalServerError, "could not build the entry")
	}
	// The listing being replaced moves to superseded before the upsert.
	if err := dbExec(`UPDATE submissions SET status = 'superseded' WHERE id = (SELECT submission FROM listings WHERE kind = $1 AND name = $2) AND id <> $3 AND status = 'listed'`,
		s.Kind, s.Name, id); err != nil {
		undo()
		return replyBusy()
	}
	err = dbExec(`INSERT INTO listings (kind, name, account, submission, version, entry, status, reason, check_failures, checked_at, listed_at) `+
		`VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'active', '', 0, now(), now()) `+
		`ON CONFLICT (kind, name) DO UPDATE SET account = EXCLUDED.account, submission = EXCLUDED.submission, version = EXCLUDED.version, `+
		`entry = EXCLUDED.entry, status = 'active', reason = '', check_failures = 0, checked_at = now(), listed_at = now()`,
		s.Kind, s.Name, s.Account, id, s.Version, string(entry))
	if err != nil {
		undo()
		return replyBusy()
	}
	notify(s.Email, fmt.Sprintf("%s %s is listed", s.Name, s.Version),
		fmt.Sprintf("%s %s was approved and is now in the developer listing. Sites that use the developer registry can install it.\n", s.Name, s.Version))
	out := map[string]any{"ok": true, "id": id, "status": "listed"}
	publishResult(out)
	return reply(http.StatusOK, out)
}

// handleReject turns a waiting submission down and tells the developer why.
func handleReject(r sdk.HTTPRequest) sdk.HTTPResponse {
	id, ok := idOf(r)
	if !ok {
		return replyErr(http.StatusBadRequest, "invalid id")
	}
	reason, ok := reasonOf(r)
	if !ok {
		return replyErr(http.StatusBadRequest, "a reason is required")
	}
	s, err := loadReviewSub(id)
	if err != nil {
		return replyBusy()
	}
	if s == nil {
		return replyErr(http.StatusNotFound, "no such submission")
	}
	n, err := dbExecN(`UPDATE submissions SET status = 'rejected', reason = $2, decided_at = now() WHERE id = $1 AND status = 'waiting'`, id, reason)
	if err != nil {
		return replyBusy()
	}
	if n == 0 {
		return replyErr(http.StatusConflict, "already decided")
	}
	notify(s.Email, fmt.Sprintf("%s %s was not approved", s.Name, s.Version),
		fmt.Sprintf("%s %s was not approved.\n\nReason: %s\n\nFix this and submit it again from your developer dashboard.\n", s.Name, s.Version, reason))
	return reply(http.StatusOK, map[string]any{"ok": true, "id": id, "status": "rejected"})
}

type reviewListing struct {
	ID         int64  `json:"id"`
	Account    int64  `json:"account"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Submission int64  `json:"submission"`
	Status     string `json:"status"`
	Email      string `json:"email"`
}

// handleRevokeVersion withdraws one listed version: it leaves the index and
// the revocation list names it.
func handleRevokeVersion(r sdk.HTTPRequest) sdk.HTTPResponse {
	id, ok := idOf(r)
	if !ok {
		return replyErr(http.StatusBadRequest, "invalid id")
	}
	reason, ok := reasonOf(r)
	if !ok {
		return replyErr(http.StatusBadRequest, "a reason is required")
	}
	var rows []reviewListing
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (`+
		`SELECT g.id, g.account, g.name, g.version, g.submission, g.status, coalesce(a.email, '') AS email `+
		`FROM listings g JOIN accounts a ON a.id = g.account WHERE g.id = $1) t`, &rows, id)
	if err != nil {
		return replyBusy()
	}
	if len(rows) == 0 {
		return replyErr(http.StatusNotFound, "no such listing")
	}
	g := rows[0]
	n, err := dbExecN(`UPDATE listings SET status = 'revoked', reason = $2 WHERE id = $1 AND status <> 'revoked'`, id, reason)
	if err != nil {
		return replyBusy()
	}
	if n == 0 {
		return replyErr(http.StatusConflict, "already revoked")
	}
	if err := dbExec(`UPDATE submissions SET status = 'revoked', reason = $2 WHERE id = $1`, g.Submission, reason); err != nil {
		logf("warn", "submission status not updated")
	}
	// Name and version only: a signer key here would revoke the developer's whole key.
	if err := dbExec(`INSERT INTO revocations (name, version, reason) VALUES ($1, $2, $3)`, g.Name, g.Version, reason); err != nil {
		return replyErr(http.StatusServiceUnavailable, "the listing is revoked, but the revocation could not be recorded")
	}
	notify(g.Email, fmt.Sprintf("%s %s was revoked", g.Name, g.Version),
		fmt.Sprintf("%s %s was removed from the developer listing and revoked.\n\nReason: %s\n", g.Name, g.Version, reason))
	out := map[string]any{"ok": true, "id": id, "status": "revoked"}
	publishResult(out)
	return reply(http.StatusOK, out)
}

// handleRevokeKey revokes a developer's signing key: every listing of the
// account leaves the index. Safe to repeat: a second call finishes what the
// first did not.
func handleRevokeKey(r sdk.HTTPRequest) sdk.HTTPResponse {
	id, ok := idOf(r)
	if !ok {
		return replyErr(http.StatusBadRequest, "invalid id")
	}
	reason, ok := reasonOf(r)
	if !ok {
		return replyErr(http.StatusBadRequest, "a reason is required")
	}
	pub, _, err := accountPubkey(id)
	if err != nil {
		return replyBusy()
	}
	if pub == "" {
		return replyErr(http.StatusNotFound, "that account has no key")
	}
	n, err := dbExecN(`INSERT INTO revocations (signer_key, reason) SELECT $1, $2 `+
		`WHERE NOT EXISTS (SELECT 1 FROM revocations WHERE signer_key = $1)`, pub, reason)
	if err != nil {
		return replyBusy()
	}
	suspended, err := dbExecN(`UPDATE listings SET status = 'suspended', reason = $2 WHERE account = $1 AND status = 'active'`, id, reason)
	if err != nil {
		return replyErr(http.StatusServiceUnavailable, "the key is revoked, but its listings could not be suspended: repeat the action")
	}
	if n > 0 {
		var rows []struct {
			Email string `json:"email"`
		}
		if dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('email', coalesce(email, ''))), '[]'::jsonb) FROM accounts WHERE id = $1`, &rows, id) == nil && len(rows) > 0 {
			notify(rows[0].Email, "Your signing key was revoked",
				fmt.Sprintf("Your signing key was revoked and your packages were removed from the developer listing.\n\nReason: %s\n", reason))
		}
	}
	out := map[string]any{"ok": true, "id": id, "suspended": suspended}
	publishResult(out)
	return reply(http.StatusOK, out)
}
