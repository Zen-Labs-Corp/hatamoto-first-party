package main

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type hubspot struct{}

func init() { providers["hubspot"] = hubspot{} }

const hsHost = "https://api.hubapi.com"

// hsPortal is what the test and fields routes remember for record links.
type hsPortal struct {
	PortalID int64  `json:"portal_id"`
	UIDomain string `json:"ui_domain"`
}

var (
	hsUIDomain   = regexp.MustCompile(`^[a-z0-9.-]+\.hubspot\.com$`)
	hsBadObject  = regexp.MustCompile(`\{[^{}]*"isValid"\s*:\s*false[^{}]*\}`)
	hsObjectName = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
)

func hsHeaders(c cfg) map[string]string {
	return map[string]string{"Authorization": "Bearer " + c.token()}
}

// codeWord is the HTTP status or the host error code of an answer.
func codeWord(r fetchResp) string {
	if r.Code != "" {
		return r.Code
	}
	return strconv.Itoa(r.Status)
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// hubspotNames reads the property names a 400 or 207 answer complains about:
// the objects embedded in the message text and errors[].context.propertyName.
func hubspotNames(resp fetchResp) []string {
	var body struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
			Context struct {
				PropertyName []string `json:"propertyName"`
			} `json:"context"`
		} `json:"errors"`
	}
	if json.Unmarshal([]byte(resp.Body), &body) != nil {
		return nil
	}
	var names []string
	scan := func(text string) {
		for _, obj := range hsBadObject.FindAllString(text, -1) {
			if m := hsObjectName.FindStringSubmatch(obj); m != nil {
				names = append(names, m[1])
			}
		}
	}
	scan(body.Message)
	for _, e := range body.Errors {
		scan(e.Message)
		names = append(names, e.Context.PropertyName...)
	}
	return names
}

func (hubspot) upsert(j job, c cfg, m []mapRow, f fetcher) result {
	props := applyMapping(j.Fields, m)
	delete(props, "email")
	props["lifecyclestage"] = c.lifecycle()
	build := func(skip map[string]bool) fetchReq {
		p := map[string]string{}
		for k, v := range props {
			if !skip[k] {
				p[k] = v
			}
		}
		body := map[string]any{"inputs": []any{map[string]any{"idProperty": "email", "id": j.Email, "properties": p}}}
		return jsonReq("POST", hsHost+"/crm/v3/objects/contacts/batch/upsert", hsHeaders(c), body)
	}
	resp, dropped := retryWithoutRejected(build, hubspotNames, keysOf(props), f)
	if r, bad := failure(resp); bad {
		r.dropped = dropped
		return r
	}
	if resp.Status == 207 {
		return result{kind: kindFail, status: 207, reason: "rejected", dropped: dropped}
	}
	var out struct {
		Results []struct {
			ID  string `json:"id"`
			New bool   `json:"new"`
		} `json:"results"`
	}
	if json.Unmarshal([]byte(resp.Body), &out) != nil || len(out.Results) == 0 || out.Results[0].ID == "" {
		return result{kind: kindFail, status: resp.Status, reason: "bad answer"}
	}
	id := out.Results[0].ID
	r := result{kind: kindUpdated, status: resp.Status, recordID: id, dropped: dropped}
	var notes []string
	if out.Results[0].New {
		r.kind = kindCreated
		if owner := c.owner(); owner != "" {
			req := jsonReq("PATCH", hsHost+"/crm/v3/objects/contacts/"+id, hsHeaders(c),
				map[string]any{"properties": map[string]string{"hubspot_owner_id": owner}})
			if o := f(req); o.Code != "" || o.Status < 200 || o.Status > 299 {
				notes = append(notes, "owner failed "+codeWord(o))
			}
		}
	}
	r.recordURL = hsRecordURL(id)
	if c.addNote() {
		if code := hsNote(j, c, id, f); code != "" {
			notes = append(notes, "note failed "+code)
		}
	}
	if m := rejectedMessage(dropped); m != "" {
		notes = append(notes, m)
	}
	r.message = strings.Join(notes, "; ")
	return r
}

// hsNote creates the note; it returns "" or the code that made it fail.
func hsNote(j job, c cfg, id string, f fetcher) string {
	num, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "bad id"
	}
	body := map[string]any{
		"properties": map[string]string{
			"hs_timestamp": j.SubmittedAt.UTC().Format(time.RFC3339),
			"hs_note_body": noteText(j, c.leadSource()),
		},
		"associations": []any{map[string]any{
			"to":    map[string]any{"id": num},
			"types": []any{map[string]any{"associationCategory": "HUBSPOT_DEFINED", "associationTypeId": 202}},
		}},
	}
	resp := f(jsonReq("POST", hsHost+"/crm/v3/objects/notes", hsHeaders(c), body))
	if resp.Code != "" || resp.Status < 200 || resp.Status > 299 {
		return codeWord(resp)
	}
	return ""
}

// hsUI is the UI domain when it is a hubspot.com host, else app.hubspot.com.
func hsUI(domain string) string {
	if d := strings.ToLower(strings.TrimSpace(domain)); hsUIDomain.MatchString(d) {
		return d
	}
	return "app.hubspot.com"
}

// hsRecordURL is "" until the portal id is known.
func hsRecordURL(id string) string {
	var p hsPortal
	if ok, err := kvGet("hs_portal", &p); err != nil || !ok || p.PortalID <= 0 {
		return ""
	}
	return "https://" + hsUI(p.UIDomain) + "/contacts/" + strconv.FormatInt(p.PortalID, 10) + "/record/0-1/" + id
}

func (hubspot) test(c cfg, f fetcher) (string, result) {
	resp := f(jsonReq("GET", hsHost+"/account-info/v3/details", hsHeaders(c), nil))
	if r, bad := failure(resp); bad {
		return "", r
	}
	var d struct {
		PortalID int64  `json:"portalId"`
		UIDomain string `json:"uiDomain"`
	}
	if json.Unmarshal([]byte(resp.Body), &d) != nil || d.PortalID <= 0 {
		return "", result{kind: kindFail, status: resp.Status, reason: "bad answer"}
	}
	_ = kvSet("hs_portal", hsPortal{PortalID: d.PortalID, UIDomain: hsUI(d.UIDomain)}, 0)
	return "HubSpot account " + strconv.FormatInt(d.PortalID, 10), result{kind: kindUpdated, status: resp.Status}
}

func (hubspot) fields(c cfg, f fetcher) ([]crmField, result) {
	resp := f(jsonReq("GET", hsHost+"/crm/v3/properties/contacts", hsHeaders(c), nil))
	if r, bad := failure(resp); bad {
		return nil, r
	}
	var d struct {
		Results []struct {
			Name                 string `json:"name"`
			Label                string `json:"label"`
			Hidden               bool   `json:"hidden"`
			Calculated           bool   `json:"calculated"`
			ModificationMetadata struct {
				ReadOnlyValue bool `json:"readOnlyValue"`
			} `json:"modificationMetadata"`
		} `json:"results"`
	}
	if json.Unmarshal([]byte(resp.Body), &d) != nil {
		return nil, result{kind: kindFail, status: resp.Status, reason: "bad answer"}
	}
	var out []crmField
	for _, p := range d.Results {
		if p.Name == "" || p.Name == "email" || p.Hidden || p.Calculated || p.ModificationMetadata.ReadOnlyValue {
			continue
		}
		out = append(out, crmField{Name: p.Name, Label: p.Label})
	}
	return out, result{kind: kindUpdated, status: resp.Status}
}

func (hubspot) refresh(cfg, fetcher) result { return result{kind: kindUpdated} }
