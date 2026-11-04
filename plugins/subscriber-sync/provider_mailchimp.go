package main

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

// mailchimp upserts members of an audience with the Marketing API v3.
type mailchimp struct{}

// The API host is built from the key's data centre only when it looks like
// us<N>; anything else is refused before a request is made.
var mailchimpDC = regexp.MustCompile(`^us[0-9]{1,2}$`)

// base returns the API base URL and the Authorization header for a key.
func (mailchimp) base(c cfg) (apiBase string, headers map[string]string, ok bool) {
	key := strings.TrimSpace(c.APIKey)
	i := strings.LastIndex(key, "-")
	if i <= 0 || !mailchimpDC.MatchString(key[i+1:]) {
		return "", nil, false
	}
	auth := base64.StdEncoding.EncodeToString([]byte("hatamoto:" + key))
	return "https://" + key[i+1:] + ".api.mailchimp.com/3.0", map[string]string{"Authorization": "Basic " + auth}, true
}

func (m mailchimp) upsert(j job, c cfg, f fetcher) result {
	apiBase, headers, ok := m.base(c)
	if !ok {
		return result{kind: kindFail, reason: "invalid key"}
	}
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(j.Email))))
	member := apiBase + "/lists/" + url.PathEscape(strings.TrimSpace(c.ListID)) + "/members/" + hex.EncodeToString(sum[:])

	status := "subscribed"
	if c.doubleOptIn() {
		status = "pending"
	}
	merge := map[string]string{}
	if j.FirstName != "" {
		merge["FNAME"] = j.FirstName
	}
	if j.LastName != "" {
		merge["LNAME"] = j.LastName
	}
	body := map[string]any{"email_address": j.Email, "status_if_new": status}
	if len(merge) > 0 {
		body["merge_fields"] = merge
	}
	resp := f(jsonReq("PUT", member, headers, body))
	if r, bad := failure(resp); bad {
		return r
	}
	if len(c.Tags) > 0 {
		tags := make([]map[string]string, 0, len(c.Tags))
		for _, t := range c.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, map[string]string{"name": t, "status": "active"})
			}
		}
		if len(tags) > 0 {
			if r, bad := failure(f(jsonReq("POST", member+"/tags", headers, map[string]any{"tags": tags}))); bad {
				return r
			}
		}
	}
	// The member PUT answers 200 for a new and an existing member alike.
	return result{kind: kindSent, status: resp.Status}
}

func (m mailchimp) test(c cfg, f fetcher) (string, result) {
	apiBase, headers, ok := m.base(c)
	if !ok {
		return "", result{kind: kindFail, reason: "invalid key"}
	}
	resp := f(jsonReq("GET", apiBase+"/lists/"+url.PathEscape(strings.TrimSpace(c.ListID))+"?fields=name", headers, nil))
	if r, bad := failure(resp); bad {
		return "", r
	}
	name := listName(resp.Body)
	if name == "" {
		return "", errNoList
	}
	return name, result{kind: kindSent, status: resp.Status}
}
