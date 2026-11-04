package main

import (
	"encoding/json"
	"strings"
	"time"
)

// Every statement below is a single statement (the host runs one per call) and
// every write is an upsert or a bounded delete, so a repeated run changes
// nothing.

const (
	kindInternal = "internal"
	kindOutbound = "outbound"

	statusPending    = "pending"
	statusOK         = "ok"
	statusBroken     = "broken"
	statusRedirected = "redirected"
	statusUnknown    = "unknown"
	statusIgnored    = "ignored"

	// retention is how long a link nobody links to any more is kept.
	retention = 30 * 24 * time.Hour
	chunk     = 500
)

// linkRef is one link found in content: URL is the normalised address, Kind is
// kindInternal (a site path) or kindOutbound (an absolute http(s) address).
type linkRef struct {
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

// pageRef is the entity a link was found on.
type pageRef struct {
	ID   string
	Name string
	Path string
}

// dueLink is a link waiting for a check.
type dueLink struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	Kind      string `json:"kind"`
	FailCount int    `json:"fail_count"`
}

type pageOn struct {
	EntityID   string `json:"entity_id"`
	EntityName string `json:"entity_name"`
	EntityPath string `json:"entity_path"`
}

// linkRow is a link as the admin page shows it.
type linkRow struct {
	ID          int64    `json:"id"`
	URL         string   `json:"url"`
	Kind        string   `json:"kind"`
	Status      string   `json:"status"`
	HTTPStatus  *int     `json:"http_status"`
	Error       *string  `json:"error"`
	LastChecked *string  `json:"last_checked"`
	FirstBroken *string  `json:"first_broken"`
	Pages       []pageOn `json:"pages"`
}

// counts is the number of links per status and the number of pages with links.
type counts struct {
	Broken     int `json:"broken"`
	Redirected int `json:"redirected"`
	OK         int `json:"ok"`
	Ignored    int `json:"ignored"`
	Unknown    int `json:"unknown"`
	Pending    int `json:"pending"`
	Pages      int `json:"pages"`
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// replacePageLinks makes refs the complete set of links of page e: new links
// are queued (pending, due now), links already known keep their result (an
// ignored link stays ignored), and the page's rows for other links are removed.
func replacePageLinks(e pageRef, refs []linkRef) error {
	for i := 0; i < len(refs); i += chunk {
		b, _ := json.Marshal(refs[i:min(i+chunk, len(refs))])
		if err := dbExec(`INSERT INTO links (url, kind)
			SELECT x.url, x.kind FROM jsonb_to_recordset($1::jsonb) AS x(url text, kind text)
			ON CONFLICT (url) DO UPDATE SET last_seen = now()`, string(b)); err != nil {
			return err
		}
		if err := dbExec(`INSERT INTO link_pages (link_id, entity_id, entity_name, entity_path)
			SELECT l.id, $2::text, $3::text, $4::text FROM links l
			  JOIN jsonb_to_recordset($1::jsonb) AS x(url text) ON l.url = x.url
			ON CONFLICT (link_id, entity_id) DO UPDATE
			  SET entity_name = EXCLUDED.entity_name, entity_path = EXCLUDED.entity_path, last_seen = now()`,
			string(b), e.ID, e.Name, e.Path); err != nil {
			return err
		}
	}
	all, _ := json.Marshal(refs)
	if refs == nil {
		all = []byte("[]")
	}
	return dbExec(`DELETE FROM link_pages
		WHERE entity_id = $1
		  AND link_id NOT IN (SELECT l.id FROM links l
		    JOIN jsonb_to_recordset($2::jsonb) AS x(url text) ON l.url = x.url)`, e.ID, string(all))
}

// dueLinks returns up to limit links of kind whose next check is due.
// Ignored links are never due. The oldest due link of every host comes first,
// then the second oldest of every host, and so on, so a host with a long
// backlog cannot crowd the others out of the batch.
func dueLinks(kind string, limit int) ([]dueLink, error) {
	var out []dueLink
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.rn, r.next_check, r.id), '[]'::jsonb) FROM
		(SELECT t.id, t.url, t.kind, t.fail_count, t.next_check, t.rn FROM
		  (SELECT id, url, kind, fail_count, next_check,
		     row_number() OVER (PARTITION BY lower(substring(url from '^https?://([^/?#]+)'))
		       ORDER BY next_check, id) AS rn
		   FROM links
		   WHERE kind = $1 AND status <> 'ignored' AND next_check <= $2::timestamptz) t
		 ORDER BY t.rn, t.next_check, t.id LIMIT $3) r`, &out, kind, ts(now()), limit)
	return out, err
}

// markResult stores the outcome of a check. first_broken is set when a link
// enters broken and kept while it stays broken. An ignored link is left alone.
func markResult(id int64, status string, httpStatus int, errText string, failCount int, next time.Time) error {
	return dbExec(`UPDATE links SET status = $2::text, http_status = nullif($3::integer, 0),
		  error = nullif($4::text, ''), fail_count = $5::integer, last_checked = $6::timestamptz,
		  next_check = $7::timestamptz,
		  first_broken = CASE WHEN $2::text = 'broken' THEN coalesce(first_broken, $6::timestamptz) ELSE NULL END
		WHERE id = $1 AND status <> 'ignored'`,
		id, status, httpStatus, errText, failCount, ts(now()), ts(next))
}

// pathAndParents returns p and every parent path of it, except "/".
func pathAndParents(p string) []string {
	p = strings.TrimRight(p, "/")
	var out []string
	for p != "" {
		out = append(out, p)
		i := strings.LastIndex(p, "/")
		if i <= 0 {
			break
		}
		p = p[:i]
	}
	return out
}

// upsertKnownPaths remembers paths of published pages and their parents.
func upsertKnownPaths(paths []string) error {
	for i := 0; i < len(paths); i += chunk {
		b, _ := json.Marshal(paths[i:min(i+chunk, len(paths))])
		if err := dbExec(`INSERT INTO known_paths (path)
			SELECT jsonb_array_elements_text($1::jsonb)
			ON CONFLICT (path) DO UPDATE SET last_seen = now()`, string(b)); err != nil {
			return err
		}
	}
	return nil
}

// isKnownParent reports whether path is a published page or a parent of one.
func isKnownParent(path string) (bool, error) {
	var out struct {
		Known bool `json:"known"`
	}
	err := dbJSON(`SELECT jsonb_build_object('known', EXISTS (SELECT 1 FROM known_paths WHERE path = $1))`, &out, path)
	return out.Known, err
}

// purge forgets what is no longer on any page: page rows and known paths not
// seen for the retention period, then links with no recent page. It only runs
// when passOK says a completed pass started inside the retention period: only
// then does "not seen" mean "no longer on the site" (a pass that never
// completes, or stopped long ago, would age out rows it never revisited).
func purge(at time.Time, passOK bool) error {
	if !passOK {
		return nil
	}
	cutoff := ts(at.Add(-retention))
	if err := dbExec(`DELETE FROM link_pages WHERE last_seen < $1::timestamptz`, cutoff); err != nil {
		return err
	}
	if err := dbExec(`DELETE FROM links l WHERE l.last_seen < $1::timestamptz
		AND NOT EXISTS (SELECT 1 FROM link_pages p WHERE p.link_id = l.id)`, cutoff); err != nil {
		return err
	}
	return dbExec(`DELETE FROM known_paths WHERE last_seen < $1::timestamptz`, cutoff)
}

const selectRows = `SELECT l.id, l.url, l.kind, l.status, l.http_status, l.error, l.last_checked, l.first_broken,
	  coalesce((SELECT jsonb_agg(jsonb_build_object('entity_id', p.entity_id, 'entity_name', p.entity_name, 'entity_path', p.entity_path)
	    ORDER BY p.entity_name, p.entity_id) FROM link_pages p WHERE p.link_id = l.id), '[]'::jsonb) AS pages
	FROM links l`

// listLinks returns up to limit links of status with id above cursor, in id order.
func listLinks(status string, cursor int64, limit int) ([]linkRow, error) {
	var out []linkRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]'::jsonb) FROM
		(`+selectRows+` WHERE l.status = $1 AND l.id > $2 ORDER BY l.id LIMIT $3) t`, &out, status, cursor, limit)
	return out, err
}

// getLink returns one link, or false when there is none.
func getLink(id int64) (linkRow, bool, error) {
	var out []linkRow
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (`+selectRows+` WHERE l.id = $1) t`, &out, id)
	if err != nil || len(out) == 0 {
		return linkRow{}, false, err
	}
	return out[0], true, nil
}

func countLinks() (counts, error) {
	var c counts
	err := dbJSON(`SELECT jsonb_build_object(
		'broken', count(*) FILTER (WHERE status = 'broken'),
		'redirected', count(*) FILTER (WHERE status = 'redirected'),
		'ok', count(*) FILTER (WHERE status = 'ok'),
		'ignored', count(*) FILTER (WHERE status = 'ignored'),
		'unknown', count(*) FILTER (WHERE status = 'unknown'),
		'pending', count(*) FILTER (WHERE status = 'pending'),
		'pages', (SELECT count(DISTINCT entity_id) FROM link_pages)) FROM links`, &c)
	return c, err
}

// queueSize is the number of links waiting for a check.
func queueSize() (int, error) {
	var out struct {
		N int `json:"n"`
	}
	err := dbJSON(`SELECT jsonb_build_object('n', count(*)) FROM links
		WHERE status <> 'ignored' AND next_check <= $1::timestamptz`, &out, ts(now()))
	return out.N, err
}

// setRecheck makes a link due now. It reports false when the link is unknown.
func setRecheck(id int64) (linkRow, bool, error) {
	if err := dbExec(`UPDATE links SET next_check = $2::timestamptz WHERE id = $1`, id, ts(now())); err != nil {
		return linkRow{}, false, err
	}
	return getLink(id)
}

// toggleIgnore ignores a link, or takes it back into the queue when it is
// ignored. It reports false when the link is unknown.
func toggleIgnore(id int64) (linkRow, bool, error) {
	if err := dbExec(`UPDATE links SET
		  status = CASE WHEN status = 'ignored' THEN 'pending' ELSE 'ignored' END,
		  next_check = CASE WHEN status = 'ignored' THEN $2::timestamptz ELSE next_check END,
		  fail_count = 0
		WHERE id = $1`, id, ts(now())); err != nil {
		return linkRow{}, false, err
	}
	return getLink(id)
}
