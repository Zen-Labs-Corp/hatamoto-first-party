package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"
)

// The data behind the Submissions and Imports tabs of the admin page. Owners
// and editors read the submissions (the submitter's email is shown to them and
// goes nowhere else); only the owner deletes one or sees a source's address.

var submissionStatuses = []string{"pending", "approved", "published", "rejected"}

// submissionItem is one row of the Submissions tab.
type submissionItem struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Start       string `json:"start_at"`
	End         string `json:"end_at"`
	Venue       string `json:"venue"`
	Description string `json:"description"`
	Email       string `json:"email"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type submissionList struct {
	Role    string           `json:"role"`
	Enabled bool             `json:"enabled"` // the public form is on
	Counts  map[string]int   `json:"counts"`
	Items   []submissionItem `json:"items"`
}

func unavailable() sdk.HTTPResponse { return plain(http.StatusServiceUnavailable, "unavailable") }

// serveSubmissions answers GET /admin/submissions?status=pending|approved|published|rejected|all.
func serveSubmissions(u *sdk.HTTPUser, query map[string][]string) sdk.HTTPResponse {
	if u == nil {
		return plain(http.StatusForbidden, "forbidden")
	}
	status := "pending"
	if v := queryValue(query["status"]); v != "" {
		status = v
	}
	if status != "all" && !slices.Contains(submissionStatuses, status) {
		return plain(http.StatusBadRequest, "bad status")
	}
	rows, err := listSubmissions(status)
	if err != nil {
		return unavailable()
	}
	counts, err := submissionCounts()
	if err != nil {
		return unavailable()
	}
	for _, s := range submissionStatuses {
		counts[s] += 0
	}
	out := submissionList{Role: u.Role, Enabled: loadSettings().SubmissionsEnabled, Counts: counts, Items: make([]submissionItem, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, submissionItem{
			ID: r.ID, Title: r.Title, Start: r.Start, End: r.End, Venue: r.Venue,
			Description: r.Description, Email: r.Email, Status: r.Status, CreatedAt: r.CreatedAt,
		})
	}
	return jsonReply(http.StatusOK, out)
}

// queryValue is the first value of a query parameter. Core's bridge turns a
// non-string value the page sends into its JSON text, so an array arrives as
// `["pending"]`; its first element is taken.
func queryValue(v []string) string {
	s := first(v)
	if strings.HasPrefix(s, "[") {
		var list []string
		if json.Unmarshal([]byte(s), &list) == nil {
			return first(list)
		}
	}
	return s
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0])
}

// serveSubmissionAction answers POST /admin/submissions/{id}/reject and
// /delete. Rejecting is open to editors, deleting is the owner's.
func serveSubmissionAction(u *sdk.HTTPUser, rawID string, del bool) sdk.HTTPResponse {
	if u == nil || del && u.Role != "owner" {
		return jsonReply(http.StatusForbidden, submitAnswer{Error: "not allowed"})
	}
	if !submissionIDRe.MatchString(rawID) {
		return jsonReply(http.StatusBadRequest, submitAnswer{Error: "bad id"})
	}
	id, _ := strconv.ParseInt(rawID, 10, 64)
	var changed bool
	if del {
		var err error
		if changed, err = deleteSubmission(id); err != nil {
			return jsonReply(http.StatusServiceUnavailable, submitAnswer{Error: "try again"})
		}
	} else {
		changed = setSubmissionStatus(id, "rejected", "pending", "approved")
	}
	if !changed {
		return jsonReply(http.StatusNotFound, submitAnswer{Error: "not found"})
	}
	return submitOK()
}

// serveRejectAll answers POST /admin/submissions/reject-all: every pending
// submission becomes rejected. It is the owner's, since one click clears the
// queue.
func serveRejectAll(u *sdk.HTTPUser) sdk.HTTPResponse {
	if u == nil || u.Role != "owner" {
		return jsonReply(http.StatusForbidden, submitAnswer{Error: "not allowed"})
	}
	n, err := rejectAllPending()
	if err != nil {
		return jsonReply(http.StatusServiceUnavailable, submitAnswer{Error: "try again"})
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "rejected": n})
}

// importRow is one source of the Imports tab. URL is set for the owner only:
// the Run now button needs it for the run form, and a feed address can be a
// secret.
type importRow struct {
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Host         string  `json:"host"`
	Category     string  `json:"category"`
	Enabled      bool    `json:"enabled"`
	LastImported *string `json:"last_imported"`
	URL          string  `json:"url,omitempty"`
	Schedule     string  `json:"schedule"`      // "off" or "daily"
	ScheduledOK  bool    `json:"scheduled_ok"`  // a scheduled run can read the source
	LastNew      *string `json:"last_new"`      // when the index first saw the newest event of the source
	CreatedToday int     `json:"created_today"` // events first seen since midnight UTC
}

type importList struct {
	Role     string      `json:"role"`
	DailyCap int         `json:"daily_cap"` // creates a day: two scheduled runs
	RunCap   int         `json:"run_cap"`   // creates a scheduled run
	Sources  []importRow `json:"sources"`
}

// sourceStatsSQL is one document per source: the latest update, the newest
// first-seen time and the events first seen today (UTC). The key prefix and
// the kind are arguments.
const sourceStatsSQL = `SELECT jsonb_build_object('last', max(updated_at), 'last_new', max(first_seen),
 'today', count(*) FILTER (WHERE first_seen >= (date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')))
 FROM events WHERE source_kind = $2 AND left(source_uid, length($1::text)) = $1::text`

// serveImports answers GET /admin/imports: the import_sources setting, the
// disabled ones included, each with its schedule and the events it brought.
// The Eventbrite token is read only to tell whether it is set.
func serveImports(u *sdk.HTTPUser) sdk.HTTPResponse {
	if u == nil {
		return plain(http.StatusForbidden, "forbidden")
	}
	var raw struct {
		Token   string           `json:"eventbrite_token"`
		Sources []settingsSource `json:"import_sources"`
	}
	_ = callHost(sdk.FnSettingsGet, struct{}{}, &raw)
	hasToken := strings.TrimSpace(raw.Token) != ""
	out := importList{Role: u.Role, DailyCap: 2 * scheduledCreateCap, RunCap: scheduledCreateCap, Sources: []importRow{}}
	for _, r := range raw.Sources {
		if len(out.Sources) >= maxSources {
			break
		}
		src := r.source()
		row := importRow{Name: src.Name, Kind: r.Kind, Host: hostOf(src.URL), Category: src.Category, Enabled: r.enabled(), Schedule: "off"}
		if r.Schedule == "daily" {
			row.Schedule = "daily"
		}
		row.ScheduledOK = hostListed(src.URL) || (r.Kind == "eventbrite" && src.OrganizationID != "" && hasToken)
		if u.Role == "owner" {
			row.URL = src.URL
		}
		var stats struct {
			Last  *string `json:"last"`
			New   *string `json:"last_new"`
			Today int     `json:"today"`
		}
		if err := dbJSON(sourceStatsSQL, &stats, importPrefix(r.Kind, src.Name), r.Kind); err == nil {
			row.LastImported, row.LastNew, row.CreatedToday = stats.Last, stats.New, stats.Today
		}
		out.Sources = append(out.Sources, row)
	}
	return jsonReply(http.StatusOK, out)
}
