package stories

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"strconv"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// newStory is a validated submission ready to be stored.
type newStory struct {
	AuthorID                               int64
	Title, URL, URLKey, Domain, Text, Kind string
}

// entityOut is the part of an entities.* answer this phase reads.
type entityOut struct {
	ID string `json:"id"`
}

// createStory reserves an item number, creates the published hn-story entity,
// inserts story_meta and publishes StorySaved. It returns the item number, or
// a notice for the member (nOff, nBusy, nError) with no row left behind.
func createStory(r *core.Registry, s newStory) (int64, core.Notice) {
	var no int64
	if err := core.QueryJSON("INSERT INTO item_numbers DEFAULT VALUES RETURNING to_jsonb(item_no)", &no); err != nil || no <= 0 {
		return 0, nError
	}
	attrs := map[string]string{"kind": s.Kind}
	if s.URL != "" { // the schema url is format: uri, so omit it when empty
		attrs["url"] = s.URL
	}
	if s.Text != "" {
		attrs["text"] = s.Text
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		dropNumber(no)
		return 0, nError
	}
	var out entityOut
	err = core.Call(sdk.FnEntitiesCreate, sdk.EntityWrite{Name: s.Title, Slug: strconv.FormatInt(no, 10), Attributes: jsontext.Value(raw), Status: "published"}, &out)
	if err != nil || out.ID == "" {
		dropNumber(no)
		return 0, hostNotice(err)
	}
	_, err = core.Exec(`INSERT INTO story_meta (entity_id, item_no, author_id, title, url, url_key, domain, body, kind, created_at)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10::timestamptz)`,
		out.ID, no, s.AuthorID, s.Title, s.URL, s.URLKey, s.Domain, s.Text, s.Kind, nowArg())
	if err != nil {
		_ = core.Call(sdk.FnEntitiesDelete, sdk.EntityDeleteArgs{ID: out.ID}, nil)
		dropNumber(no)
		return 0, nError
	}
	r.Publish(core.StoryEvent{Kind: core.StorySaved, EntityID: out.ID, ItemNo: no, Title: s.Title, Body: s.Text})
	return no, ""
}

// dropNumber gives a reserved item number back.
func dropNumber(no int64) {
	_, _ = core.Exec("DELETE FROM item_numbers WHERE item_no = $1", no)
}

// hostNotice maps a failed entities.create to a notice: unavailable is "off",
// rate limited is "busy", anything else (including no error with an empty id) is a plain error.
func hostNotice(err error) core.Notice {
	var he *sdk.HostError
	if errors.As(err, &he) {
		switch he.Code {
		case sdk.ErrUnavailable:
			return nOff
		case sdk.ErrRateLimited:
			return nBusy
		}
	}
	return nError
}
