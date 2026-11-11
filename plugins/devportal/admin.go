package main

import (
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"
)

const (
	adminPageSize = 50
	maxQueryLen   = 64
	maxPage       = 100000
)

// accountWhere is shared by the count and the page so they always agree.
const accountWhere = `($1 = '' OR lower(a.login) LIKE $2 ESCAPE '\' OR lower(a.display_name) LIKE $2 ESCAPE '\' OR lower(a.email) LIKE $2 ESCAPE '\')`

const listAccounts = `SELECT jsonb_build_object(` +
	`'total', (SELECT count(*) FROM accounts a WHERE ` + accountWhere + `), ` +
	`'rows', coalesce((SELECT jsonb_agg(to_jsonb(t) ORDER BY t.created_at DESC, t.id DESC) FROM (` +
	`SELECT a.id, a.login, a.display_name, a.email, a.plan, a.status, a.created_at, k.key_id ` +
	`FROM accounts a LEFT JOIN keys k ON k.account = a.id WHERE ` + accountWhere + ` ` +
	`ORDER BY a.created_at DESC, a.id DESC LIMIT 50 OFFSET $3) t), '[]'::jsonb))`

// likePattern turns a search into a LIKE pattern that matches it literally.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(q)) + "%"
}

type adminRow struct {
	ID          int64   `json:"id"`
	Login       string  `json:"login"`
	DisplayName string  `json:"display_name"`
	Email       string  `json:"email"`
	Plan        string  `json:"plan"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	KeyID       *string `json:"key_id"`
}

// handleAdminList lists accounts, newest first, 50 per page. The route is not
// public: the host only lets the owner reach it.
func handleAdminList(r sdk.HTTPRequest) sdk.HTTPResponse {
	q := strings.TrimSpace(first(r.Query["q"]))
	if len(q) > maxQueryLen {
		q = q[:maxQueryLen]
	}
	page, _ := strconv.Atoi(first(r.Query["page"]))
	page = min(max(page, 1), maxPage)
	var out struct {
		Total    int        `json:"total"`
		Rows     []adminRow `json:"rows"`
		Page     int        `json:"page"`
		PageSize int        `json:"page_size"`
	}
	if err := dbJSON(listAccounts, &out, q, likePattern(q), (page-1)*adminPageSize); err != nil {
		return reply(http.StatusServiceUnavailable, map[string]string{"error": "database unavailable"})
	}
	if out.Rows == nil {
		out.Rows = []adminRow{}
	}
	out.Page, out.PageSize = page, adminPageSize
	return reply(http.StatusOK, out)
}

// handleAdminStatus suspends or unsuspends an account. A suspension also ends
// its sessions, so it takes effect at once.
func handleAdminStatus(r sdk.HTTPRequest, suspend bool) sdk.HTTPResponse {
	id, err := strconv.ParseInt(r.PathValues["id"], 10, 64)
	if err != nil || id <= 0 {
		return reply(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}
	status := "active"
	if suspend {
		status = "suspended"
	}
	n, err := dbExecN(`UPDATE accounts SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]string{"error": "database unavailable"})
	}
	if n == 0 {
		return reply(http.StatusNotFound, map[string]string{"error": "no such account"})
	}
	if suspend {
		if err := deleteAccountSessions(id); err != nil {
			return reply(http.StatusServiceUnavailable, map[string]string{"error": "suspended, but its sessions could not be ended"})
		}
	}
	return reply(http.StatusOK, map[string]any{"id": id, "status": status})
}
