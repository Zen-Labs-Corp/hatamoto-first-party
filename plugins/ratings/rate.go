package main

import (
	"encoding/json"
	"net/url"
	"strings"

	"hatamoto.dev/sdk"
)

const (
	maxBody      = 8192
	voterMaxAge  = 31536000 // one year, the most the host allows
	voterCookie  = "voter"
	fetchSiteHdr = "sec-fetch-site"
)

const (
	sqlVoteKeep = `INSERT INTO votes (entity_id, voter, stars) VALUES ($1::uuid, $2, $3)
ON CONFLICT (entity_id, voter) DO NOTHING`
	sqlVoteChange = `INSERT INTO votes (entity_id, voter, stars) VALUES ($1::uuid, $2, $3)
ON CONFLICT (entity_id, voter) DO UPDATE SET stars = EXCLUDED.stars, updated = now()`
	sqlPage = `INSERT INTO pages (entity_id, path) VALUES ($1::uuid, $2)
ON CONFLICT (entity_id) DO UPDATE SET path = EXCLUDED.path, updated = now()`
)

func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "POST /rate":
		return handleRate(r), nil
	}
	return reply(404, map[string]string{"error": "not found"}), nil
}

// reply answers JSON; a value that cannot be encoded is an empty object.
func reply(code int, v any) sdk.HTTPResponse {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("{}")
	}
	return sdk.HTTPResponse{Status: code, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}, Body: string(b)}
}

// see answers 303 to a validated same-site path. Nothing else is sent to a
// visitor, so the response never depends on whether the vote counted.
func see(back string, cookies ...sdk.SetCookie) sdk.HTTPResponse {
	return sdk.HTTPResponse{
		Status:     303,
		Headers:    map[string]string{"Location": back, "Cache-Control": "no-store"},
		SetCookies: cookies,
	}
}

// handleRate records one vote. It never returns an error: invalid input, a
// cross-site post and database errors all end in a redirect without a vote
// (only a body over maxBody is refused). Nothing about the visitor is stored:
// the voter key is a random cookie value and the only request header read is
// sec-fetch-site.
func handleRate(r sdk.HTTPRequest) sdk.HTTPResponse {
	if len(r.Body) > maxBody || r.BodyB64 != "" {
		return sdk.HTTPResponse{Status: 413, Headers: map[string]string{"Content-Type": "text/plain", "Cache-Control": "no-store"}, Body: "too large"}
	}
	form, err := url.ParseQuery(r.Body)
	if err != nil {
		return see("/")
	}
	back, ok := cleanPath(form.Get("return_to"))
	if !ok {
		back = "/"
	}
	id := strings.ToLower(form.Get("entity_id"))
	stars := form.Get("stars")
	if r.Headers[fetchSiteHdr] == "cross-site" || !isUUID(id) || len(stars) != 1 || stars[0] < '1' || stars[0] > '5' {
		return see(back)
	}
	cfg := loadConfig()
	if !cfg.allows(form.Get("schema")) {
		return see(back)
	}

	var cookies []sdk.SetCookie
	voter := r.Cookies[voterCookie]
	if !validVoter(voter) {
		if voter, err = newVoter(); err != nil {
			return see(back)
		}
		cookies = []sdk.SetCookie{{Name: voterCookie, Value: voter, MaxAge: voterMaxAge, Path: "/"}}
	}

	query := sqlVoteKeep
	if cfg.AllowChange {
		query = sqlVoteChange
	}
	if err := dbExec(query, id, voter, int(stars[0]-'0')); err != nil {
		return see(back, cookies...)
	}
	if p, ok := cleanPath(form.Get("path")); ok {
		_ = dbExec(sqlPage, id, p)
	}
	return see(back, cookies...)
}
