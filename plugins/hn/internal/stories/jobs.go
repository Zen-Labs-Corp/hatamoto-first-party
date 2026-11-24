package stories

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// jobIn is the JSON body of POST /admin/jobs.
type jobIn struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
	By    string `json:"by"`
}

// handleJob is POST /admin/jobs, an owner-only route (the host enforces the
// role): it posts a job story as member "by", else as the lowest-id member.
// Jobs skip the duplicate check, the hourly limit and the submit gates.
func handleJob(r *core.Registry, q *core.Request) (sdk.HTTPResponse, error) {
	if q.User == nil {
		return core.JSONError(403, "owner only"), nil
	}
	if len(q.Body) > maxBody {
		return core.JSONError(413, "too large"), nil
	}
	var in jobIn
	dec := json.NewDecoder(strings.NewReader(q.Body))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		return core.JSONError(400, "invalid json"), nil
	}
	title, err := core.CleanTitle(in.Title)
	if err != nil {
		return core.JSONError(400, err.Error()), nil
	}
	rawURL, text := strings.TrimSpace(in.URL), strings.TrimSpace(in.Text)
	if rawURL == "" && text == "" {
		return core.JSONError(400, "url or text required"), nil
	}
	if !textOK(text) {
		return core.JSONError(400, core.NoticeText(string(nTextLong))), nil
	}
	var canon, key, domain string
	if rawURL != "" {
		if canon, key, domain, err = core.NormalizeURL(rawURL); err != nil {
			return core.JSONError(400, err.Error()), nil
		}
	}
	var author int64
	if by := strings.TrimSpace(in.By); by != "" {
		m, err := core.UserByName(by)
		if err != nil {
			return core.JSONError(500, core.NoticeText(string(nError))), nil
		}
		if m == nil {
			return core.JSONError(400, "unknown member"), nil
		}
		author = m.ID
	} else if err := core.QueryJSON("SELECT to_jsonb(id) FROM users ORDER BY id LIMIT 1", &author); err != nil {
		if errors.Is(err, core.ErrNoRows) {
			return core.JSONError(409, "no member to post as"), nil
		}
		return core.JSONError(500, core.NoticeText(string(nError))), nil
	}
	no, n := createStory(r, newStory{AuthorID: author, Title: title, URL: canon, URLKey: key, Domain: siteOf(domain), Text: text, Kind: "job"})
	switch n {
	case "":
	case nOff:
		return core.JSONError(503, core.NoticeText(string(n))), nil
	case nBusy:
		return core.JSONError(429, core.NoticeText(string(n))), nil
	default:
		return core.JSONError(500, core.NoticeText(string(n))), nil
	}
	body, _ := json.Marshal(map[string]any{"item_no": no, "path": "/item/" + strconv.FormatInt(no, 10)})
	return sdk.HTTPResponse{Status: 200, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}, Body: string(body)}, nil
}
