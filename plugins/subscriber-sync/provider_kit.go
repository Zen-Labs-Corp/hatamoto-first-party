package main

import (
	"encoding/json"
	"net/url"
	"strings"
)

// kit upserts subscribers with the v4 API and adds them to one form. Only the
// email and first name are sent: a last name needs a custom field that not
// every account has, and a missing one would reject the whole request.
type kit struct{}

const kitBase = "https://api.kit.com/v4"

func (kit) headers(c cfg) map[string]string {
	return map[string]string{"X-Kit-Api-Key": strings.TrimSpace(c.APIKey)}
}

func (k kit) upsert(j job, c cfg, f fetcher) result {
	h := k.headers(c)
	// A new subscriber is created inactive with double opt-in, so the form's
	// confirmation applies; without it they are active right away.
	state := "active"
	if c.doubleOptIn() {
		state = "inactive"
	}
	sub := map[string]any{"email_address": j.Email, "state": state}
	if j.FirstName != "" {
		sub["first_name"] = j.FirstName
	}
	resp := f(jsonReq("POST", kitBase+"/subscribers", h, sub))
	if r, bad := failure(resp); bad {
		return r
	}
	out := result{kind: kindSent, status: resp.Status}
	if resp.Status == 200 {
		out.kind = kindUpdated
	}
	who := map[string]any{"email_address": j.Email}
	form := kitBase + "/forms/" + url.PathEscape(strings.TrimSpace(c.ListID)) + "/subscribers"
	if r, bad := failure(f(jsonReq("POST", form, h, who))); bad {
		return r
	}
	if names := nonEmpty(c.Tags); len(names) > 0 {
		ids, r := k.tagIDs(names, h, f)
		if r.kind != "" {
			return r
		}
		for _, id := range ids {
			if r, bad := failure(f(jsonReq("POST", kitBase+"/tags/"+url.PathEscape(id)+"/subscribers", h, who))); bad {
				return r
			}
		}
	}
	return out
}

// tagIDs resolves tag names to ids; unknown names are skipped. A non-empty
// result.kind means the lookup failed.
func (kit) tagIDs(names []string, h map[string]string, f fetcher) ([]string, result) {
	var ids []string
	remaining := len(names)
	after := ""
	for page := 0; page < maxPages && remaining > 0; page++ {
		resp := f(jsonReq("GET", kitBase+"/tags"+kitCursor(after), h, nil))
		if r, bad := failure(resp); bad {
			return nil, r
		}
		var v struct {
			Tags []struct {
				ID   json.Number `json:"id"`
				Name string      `json:"name"`
			} `json:"tags"`
			Pagination kitPagination `json:"pagination"`
		}
		if json.Unmarshal([]byte(resp.Body), &v) != nil {
			return nil, result{kind: kindFail, status: resp.Status, reason: "unexpected answer"}
		}
		for _, t := range v.Tags {
			for _, n := range names {
				if strings.EqualFold(t.Name, n) {
					ids = append(ids, t.ID.String())
					remaining--
					break
				}
			}
		}
		if !v.Pagination.HasNextPage || v.Pagination.EndCursor == "" {
			break
		}
		after = v.Pagination.EndCursor
	}
	return ids, result{}
}

type kitPagination struct {
	HasNextPage bool   `json:"has_next_page"`
	EndCursor   string `json:"end_cursor"`
}

func kitCursor(after string) string {
	if after == "" {
		return ""
	}
	return "?after=" + url.QueryEscape(after)
}

// test finds the form by id in the form list.
func (k kit) test(c cfg, f fetcher) (string, result) {
	want := strings.TrimSpace(c.ListID)
	after := ""
	for page := 0; page < maxPages; page++ {
		resp := f(jsonReq("GET", kitBase+"/forms"+kitCursor(after), k.headers(c), nil))
		if r, bad := failure(resp); bad {
			return "", r
		}
		var v struct {
			Forms []struct {
				ID   json.Number `json:"id"`
				Name string      `json:"name"`
			} `json:"forms"`
			Pagination kitPagination `json:"pagination"`
		}
		if json.Unmarshal([]byte(resp.Body), &v) != nil {
			return "", result{kind: kindFail, status: resp.Status, reason: "unexpected answer"}
		}
		for _, fm := range v.Forms {
			if fm.ID.String() == want && fm.Name != "" {
				return fm.Name, result{kind: kindSent, status: resp.Status}
			}
		}
		if !v.Pagination.HasNextPage || v.Pagination.EndCursor == "" {
			break
		}
		after = v.Pagination.EndCursor
	}
	return "", errNoList
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
