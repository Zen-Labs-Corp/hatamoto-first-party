package main

import "strconv"

// brevo upserts contacts with the v3 API.
type brevo struct{}

const brevoBase = "https://api.brevo.com/v3"

func (brevo) headers(c cfg) map[string]string { return map[string]string{"api-key": c.APIKey} }

func brevoList(c cfg) (int, bool) {
	n, err := strconv.Atoi(c.ListID)
	return n, err == nil && n > 0
}

func (b brevo) upsert(j job, c cfg, f fetcher) result {
	list, ok := brevoList(c)
	if !ok {
		return result{kind: kindFail, reason: "invalid list"}
	}
	attrs := map[string]string{}
	if j.FirstName != "" {
		attrs["FIRSTNAME"] = j.FirstName
	}
	if j.LastName != "" {
		attrs["LASTNAME"] = j.LastName
	}
	body := map[string]any{"email": j.Email, "listIds": []int{list}, "updateEnabled": true}
	if len(attrs) > 0 {
		body["attributes"] = attrs
	}
	resp := f(jsonReq("POST", brevoBase+"/contacts", b.headers(c), body))
	if r, bad := failure(resp); bad {
		return r
	}
	// 201 created the contact, 204 updated an existing one.
	if resp.Status == 204 {
		return result{kind: kindUpdated, status: resp.Status}
	}
	return result{kind: kindSent, status: resp.Status}
}

func (b brevo) test(c cfg, f fetcher) (string, result) {
	list, ok := brevoList(c)
	if !ok {
		return "", result{kind: kindFail, reason: "invalid list"}
	}
	resp := f(jsonReq("GET", brevoBase+"/contacts/lists/"+strconv.Itoa(list), b.headers(c), nil))
	if r, bad := failure(resp); bad {
		return "", r
	}
	name := listName(resp.Body)
	if name == "" {
		return "", errNoList
	}
	return name, result{kind: kindSent, status: resp.Status}
}
