package main

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const tokenKey = "ig:token"

// item is one cached feed entry. PublishedAt is RFC 3339, empty when unknown.
type item struct {
	Source      string `json:"source"`
	ID          string `json:"id"`
	Position    int    `json:"position"`
	Title       string `json:"title"`
	Excerpt     string `json:"excerpt"`
	URL         string `json:"url"`
	Image       string `json:"image"`
	Type        string `json:"type"`
	PublishedAt string `json:"published_at"`
}

// replaceItems swaps the rows of one source for items in a single statement
// (each db.exec is its own transaction): rows are upserted and the ones no
// longer listed are deleted, so a failure leaves the old rows and a concurrent
// reader never sees the feed empty. Callers fetch first and call this only on
// success.
func replaceItems(source string, items []item) error {
	seen := map[string]bool{}
	clean := make([]item, 0, len(items))
	for _, it := range items {
		it.Source = source
		it.ID, it.Title, it.Excerpt, it.URL, it.Image, it.Type =
			cleanText(it.ID), cleanText(it.Title), cleanText(it.Excerpt), cleanText(it.URL), cleanText(it.Image), cleanText(it.Type)
		if it.ID == "" || seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		clean = append(clean, it)
	}
	b, _ := json.Marshal(clean)
	return dbExec(`WITH fresh AS (
			SELECT x.id, x.position, x.title, x.excerpt, x.url, x.image, x.type, nullif(x.published_at, '')::timestamptz AS published_at
			  FROM jsonb_to_recordset($2::jsonb) AS x(id text, position integer, title text, excerpt text, url text, image text, type text, published_at text)
		), up AS (
			INSERT INTO item (source, id, position, title, excerpt, url, image, type, published_at)
			SELECT $1::text, id, position, title, excerpt, url, image, type, published_at FROM fresh
			ON CONFLICT (source, id) DO UPDATE SET position = EXCLUDED.position, title = EXCLUDED.title, excerpt = EXCLUDED.excerpt,
				url = EXCLUDED.url, image = EXCLUDED.image, type = EXCLUDED.type, published_at = EXCLUDED.published_at
			RETURNING id
		)
		DELETE FROM item WHERE source = $1 AND id NOT IN (SELECT id FROM fresh)`, source, string(b))
}

// cleanText drops what Postgres text and jsonb refuse: NUL and invalid UTF-8.
func cleanText(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")
}

// listItems returns up to limit items of a source, newest first.
func listItems(source string, limit int) ([]item, error) {
	var out []item
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(c)), '[]'::jsonb) FROM
		(SELECT source, id, position, title, excerpt, url, image, type,
		        coalesce(to_char(published_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '') AS published_at
		   FROM item WHERE source = $1
		  ORDER BY item.published_at DESC NULLS LAST, position LIMIT $2) c`, &out, source, limit)
	return out, err
}

// clearItems deletes the cached rows of one source.
func clearItems(source string) error {
	return dbExec(`DELETE FROM item WHERE source = $1`, source)
}

// clearStatus forgets the refresh outcome of one source.
func clearStatus(source string) error {
	return dbExec(`DELETE FROM source_status WHERE source = $1`, source)
}

// sourceStatus is the last refresh outcome of one source.
type sourceStatus struct {
	Source      string `json:"source"`
	RefreshedAt string `json:"refreshed_at"`
	Error       string `json:"error"`
}

const maxErrorLen = 300

// setStatus records a refresh outcome. A success stamps refreshed_at and clears
// the error; a failure keeps the previous refreshed_at. errText must already be
// redacted; it is cut to 300 bytes.
func setStatus(source, errText string, ok bool) error {
	var at any
	if ok {
		at = now().UTC().Format(time.RFC3339)
		errText = ""
	}
	errText = truncate(errText, maxErrorLen)
	return dbExec(`INSERT INTO source_status (source, refreshed_at, error) VALUES ($1, $2::timestamptz, $3)
		ON CONFLICT (source) DO UPDATE SET refreshed_at = coalesce(EXCLUDED.refreshed_at, source_status.refreshed_at), error = EXCLUDED.error`,
		source, at, errText)
}

// getStatus returns the status of a source (zero value when never refreshed).
func getStatus(source string) (sourceStatus, error) {
	var rows []sourceStatus
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(c)), '[]'::jsonb) FROM
		(SELECT source, coalesce(to_char(refreshed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '') AS refreshed_at, error
		   FROM source_status WHERE source = $1) c`, &rows, source)
	if err != nil || len(rows) == 0 {
		return sourceStatus{}, err
	}
	return rows[0], nil
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// igToken is the Instagram connection, kept in kv (never in the db).
type igToken struct {
	AccessToken string    `json:"access_token"`
	UserID      string    `json:"user_id"`
	Username    string    `json:"username"`
	ObtainedAt  time.Time `json:"obtained_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// loadToken returns the stored token, or nil when not connected.
func loadToken() (*igToken, error) {
	var res struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := callHost("kv.get", map[string]any{"key": tokenKey}, &res); err != nil {
		return nil, err
	}
	if !res.Found {
		return nil, nil
	}
	var t igToken
	if err := json.Unmarshal(res.Value, &t); err != nil || t.AccessToken == "" {
		return nil, err
	}
	return &t, nil
}

// errDisconnected means the owner disconnected while a run was in flight.
var errDisconnected = errors.New("disconnected during the run")

// stillConnected reports whether tok is still the stored connection. A run
// that read the token and then fetched for seconds checks it before writing, so
// a Disconnect in between is not undone. (Reads of kv and db cannot be made one
// atomic step; this leaves a window of milliseconds, not seconds.)
func stillConnected(tok *igToken) bool {
	cur, err := loadToken()
	return err == nil && cur != nil && cur.AccessToken == tok.AccessToken
}

func saveToken(t igToken) error {
	return callHost("kv.set", map[string]any{"key": tokenKey, "value": t}, nil)
}

func deleteToken() error {
	return callHost("kv.delete", map[string]any{"key": tokenKey}, nil)
}
