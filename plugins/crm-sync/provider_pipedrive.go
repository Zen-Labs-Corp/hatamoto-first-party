package main

import (
	"encoding/json"
	"html"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

type pipedrive struct{}

func init() { providers["pipedrive"] = pipedrive{} }

const (
	pdMaxFieldPages = 3
	pdFieldsPage    = 500
)

func pdBase(label string) string { return "https://" + label + ".pipedrive.com" }

func pdHeaders(c cfg) map[string]string { return map[string]string{"x-api-token": c.apiToken()} }

// pdID reads an id that may arrive as a number or as a string.
func pdID(raw json.RawMessage) string {
	var n json.Number
	if json.Unmarshal(raw, &n) == nil && n != "" {
		return n.String()
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// pdSearchID is the id of the first search hit, or "". Hits are documented as
// data.items[].item; a bare data.items[].id is accepted too.
func pdSearchID(body string) string {
	var out struct {
		Data struct {
			Items []struct {
				ID   json.RawMessage `json:"id"`
				Item struct {
					ID json.RawMessage `json:"id"`
				} `json:"item"`
			} `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(body), &out) != nil || len(out.Data.Items) == 0 {
		return ""
	}
	if id := pdID(out.Data.Items[0].Item.ID); id != "" {
		return id
	}
	return pdID(out.Data.Items[0].ID)
}

// pdDataID is data.id of a create or update answer.
func pdDataID(body string) string {
	var out struct {
		Data struct {
			ID json.RawMessage `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(body), &out) != nil {
		return ""
	}
	return pdID(out.Data.ID)
}

func pdOK(r fetchResp) bool { return r.Code == "" && r.Status >= 200 && r.Status <= 299 }

func pdWork(v string) []any {
	return []any{map[string]any{"value": v, "primary": true, "label": "work"}}
}

// noteHTML turns the plain-text note into the HTML Pipedrive expects.
func noteHTML(text string) string {
	return strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
}

// pdSearch finds a record by exact term; "" when there is no hit.
func pdSearch(c cfg, base, kind, field, term string, f fetcher) (string, result) {
	q := url.Values{"term": {term}, "fields": {field}, "exact_match": {"true"}, "limit": {"1"}}
	resp := f(jsonReq("GET", base+"/api/v2/"+kind+"/search?"+q.Encode(), pdHeaders(c), nil))
	if r, bad := failure(resp); bad {
		return "", r
	}
	return pdSearchID(resp.Body), result{}
}

// pdPersonBody builds the person fields; skip names fields to leave out.
func pdPersonBody(props map[string]string, email, orgID, owner string, create bool, skip map[string]bool) map[string]any {
	body := map[string]any{}
	custom := map[string]any{}
	for k, v := range props {
		switch k {
		case "first_name", "last_name", "name", "org_name":
		case "phones":
			if !skip[k] {
				body["phones"] = pdWork(v)
			}
		default:
			if !skip[k] {
				custom[k] = v
			}
		}
	}
	if len(custom) > 0 {
		body["custom_fields"] = custom
	}
	first, last := props["first_name"], props["last_name"]
	switch {
	case first != "" && last != "":
		body["first_name"], body["last_name"] = first, last
	case !create:
	case props["name"] != "":
		body["name"] = props["name"]
	case first != "" || last != "":
		body["name"] = first + last
	default:
		body["name"] = email
	}
	if create {
		body["emails"] = pdWork(email)
		if n, err := strconv.ParseInt(owner, 10, 64); err == nil && !skip["owner_id"] {
			body["owner_id"] = n
		}
	}
	if n, err := strconv.ParseInt(orgID, 10, 64); err == nil && !skip["org_id"] {
		body["org_id"] = n
	}
	return body
}

// pdDisplayName is the person's name as the lead title shows it.
func pdDisplayName(props map[string]string, email string) string {
	first, last := props["first_name"], props["last_name"]
	switch {
	case first != "" && last != "":
		return first + " " + last
	case props["name"] != "":
		return props["name"]
	case first != "" || last != "":
		return first + last
	}
	return email
}

func (pipedrive) upsert(j job, c cfg, m []mapRow, f fetcher) result {
	label := c.companyLabel()
	if label == "" {
		return result{kind: kindFail, reason: "bad company domain"}
	}
	base := pdBase(label)
	h := pdHeaders(c)
	props := applyMapping(j.Fields, m)
	delete(props, "email")

	personID, r := pdSearch(c, base, "persons", "email", j.Email, f)
	if r.kind != "" {
		return r
	}
	orgID := ""
	if name := props["org_name"]; name != "" {
		if orgID, r = pdSearch(c, base, "organizations", "name", name, f); r.kind != "" {
			return r
		}
		if orgID == "" {
			resp := f(jsonReq("POST", base+"/api/v2/organizations", h, map[string]any{"name": name}))
			if r, bad := failure(resp); bad {
				return r
			}
			if orgID = pdDataID(resp.Body); orgID == "" {
				return result{kind: kindFail, status: resp.Status, reason: "bad answer"}
			}
		}
	}

	create := personID == ""
	var sent []string
	first := pdPersonBody(props, j.Email, orgID, c.owner(), create, nil)
	for _, k := range []string{"phones", "org_id", "owner_id"} {
		if _, ok := first[k]; ok {
			sent = append(sent, k)
		}
	}
	if cf, ok := first["custom_fields"].(map[string]any); ok {
		for k := range cf {
			sent = append(sent, k)
		}
	}
	slices.Sort(sent)
	build := func(skip map[string]bool) fetchReq {
		body := pdPersonBody(props, j.Email, orgID, c.owner(), create, skip)
		if create {
			return jsonReq("POST", base+"/api/v2/persons", h, body)
		}
		return jsonReq("PATCH", base+"/api/v2/persons/"+url.PathEscape(personID), h, body)
	}
	resp, dropped := retryWithoutRejected(build, func(r fetchResp) []string { return pipedriveNames(r, sent) }, sent, f)
	if r, bad := failure(resp); bad {
		r.dropped = dropped
		return r
	}
	if id := pdDataID(resp.Body); id != "" {
		personID = id
	}
	if personID == "" {
		return result{kind: kindFail, status: resp.Status, reason: "bad answer"}
	}
	out := result{kind: kindUpdated, status: resp.Status, recordID: personID, dropped: dropped,
		recordURL: base + "/person/" + url.PathEscape(personID)}
	if create {
		out.kind = kindCreated
	}
	personNum, _ := strconv.ParseInt(personID, 10, 64)

	leadID := ""
	if c.createLead() {
		var r result
		if leadID, r = pdLead(j, c, base, personNum, orgID, pdDisplayName(props, j.Email), f); r.kind != "" {
			return r
		}
		if leadID != "" {
			out.recordURL = base + "/leads/inbox/" + url.PathEscape(leadID)
		}
	}

	var notes []string
	if c.addNote() {
		body := map[string]any{"content": noteHTML(noteText(j, c.leadSource())), "person_id": personNum}
		if leadID != "" {
			body["lead_id"] = leadID
		}
		if nr := f(jsonReq("POST", base+"/api/v1/notes", h, body)); !pdOK(nr) {
			notes = append(notes, "note failed "+codeWord(nr))
		}
	}
	if m := rejectedMessage(dropped); m != "" {
		notes = append(notes, m)
	}
	out.message = strings.Join(notes, "; ")
	return out
}

// pdLead reuses the person's first lead or creates one; it returns the id.
func pdLead(j job, c cfg, base string, personID int64, orgID, name string, f fetcher) (string, result) {
	h := pdHeaders(c)
	q := url.Values{"person_id": {strconv.FormatInt(personID, 10)}, "limit": {"1"}}
	resp := f(jsonReq("GET", base+"/api/v1/leads?"+q.Encode(), h, nil))
	if r, bad := failure(resp); bad {
		return "", r
	}
	var list struct {
		Data []struct {
			ID json.RawMessage `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(resp.Body), &list) == nil && len(list.Data) > 0 {
		if id := pdID(list.Data[0].ID); id != "" {
			return id, result{}
		}
	}
	body := map[string]any{"title": name + " (website form " + j.FormSlug + ")", "person_id": personID}
	if n, err := strconv.ParseInt(orgID, 10, 64); err == nil {
		body["organization_id"] = n
	}
	if n, err := strconv.ParseInt(c.owner(), 10, 64); err == nil {
		body["owner_id"] = n
	}
	resp = f(jsonReq("POST", base+"/api/v1/leads", h, body))
	if r, bad := failure(resp); bad {
		return "", r
	}
	id := pdDataID(resp.Body)
	if id == "" {
		return "", result{kind: kindFail, status: resp.Status, reason: "bad answer"}
	}
	return id, result{}
}

// pipedriveNames lists the sent fields whose key appears in a 400 or 422
// answer. The answer text itself is never kept.
func pipedriveNames(resp fetchResp, sent []string) []string {
	if resp.Status != 400 && resp.Status != 422 {
		return nil
	}
	var names []string
	for _, k := range sent {
		if k != "" && strings.Contains(resp.Body, k) {
			names = append(names, k)
		}
	}
	return names
}

func (pipedrive) test(c cfg, f fetcher) (string, result) {
	label := c.companyLabel()
	if label == "" {
		return "", result{kind: kindFail, reason: "bad company domain"}
	}
	resp := f(jsonReq("GET", pdBase(label)+"/api/v1/users/me", pdHeaders(c), nil))
	if r, bad := failure(resp); bad {
		return "", r
	}
	var out struct {
		Data struct {
			CompanyName string `json:"company_name"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(resp.Body), &out)
	name := strings.TrimSpace(out.Data.CompanyName)
	if name == "" {
		name = label
	}
	return name, result{kind: kindUpdated, status: resp.Status}
}

func (pipedrive) fields(c cfg, f fetcher) ([]crmField, result) {
	label := c.companyLabel()
	if label == "" {
		return nil, result{kind: kindFail, reason: "bad company domain"}
	}
	out := []crmField{
		{Name: "first_name", Label: "First name"}, {Name: "last_name", Label: "Last name"},
		{Name: "name", Label: "Name"}, {Name: "phones", Label: "Phone"},
	}
	cursor := ""
	for range pdMaxFieldPages {
		q := url.Values{"limit": {strconv.Itoa(pdFieldsPage)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		resp := f(jsonReq("GET", pdBase(label)+"/api/v2/personFields?"+q.Encode(), pdHeaders(c), nil))
		if r, bad := failure(resp); bad {
			return nil, r
		}
		var page struct {
			Data []struct {
				Code   string `json:"field_code"`
				Name   string `json:"field_name"`
				Custom bool   `json:"is_custom_field"`
			} `json:"data"`
			More struct {
				Next *string `json:"next_cursor"`
			} `json:"additional_data"`
		}
		if json.Unmarshal([]byte(resp.Body), &page) != nil {
			return nil, result{kind: kindFail, status: resp.Status, reason: "bad answer"}
		}
		for _, d := range page.Data {
			if d.Custom && d.Code != "" && !slices.ContainsFunc(out, func(x crmField) bool { return x.Name == d.Code }) {
				out = append(out, crmField{Name: d.Code, Label: orDefault(d.Name, d.Code)})
			}
		}
		if page.More.Next == nil || *page.More.Next == "" {
			break
		}
		cursor = *page.More.Next
	}
	return append(out, crmField{Name: "org_name", Label: "Organization name"}), result{kind: kindUpdated}
}

func (pipedrive) refresh(cfg, fetcher) result { return result{kind: kindUpdated} }
