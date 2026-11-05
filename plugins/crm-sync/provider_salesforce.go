package main

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

type salesforce struct{}

func init() { providers["salesforce"] = salesforce{} }

const (
	sfAPI      = "v62.0"
	sfTokenKey = "sf_token"
	sfTokenTTL = 7200
	sfMaxDesc  = 32000
)

// sfToken is the cached client-credentials access token.
type sfToken struct {
	AccessToken string    `json:"access_token"`
	InstanceURL string    `json:"instance_url"`
	FetchedAt   time.Time `json:"fetched_at"`
}

var (
	sfNoColumn   = regexp.MustCompile(`No such column '([^']+)'`)
	sfUnableTo   = regexp.MustCompile(`Unable to create/update fields: ([^\n]+)`)
	sfFieldCodes = []string{"INVALID_FIELD", "INVALID_FIELD_FOR_INSERT_UPDATE", "INVALID_OR_NULL_FOR_RESTRICTED_PICKLIST", "STRING_TOO_LONG"}
	// Field types that cannot be filled from one form value.
	sfSkipTypes = []string{"address", "location", "base64", "reference", "complexvalue"}
	sfSkipNames = []string{"Id", "OwnerId", "Email"}
)

// sfInstanceOK accepts only https://<host>.my.salesforce.com with no user,
// port or path, and returns it as "https://host".
func sfInstanceOK(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if normMyDomain(host) != host {
		return "", false
	}
	return "https://" + host, true
}

// soqlEscape escapes a string literal for SOQL.
func soqlEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

// sfClient sends authorised requests, refreshing the token once on a 401.
type sfClient struct {
	c         cfg
	f         fetcher
	tok       sfToken
	refreshed bool
	// fail is set when the token could not be had; fetch then answers with a
	// host-style code and check returns this outcome instead.
	fail *result
}

// token returns the cached token (unless force) or fetches a new one.
func (s *sfClient) token(force bool) (sfToken, result) {
	host := s.c.sfHost()
	if host == "" {
		return sfToken{}, result{kind: kindFail, reason: "bad my domain"}
	}
	if !force {
		var t sfToken
		if ok, err := kvGet(sfTokenKey, &t); err == nil && ok && t.AccessToken != "" {
			if inst, good := sfInstanceOK(t.InstanceURL); good {
				t.InstanceURL = inst
				return t, result{}
			}
		}
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.c.clientID()},
		"client_secret": {s.c.clientSecret()},
	}
	resp := s.f(fetchReq{
		Method:  "POST",
		URL:     "https://" + host + "/services/oauth2/token",
		Headers: map[string]string{"Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded"},
		Body:    form.Encode(),
	})
	if r, bad := failure(resp); bad {
		if r.kind == kindFail && r.status == 400 {
			r.reason = "unauthorized"
		}
		return sfToken{}, r
	}
	var t sfToken
	if json.Unmarshal([]byte(resp.Body), &t) != nil || strings.TrimSpace(t.AccessToken) == "" {
		return sfToken{}, result{kind: kindFail, status: resp.Status, reason: "bad answer"}
	}
	inst, good := sfInstanceOK(t.InstanceURL)
	if !good {
		return sfToken{}, result{kind: kindFail, status: resp.Status, reason: "bad instance url"}
	}
	t.InstanceURL = inst
	t.FetchedAt = now().UTC()
	_ = kvSet(sfTokenKey, t, sfTokenTTL)
	return t, result{}
}

func (s *sfClient) setFail(r result) fetchResp {
	s.fail = &r
	return fetchResp{Code: "token"}
}

// fetch sends req, whose URL is a path below the instance.
func (s *sfClient) fetch(req fetchReq) fetchResp {
	if s.fail != nil {
		return fetchResp{Code: "token"}
	}
	if s.tok.AccessToken == "" {
		t, r := s.token(false)
		if r.kind != "" {
			return s.setFail(r)
		}
		s.tok = t
	}
	resp := s.send(req)
	if resp.Code == "" && resp.Status == 401 && !s.refreshed {
		s.refreshed = true
		t, r := s.token(true)
		if r.kind != "" {
			return s.setFail(r)
		}
		s.tok = t
		resp = s.send(req)
	}
	return resp
}

func (s *sfClient) send(req fetchReq) fetchResp {
	h := map[string]string{}
	for k, v := range req.Headers {
		h[k] = v
	}
	h["Authorization"] = "Bearer " + s.tok.AccessToken
	req.URL = s.tok.InstanceURL + req.URL
	req.Headers = h
	return s.f(req)
}

// check is the outcome of a non-2xx answer, or false for a 2xx.
func (s *sfClient) check(resp fetchResp) (result, bool) {
	if s.fail != nil {
		return *s.fail, true
	}
	return failure(resp)
}

func (s *sfClient) req(method, path string, body any) fetchReq {
	return jsonReq(method, "/services/data/"+sfAPI+path, nil, body)
}

// query runs a SOQL query and returns the raw records.
func (s *sfClient) query(soql string) ([]map[string]any, fetchResp, result) {
	resp := s.fetch(s.req("GET", "/query?q="+url.QueryEscape(soql), nil))
	if r, bad := s.check(resp); bad {
		return nil, resp, r
	}
	var out struct {
		Records []map[string]any `json:"records"`
	}
	if json.Unmarshal([]byte(resp.Body), &out) != nil {
		return nil, resp, result{kind: kindFail, status: resp.Status, reason: "bad answer"}
	}
	return out.Records, resp, result{}
}

// sfNames reads the field names a 400 answer complains about: fields[] of the
// codes that name fields, plus the names inside the message text (an unknown
// column often comes with an empty fields list).
func sfNames(resp fetchResp) []string {
	type apiErr struct {
		Message string   `json:"message"`
		Code    string   `json:"errorCode"`
		Fields  []string `json:"fields"`
	}
	var errs []apiErr
	if json.Unmarshal([]byte(resp.Body), &errs) != nil {
		var one apiErr
		if json.Unmarshal([]byte(resp.Body), &one) != nil {
			return nil
		}
		errs = []apiErr{one}
	}
	var names []string
	for _, e := range errs {
		if slices.Contains(sfFieldCodes, e.Code) {
			names = append(names, e.Fields...)
		}
		for _, m := range sfNoColumn.FindAllStringSubmatch(e.Message, -1) {
			names = append(names, m[1])
		}
		if m := sfUnableTo.FindStringSubmatch(e.Message); m != nil {
			list, _, _ := strings.Cut(m[1], ". ")
			for _, n := range strings.Split(list, ", ") {
				names = append(names, strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(n), ".")))
			}
		}
	}
	return names
}

func sfObject(c cfg) string {
	if c.object() == "contact" {
		return "Contact"
	}
	return "Lead"
}

func (salesforce) upsert(j job, c cfg, m []mapRow, f fetcher) result {
	s := &sfClient{c: c, f: f}
	obj := sfObject(c)
	soql := "SELECT Id FROM " + obj + " WHERE Email = '" + soqlEscape(j.Email) + "'"
	if obj == "Lead" {
		soql += " AND IsConverted = false"
	}
	soql += " ORDER BY CreatedDate DESC LIMIT 1"
	recs, _, r := s.query(soql)
	if r.kind != "" {
		return r
	}
	id := ""
	if len(recs) > 0 {
		id, _ = recs[0]["Id"].(string)
	}
	create := id == ""

	props := applyMapping(j.Fields, m)
	props["Email"] = j.Email
	if props["LastName"] == "" {
		props["LastName"] = j.Email
	}
	if obj == "Lead" {
		if props["Company"] == "" {
			props["Company"] = c.defaultCompany()
		}
	} else {
		delete(props, "Company")
	}
	if create {
		if obj == "Lead" {
			props["LeadSource"] = c.leadSource()
		}
		if owner := c.owner(); owner != "" {
			props["OwnerId"] = owner
		}
	}
	var sent []string
	for k := range props {
		if k != "Email" && k != "LastName" && k != "Company" {
			sent = append(sent, k)
		}
	}
	slices.Sort(sent)
	build := func(skip map[string]bool) fetchReq {
		p := map[string]string{}
		for k, v := range props {
			if !skip[k] {
				p[k] = v
			}
		}
		if create {
			return s.req("POST", "/sobjects/"+obj, p)
		}
		return s.req("PATCH", "/sobjects/"+obj+"/"+url.PathEscape(id), p)
	}
	resp, dropped := retryWithoutRejected(build, sfNames, sent, s.fetch)
	if r, bad := s.check(resp); bad {
		r.dropped = dropped
		return r
	}
	out := result{kind: kindUpdated, status: resp.Status, dropped: dropped}
	if create {
		var made struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(resp.Body), &made) != nil || made.ID == "" {
			return result{kind: kindFail, status: resp.Status, reason: "bad answer"}
		}
		id = made.ID
		out.kind = kindCreated
	}
	out.recordID = id
	out.recordURL = s.tok.InstanceURL + "/" + url.PathEscape(id)

	var notes []string
	if c.addNote() {
		task := map[string]string{
			"WhoId":        id,
			"Subject":      "Website form: " + j.FormSlug,
			"Description":  cut(noteText(j, c.leadSource()), sfMaxDesc),
			"Status":       "Completed",
			"ActivityDate": j.SubmittedAt.UTC().Format("2006-01-02"),
		}
		if nr := s.fetch(s.req("POST", "/sobjects/Task", task)); nr.Code != "" || nr.Status < 200 || nr.Status > 299 {
			notes = append(notes, "note failed "+codeWord(nr))
		}
	}
	if m := rejectedMessage(dropped); m != "" {
		notes = append(notes, m)
	}
	out.message = strings.Join(notes, "; ")
	return out
}

func (salesforce) test(c cfg, f fetcher) (string, result) {
	s := &sfClient{c: c, f: f}
	// A fresh token proves the credentials; a cached one would not.
	t, r := s.token(true)
	if r.kind != "" {
		return "", r
	}
	s.tok = t
	recs, resp, r := s.query("SELECT Name FROM Organization")
	if r.kind != "" {
		return "", r
	}
	name := ""
	if len(recs) > 0 {
		name, _ = recs[0]["Name"].(string)
	}
	return orDefault(name, "Salesforce org"), result{kind: kindUpdated, status: resp.Status}
}

func (salesforce) fields(c cfg, f fetcher) ([]crmField, result) {
	s := &sfClient{c: c, f: f}
	resp := s.fetch(s.req("GET", "/sobjects/"+sfObject(c)+"/describe", nil))
	if r, bad := s.check(resp); bad {
		return nil, r
	}
	var d struct {
		Fields []struct {
			Name              string `json:"name"`
			Label             string `json:"label"`
			Type              string `json:"type"`
			Createable        bool   `json:"createable"`
			Updateable        bool   `json:"updateable"`
			Nillable          bool   `json:"nillable"`
			DefaultedOnCreate bool   `json:"defaultedOnCreate"`
		} `json:"fields"`
	}
	if json.Unmarshal([]byte(resp.Body), &d) != nil {
		return nil, result{kind: kindFail, status: resp.Status, reason: "bad answer"}
	}
	var out []crmField
	for _, x := range d.Fields {
		if x.Name == "" || !(x.Createable || x.Updateable) || slices.Contains(sfSkipTypes, x.Type) || slices.Contains(sfSkipNames, x.Name) {
			continue
		}
		out = append(out, crmField{Name: x.Name, Label: x.Label, Required: x.Createable && !x.Nillable && !x.DefaultedOnCreate})
	}
	return out, result{kind: kindUpdated, status: resp.Status}
}

func (salesforce) refresh(c cfg, f fetcher) result {
	s := &sfClient{c: c, f: f}
	if _, r := s.token(true); r.kind != "" {
		return r
	}
	return result{kind: kindUpdated}
}
