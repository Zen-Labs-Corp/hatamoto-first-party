package main

import (
	"encoding/json"
	"time"
)

const (
	sourceGoogle     = "google"
	sourceTrustpilot = "trustpilot"
)

// review is one cached review.
type review struct {
	Source    string
	ExtID     string
	Author    string
	AuthorURL string
	PhotoURL  string
	Body      string
	Link      string
	Rating    int
	Published time.Time
}

// sourceInfo is the per-source summary and refresh state.
type sourceInfo struct {
	Source      string
	Name        string
	ProfileURL  string
	LastError   string
	Rating      float64
	Count       int
	AttemptedAt time.Time
	OKAt        time.Time
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// replaceSource stores a successful refresh: the rows of src become exactly rs
// and the summary is upserted with ok_at = now. Rows are upserted first and
// the ones no longer returned pruned after, so a failure half way keeps data.
func replaceSource(src string, info sourceInfo, rs []review) error {
	ids := make([]string, 0, len(rs))
	for _, r := range rs {
		ids = append(ids, r.ExtID)
		if err := dbExec(`INSERT INTO reviews (source, ext_id, author, author_url, photo_url, rating, body, published_at, link)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::timestamptz, $9)
ON CONFLICT (source, ext_id) DO UPDATE SET author = EXCLUDED.author, author_url = EXCLUDED.author_url,
photo_url = EXCLUDED.photo_url, rating = EXCLUDED.rating, body = EXCLUDED.body,
published_at = EXCLUDED.published_at, link = EXCLUDED.link`,
			src, r.ExtID, r.Author, r.AuthorURL, r.PhotoURL, r.Rating, r.Body, stamp(r.Published), r.Link); err != nil {
			return err
		}
	}
	keep, _ := json.Marshal(ids)
	if err := dbExec(`DELETE FROM reviews WHERE source = $1 AND ext_id NOT IN (SELECT jsonb_array_elements_text($2::jsonb))`,
		src, string(keep)); err != nil {
		return err
	}
	t := stamp(now())
	return dbExec(`INSERT INTO sources (source, rating, review_count, profile_url, name, attempted_at, ok_at, last_error)
VALUES ($1, $2::numeric, $3, $4, $5, $6::timestamptz, $6::timestamptz, '')
ON CONFLICT (source) DO UPDATE SET rating = EXCLUDED.rating, review_count = EXCLUDED.review_count,
profile_url = EXCLUDED.profile_url, name = EXCLUDED.name, attempted_at = EXCLUDED.attempted_at,
ok_at = EXCLUDED.ok_at, last_error = ''`,
		src, info.Rating, info.Count, info.ProfileURL, info.Name, t)
}

// recordFailure stores a failed attempt and its (already redacted) message and
// keeps the cached data.
func recordFailure(src, msg string) error {
	return dbExec(`INSERT INTO sources (source, attempted_at, last_error) VALUES ($1, $2::timestamptz, $3)
ON CONFLICT (source) DO UPDATE SET attempted_at = EXCLUDED.attempted_at, last_error = EXCLUDED.last_error`,
		src, stamp(now()), msg)
}

type sourceRow struct {
	Source      string    `json:"source"`
	Rating      *float64  `json:"rating"`
	Count       int       `json:"review_count"`
	ProfileURL  string    `json:"profile_url"`
	Name        string    `json:"name"`
	AttemptedAt time.Time `json:"attempted_at"`
	OKAt        time.Time `json:"ok_at"`
	LastError   string    `json:"last_error"`
}

// loadSources returns the per-source state keyed by source.
func loadSources() (map[string]sourceInfo, error) {
	var rows []sourceRow
	if err := dbJSON(`SELECT coalesce(jsonb_agg(row_to_json(t)), '[]'::jsonb) FROM (
SELECT source, rating, review_count, profile_url, name, attempted_at, ok_at, last_error FROM sources) t`, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]sourceInfo, len(rows))
	for _, r := range rows {
		info := sourceInfo{Source: r.Source, Name: r.Name, ProfileURL: r.ProfileURL, LastError: r.LastError,
			Count: r.Count, AttemptedAt: r.AttemptedAt, OKAt: r.OKAt}
		if r.Rating != nil {
			info.Rating = *r.Rating
		}
		out[r.Source] = info
	}
	return out, nil
}

type reviewRow struct {
	Source    string    `json:"source"`
	ExtID     string    `json:"ext_id"`
	Author    string    `json:"author"`
	AuthorURL string    `json:"author_url"`
	PhotoURL  string    `json:"photo_url"`
	Body      string    `json:"body"`
	Link      string    `json:"link"`
	Rating    int       `json:"rating"`
	Published time.Time `json:"published_at"`
}

// loadReviews returns every cached review, newest first.
func loadReviews() ([]review, error) {
	var rows []reviewRow
	if err := dbJSON(`SELECT coalesce(jsonb_agg(row_to_json(t)), '[]'::jsonb) FROM (
SELECT source, ext_id, author, author_url, photo_url, rating, body, published_at, link
FROM reviews ORDER BY published_at DESC, source, ext_id) t`, &rows); err != nil {
		return nil, err
	}
	out := make([]review, 0, len(rows))
	for _, r := range rows {
		out = append(out, review(r))
	}
	return out, nil
}

// deleteSource removes a source's reviews and summary.
func deleteSource(src string) error {
	if err := dbExec(`DELETE FROM reviews WHERE source = $1`, src); err != nil {
		return err
	}
	return dbExec(`DELETE FROM sources WHERE source = $1`, src)
}
