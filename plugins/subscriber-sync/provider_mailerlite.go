package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// mailerlite upserts subscribers (the API updates an existing email) and puts
// them in one group.
type mailerlite struct{}

const mailerliteBase = "https://connect.mailerlite.com/api"

func (mailerlite) headers(c cfg) map[string]string {
	return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(c.APIKey)}
}

func (m mailerlite) upsert(j job, c cfg, f fetcher) result {
	body := map[string]any{"email": j.Email, "groups": []string{strings.TrimSpace(c.ListID)}}
	fields := map[string]string{}
	if j.FirstName != "" {
		fields["name"] = j.FirstName
	}
	if j.LastName != "" {
		fields["last_name"] = j.LastName
	}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	resp := f(jsonReq("POST", mailerliteBase+"/subscribers", m.headers(c), body))
	if r, bad := failure(resp); bad {
		return r
	}
	// 201 created the subscriber, 200 updated an existing one.
	if resp.Status == 200 {
		return result{kind: kindUpdated, status: resp.Status}
	}
	return result{kind: kindSent, status: resp.Status}
}

// test finds the group by id; the API has no single-group read, so it pages
// through the group list.
func (m mailerlite) test(c cfg, f fetcher) (string, result) {
	want := strings.TrimSpace(c.ListID)
	for page := 1; page <= maxPages; page++ {
		resp := f(jsonReq("GET", mailerliteBase+"/groups?limit=100&page="+strconv.Itoa(page), m.headers(c), nil))
		if r, bad := failure(resp); bad {
			return "", r
		}
		var v struct {
			Data []struct {
				ID   json.Number `json:"id"`
				Name string      `json:"name"`
			} `json:"data"`
			Meta struct {
				LastPage int `json:"last_page"`
			} `json:"meta"`
		}
		if json.Unmarshal([]byte(resp.Body), &v) != nil {
			return "", result{kind: kindFail, status: resp.Status, reason: "unexpected answer"}
		}
		for _, g := range v.Data {
			if g.ID.String() == want && g.Name != "" {
				return g.Name, result{kind: kindSent, status: resp.Status}
			}
		}
		if page >= v.Meta.LastPage || len(v.Data) == 0 {
			break
		}
	}
	return "", errNoList
}
