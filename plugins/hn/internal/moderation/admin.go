package moderation

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	sqlQueue   = `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.dead, x.flags DESC, x.last DESC), '[]'::jsonb) FROM (SELECT f.kind, f.target_id AS id, COALESCE(s.item_no, cs.item_no, 0) AS item_no, COALESCE(s.title, '') AS title, COALESCE(cm.body_html, '') AS body_html, COALESCE(su.username, cm.author_name, '') AS "by", f.n AS flags, f.last, (k.cause IS NOT NULL OR COALESCE(s.dead, false)) AS dead, COALESCE(k.cause, CASE WHEN s.dead THEN 'flags' ELSE '' END) AS cause FROM (SELECT kind, target_id, count(*) AS n, max(created_at) AS last FROM flags GROUP BY kind, target_id) f LEFT JOIN story_meta s ON f.kind = 'story' AND s.entity_id = f.target_id LEFT JOIN users su ON su.id = s.author_id LEFT JOIN comment_meta cm ON f.kind = 'comment' AND cm.comment_id = f.target_id LEFT JOIN story_meta cs ON cs.entity_id = cm.entity_id LEFT JOIN kills k ON k.kind = f.kind AND k.target_id = f.target_id WHERE s.entity_id IS NOT NULL OR cm.comment_id IS NOT NULL ORDER BY (k.cause IS NOT NULL OR COALESCE(s.dead, false)), f.n DESC, f.last DESC LIMIT 100) x`
	sqlUsers   = `SELECT COALESCE(jsonb_agg(to_jsonb(x)), '[]'::jsonb) FROM (SELECT u.username AS name, b.mode, b.reason, b.created_at FROM bans b JOIN users u ON u.id = b.user_id ORDER BY b.created_at DESC LIMIT 200) x`
	sqlDomains = `SELECT COALESCE(jsonb_agg(to_jsonb(x)), '[]'::jsonb) FROM (SELECT domain, reason, created_at FROM banned_domains ORDER BY created_at DESC LIMIT 500) x`
	sqlNets    = `SELECT jsonb_build_object('bans', (SELECT COALESCE(jsonb_agg(to_jsonb(b)), '[]'::jsonb) FROM (SELECT net_key, reason, expires_at FROM net_bans WHERE expires_at IS NULL OR expires_at > $1::timestamptz ORDER BY created_at DESC LIMIT 200) b), 'noisy', (SELECT COALESCE(jsonb_agg(to_jsonb(n)), '[]'::jsonb) FROM (SELECT key AS net_key, count(*) AS events FROM rate_events WHERE kind IN ('vote_net', 'signup_net') AND created_at > $1::timestamptz - interval '24 hours' GROUP BY key ORDER BY count(*) DESC LIMIT 50) n))`
	sqlLogList = `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.id DESC), '[]'::jsonb) FROM (SELECT id, created_at AS at, actor, action, target, detail FROM mod_log ORDER BY id DESC LIMIT 200) x`

	sqlBanUpsert    = `INSERT INTO bans (user_id, mode, reason, created_at) VALUES ($1, $2, $3, $4::timestamptz) ON CONFLICT (user_id) DO UPDATE SET mode = EXCLUDED.mode, reason = EXCLUDED.reason`
	sqlBanDelete    = `DELETE FROM bans WHERE user_id = $1`
	sqlDomainUpsert = `INSERT INTO banned_domains (domain, reason, created_at) VALUES ($1, $2, $3::timestamptz) ON CONFLICT (domain) DO UPDATE SET reason = EXCLUDED.reason`
	sqlDomainDelete = `DELETE FROM banned_domains WHERE domain = $1`
	sqlNetUpsert    = `INSERT INTO net_bans (net_key, reason, expires_at, created_at) VALUES ($1, $2, $3::timestamptz, $4::timestamptz) ON CONFLICT (net_key) DO UPDATE SET reason = EXCLUDED.reason, expires_at = EXCLUDED.expires_at`
	sqlNetDelete    = `DELETE FROM net_bans WHERE net_key = $1`
)

var (
	domainRe = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)
	netKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
)

func registerAdmin(r *core.Registry) {
	route := func(key string, h func(*core.Request) sdk.HTTPResponse) {
		r.Route(key, func(q *core.Request) (sdk.HTTPResponse, error) {
			if q.User == nil || q.User.Role != "owner" {
				return core.JSONError(403, "owner only"), nil
			}
			return h(q), nil
		})
	}
	route("GET /admin/queue", handleQueue)
	route("GET /admin/log", func(*core.Request) sdk.HTTPResponse { return list("log", sqlLogList) })
	route("POST /admin/users/{name}/ban", handleBan)
	route("POST /admin/users/{name}/unban", handleUnban)
	route("POST /admin/domains", handleDomainBan)
	route("POST /admin/domains/remove", handleDomainRemove)
	route("POST /admin/nets/ban", handleNetBan)
	route("POST /admin/nets/unban", handleNetUnban)
	route("POST /admin/items/kill", func(q *core.Request) sdk.HTTPResponse { return handleItem(q, true) })
	route("POST /admin/items/restore", func(q *core.Request) sdk.HTTPResponse { return handleItem(q, false) })
}

func writeJSON(status int, v any) sdk.HTTPResponse {
	body, err := json.Marshal(v)
	if err != nil {
		return core.JSONError(500, core.NoticeText(string(nError)))
	}
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}, Body: string(body)}
}

func failed() sdk.HTTPResponse { return core.JSONError(500, core.NoticeText(string(nError))) }

func ok(extra map[string]any) sdk.HTTPResponse {
	out := map[string]any{"ok": true}
	for k, v := range extra {
		out[k] = v
	}
	return writeJSON(200, out)
}

// decodeJSON reads the JSON body into v: an empty body reads as {}, an unknown
// field is an error.
func decodeJSON(q *core.Request, v any) (sdk.HTTPResponse, bool) {
	if len(q.Body) > maxModBody {
		return core.JSONError(413, "too large"), false
	}
	body := strings.TrimSpace(q.Body)
	if body == "" {
		body = "{}"
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		return core.JSONError(400, "invalid json"), false
	}
	return sdk.HTTPResponse{}, true
}

// cleanReason trims a reason and refuses one that is long or has control characters.
func cleanReason(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 200 {
		return "", false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return "", false
		}
	}
	return s, true
}

// list answers {key: <rows>} for a query that returns one json array.
func list(key, sql string, args ...any) sdk.HTTPResponse {
	var rows []map[string]any
	if err := core.QueryJSON(sql, &rows, args...); err != nil {
		return failed()
	}
	return writeJSON(200, map[string]any{key: rows})
}

// excerpt is the plain text of a comment, at most 80 runes.
func excerpt(bodyHTML string) string {
	var b strings.Builder
	in := false
	for _, c := range bodyHTML {
		switch {
		case c == '<':
			in = true
			b.WriteByte(' ')
		case c == '>' && in:
			in = false
		case !in:
			b.WriteRune(c)
		}
	}
	s := strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80]) + "…"
	}
	return s
}

func handleQueue(q *core.Request) sdk.HTTPResponse {
	view := "queue"
	if v := q.Query["view"]; len(v) > 0 && v[0] != "" {
		view = v[0]
	}
	switch view {
	case "queue":
		var rows []struct {
			Kind     string    `json:"kind"`
			ID       string    `json:"id"`
			ItemNo   int64     `json:"item_no"`
			Title    string    `json:"title"`
			BodyHTML string    `json:"body_html"`
			By       string    `json:"by"`
			Flags    int       `json:"flags"`
			Dead     bool      `json:"dead"`
			Cause    string    `json:"cause"`
			Last     time.Time `json:"last"`
		}
		if err := core.QueryJSON(sqlQueue, &rows); err != nil {
			return failed()
		}
		items := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			title := r.Title
			if r.Kind == kindComment {
				title = excerpt(r.BodyHTML)
			}
			items = append(items, map[string]any{"kind": r.Kind, "id": r.ID, "item_no": r.ItemNo, "title": title, "by": r.By, "flags": r.Flags, "dead": r.Dead, "cause": r.Cause})
		}
		return writeJSON(200, map[string]any{"items": items})
	case "users":
		return list("users", sqlUsers)
	case "domains":
		return list("domains", sqlDomains)
	case "nets":
		var out map[string]any
		if err := core.QueryJSON(sqlNets, &out, nowArg()); err != nil {
			return failed()
		}
		return writeJSON(200, out)
	}
	return core.JSONError(400, "unknown view")
}

func handleBan(q *core.Request) sdk.HTTPResponse {
	var in struct{ Mode, Reason string }
	if resp, good := decodeJSON(q, &in); !good {
		return resp
	}
	reason, good := cleanReason(in.Reason)
	if !good || (in.Mode != "dead" && in.Mode != "blocked") {
		return core.JSONError(400, "invalid mode or reason")
	}
	u, err := core.UserByName(q.PathValue("name"))
	if err != nil {
		return failed()
	}
	if u == nil {
		return core.JSONError(404, "unknown member")
	}
	if _, err := core.Exec(sqlBanUpsert, u.ID, in.Mode, reason, nowArg()); err != nil {
		return failed()
	}
	writeLog("owner", "ban", "user:"+u.Username, in.Mode)
	return ok(nil)
}

func handleUnban(q *core.Request) sdk.HTTPResponse {
	var in struct{}
	if resp, good := decodeJSON(q, &in); !good {
		return resp
	}
	u, err := core.UserByName(q.PathValue("name"))
	if err != nil {
		return failed()
	}
	if u == nil {
		return core.JSONError(404, "unknown member")
	}
	if _, err := core.Exec(sqlBanDelete, u.ID); err != nil {
		return failed()
	}
	writeLog("owner", "unban", "user:"+u.Username, "")
	return ok(nil)
}

// cleanDomain lower-cases a domain and drops "www.".
func cleanDomain(s string) (string, bool) {
	d := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "www.")
	return d, len(d) <= 253 && domainRe.MatchString(d)
}

func handleDomainBan(q *core.Request) sdk.HTTPResponse {
	var in struct{ Domain, Reason string }
	if resp, good := decodeJSON(q, &in); !good {
		return resp
	}
	d, good := cleanDomain(in.Domain)
	reason, good2 := cleanReason(in.Reason)
	if !good || !good2 {
		return core.JSONError(400, "invalid domain or reason")
	}
	if _, err := core.Exec(sqlDomainUpsert, d, reason, nowArg()); err != nil {
		return failed()
	}
	writeLog("owner", "domain-ban", "domain:"+d, "")
	return ok(map[string]any{"domain": d})
}

func handleDomainRemove(q *core.Request) sdk.HTTPResponse {
	var in struct{ Domain string }
	if resp, good := decodeJSON(q, &in); !good {
		return resp
	}
	d, good := cleanDomain(in.Domain)
	if !good {
		return core.JSONError(400, "invalid domain")
	}
	if _, err := core.Exec(sqlDomainDelete, d); err != nil {
		return failed()
	}
	writeLog("owner", "domain-remove", "domain:"+d, "")
	return ok(nil)
}

func handleNetBan(q *core.Request) sdk.HTTPResponse {
	var in struct {
		NetKey string `json:"net_key"`
		Reason string
		Days   int
	}
	if resp, good := decodeJSON(q, &in); !good {
		return resp
	}
	reason, good := cleanReason(in.Reason)
	if !good || !netKeyRe.MatchString(in.NetKey) || in.Days < 0 || in.Days > 365 {
		return core.JSONError(400, "invalid network key, days or reason")
	}
	var expires any
	if in.Days > 0 {
		expires = core.Now().UTC().AddDate(0, 0, in.Days).Format(time.RFC3339Nano)
	}
	if _, err := core.Exec(sqlNetUpsert, in.NetKey, reason, expires, nowArg()); err != nil {
		return failed()
	}
	writeLog("owner", "net-ban", "net:"+in.NetKey[:8], "")
	return ok(nil)
}

func handleNetUnban(q *core.Request) sdk.HTTPResponse {
	var in struct {
		NetKey string `json:"net_key"`
	}
	if resp, good := decodeJSON(q, &in); !good {
		return resp
	}
	if !netKeyRe.MatchString(in.NetKey) {
		return core.JSONError(400, "invalid network key")
	}
	if _, err := core.Exec(sqlNetDelete, in.NetKey); err != nil {
		return failed()
	}
	writeLog("owner", "net-unban", "net:"+in.NetKey[:8], "")
	return ok(nil)
}

// handleItem kills (owner cause, which no vouch can undo) or restores a story
// or comment.
func handleItem(q *core.Request, dying bool) sdk.HTTPResponse {
	var in struct{ Kind, ID string }
	if resp, good := decodeJSON(q, &in); !good {
		return resp
	}
	t, err := resolveByID(in.Kind, in.ID)
	if err != nil {
		return failed()
	}
	if t == nil {
		return core.JSONError(404, core.NoticeText(string(nMissing)))
	}
	var n core.Notice
	if dying {
		_, n = kill(t, causeOwner, "owner")
	} else {
		n = restore(t, "owner")
	}
	switch n {
	case "":
		return ok(map[string]any{"dead": dying})
	case nOff:
		return core.JSONError(503, core.NoticeText(string(n)))
	}
	return failed()
}
