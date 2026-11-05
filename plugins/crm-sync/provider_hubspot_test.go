package main

import (
	"strings"
	"testing"
	"time"
)

func hsCfg() cfg { return cfg{Provider: "hubspot", AccessToken: " tok \n", OwnerID: "77"} }

func hsJob() job {
	return job{
		FormSlug: "contact", Email: "ann@example.com", SourcePath: "/contact",
		SubmittedAt: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC),
		Fields:      map[string]string{"first_name": "Ann", "phone": "123", "fax": "9", "email": "ann@example.com"},
	}
}

var hsMap = []mapRow{{"first_name", "firstname"}, {"phone", "phone"}, {"fax", "fax"}, {"email", "email"}}

const hsOK = `{"status":"COMPLETE","results":[{"id":"501","new":true}]}`

func upsertProps(t *testing.T, req fetchReq) map[string]any {
	t.Helper()
	in := jsonOf(t, req.Body)["inputs"].([]any)[0].(map[string]any)
	return in["properties"].(map[string]any)
}

func TestHubSpotUpsertUsesEmailIDProperty(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":false}]}`})
	c := hsCfg()
	c.AddNote = new(bool)
	r := hubspot{}.upsert(hsJob(), c, hsMap, f.fetch)
	if r.kind != kindUpdated || r.recordID != "501" {
		t.Fatalf("%+v", r)
	}
	req := f.reqs[0]
	if req.Method != "POST" || req.URL != hsHost+"/crm/v3/objects/contacts/batch/upsert" || req.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("%+v", req)
	}
	if !strings.Contains(req.Body, `"idProperty":"email"`) {
		t.Fatalf("body %s", req.Body)
	}
	in := jsonOf(t, req.Body)["inputs"].([]any)[0].(map[string]any)
	p := in["properties"].(map[string]any)
	if in["id"] != "ann@example.com" || p["email"] != nil || p["lifecyclestage"] != "lead" || p["firstname"] != "Ann" {
		t.Fatalf("input %+v", in)
	}
}

func TestHubSpotNewVsExisting(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := hsCfg()
	c.AddNote = new(bool)
	f := newFake(t, fetchResp{Status: 200, Body: hsOK}, fetchResp{Status: 200, Body: `{}`})
	r := hubspot{}.upsert(hsJob(), c, hsMap, f.fetch)
	if r.kind != kindCreated || len(f.reqs) != 2 {
		t.Fatalf("%+v reqs %d", r, len(f.reqs))
	}
	o := f.reqs[1]
	if o.Method != "PATCH" || !strings.HasSuffix(o.URL, "/crm/v3/objects/contacts/501") || !strings.Contains(o.Body, `"hubspot_owner_id":"77"`) {
		t.Fatalf("%+v", o)
	}
	f = newFake(t, fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":false}]}`})
	if r := (hubspot{}).upsert(hsJob(), c, hsMap, f.fetch); r.kind != kindUpdated || len(f.reqs) != 1 {
		t.Fatalf("%+v reqs %d", r, len(f.reqs))
	}
	// A failed owner call only adds to the message.
	f = newFake(t, fetchResp{Status: 200, Body: hsOK}, fetchResp{Status: 500})
	if r := (hubspot{}).upsert(hsJob(), c, hsMap, f.fetch); r.kind != kindCreated || r.message != "owner failed 500" {
		t.Fatalf("%+v", r)
	}
}

func TestHubSpotRejectedPropertyIsDropped(t *testing.T) {
	embedded := `{"status":"error","message":"Property values were not valid: [{\"isValid\":false,\"message\":\"Property \\\"fax\\\" does not exist\",\"error\":\"PROPERTY_DOESNT_EXIST\",\"name\":\"fax\"}]","category":"VALIDATION_ERROR"}`
	ctx := `{"status":"error","message":"bad","errors":[{"message":"x","code":"INVALID_OPTION","context":{"propertyName":["fax"]}}]}`
	for name, first := range map[string]fetchResp{
		"embedded": {Status: 400, Body: embedded},
		"context":  {Status: 400, Body: ctx},
		"207":      {Status: 207, Body: ctx},
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{}
			h.install(t)
			c := hsCfg()
			c.AddNote = new(bool)
			f := newFake(t, first, fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":false}]}`})
			r := hubspot{}.upsert(hsJob(), c, hsMap, f.fetch)
			if r.kind != kindUpdated || r.message != "rejected fields: fax" {
				t.Fatalf("%+v", r)
			}
			if _, has := upsertProps(t, f.reqs[0])["fax"]; !has {
				t.Fatal("first request lacks fax")
			}
			p := upsertProps(t, f.reqs[1])
			if _, has := p["fax"]; has || p["firstname"] != "Ann" {
				t.Fatalf("second %+v", p)
			}
		})
	}
}

func TestHubSpotRejectedUnknownNameIsPlainFailure(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 400, Body: `{"message":"x","errors":[{"context":{"propertyName":["nothing"]}}]}`})
	r := hubspot{}.upsert(hsJob(), hsCfg(), hsMap, f.fetch)
	if r.kind != kindFail || len(f.reqs) != 1 {
		t.Fatalf("%+v reqs %d", r, len(f.reqs))
	}
	// A 207 that cannot be fixed is a failure too.
	f = newFake(t, fetchResp{Status: 207, Body: `{}`})
	if r := (hubspot{}).upsert(hsJob(), hsCfg(), hsMap, f.fetch); r.kind != kindFail || r.reason != "rejected" {
		t.Fatalf("%+v", r)
	}
}

func TestHubSpotNoteBodyAndAssociation(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	j := hsJob()
	j.Fields["message"] = strings.Repeat("x", 1000)
	f := newFake(t, fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":false}]}`}, fetchResp{Status: 201})
	r := hubspot{}.upsert(j, hsCfg(), hsMap, f.fetch)
	if r.kind != kindUpdated || r.message != "" || len(f.reqs) != 2 {
		t.Fatalf("%+v", r)
	}
	n := f.reqs[1]
	if n.URL != hsHost+"/crm/v3/objects/notes" || n.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("%+v", n)
	}
	b := jsonOf(t, n.Body)
	pr := b["properties"].(map[string]any)
	if pr["hs_timestamp"] != "2026-11-05T09:00:00Z" || !strings.HasPrefix(pr["hs_note_body"].(string), "Form: contact") || len(pr["hs_note_body"].(string)) > maxNoteBytes {
		t.Fatalf("properties %+v", pr)
	}
	if body := pr["hs_note_body"].(string); strings.Contains(body, "\n") || !strings.Contains(body, "Form: contact<br>Page: /contact<br>") {
		t.Fatalf("note body not HTML: %q", body)
	}
	a := b["associations"].([]any)[0].(map[string]any)
	if a["to"].(map[string]any)["id"] != float64(501) {
		t.Fatalf("to %+v", a["to"])
	}
	ty := a["types"].([]any)[0].(map[string]any)
	if ty["associationTypeId"] != float64(202) || ty["associationCategory"] != "HUBSPOT_DEFINED" {
		t.Fatalf("types %+v", ty)
	}
}

func TestHubSpotNoteFailureIsNotRetried(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":false}]}`}, fetchResp{Status: 503})
	r := hubspot{}.upsert(hsJob(), hsCfg(), hsMap, f.fetch)
	if r.kind != kindUpdated || r.message != "note failed 503" || len(f.reqs) != 2 {
		t.Fatalf("%+v reqs %d", r, len(f.reqs))
	}
	f = newFake(t, fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":false}]}`}, fetchResp{Code: "timeout"})
	if r := (hubspot{}).upsert(hsJob(), hsCfg(), hsMap, f.fetch); r.kind != kindUpdated || r.message != "note failed timeout" {
		t.Fatalf("%+v", r)
	}
	c := hsCfg()
	c.AddNote = new(bool)
	f = newFake(t, fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":false}]}`})
	if r := (hubspot{}).upsert(hsJob(), c, hsMap, f.fetch); r.message != "" || len(f.reqs) != 1 {
		t.Fatalf("note sent with add_note false: %+v", r)
	}
}

func TestHubSpotFailureClassification(t *testing.T) {
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHost{}
			h.install(t)
			f := newFake(t, tc.resp)
			r := hubspot{}.upsert(hsJob(), hsCfg(), hsMap, f.fetch)
			if r.kind != tc.kind || len(f.reqs) != 1 {
				t.Fatalf("%+v reqs %d", r, len(f.reqs))
			}
		})
	}
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 401})
	if r := (hubspot{}).upsert(hsJob(), hsCfg(), hsMap, f.fetch); r.kind != kindFail || r.reason != "unauthorized" {
		t.Fatalf("%+v", r)
	}
	f = newFake(t, fetchResp{Status: 200, Body: `{"results":[]}`})
	if r := (hubspot{}).upsert(hsJob(), hsCfg(), hsMap, f.fetch); r.kind != kindFail || r.reason != "bad answer" {
		t.Fatalf("%+v", r)
	}
}

func TestHubSpotTestAndFields(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 200, Body: `{"portalId":123456,"uiDomain":"app-eu1.hubspot.com"}`})
	acc, r := hubspot{}.test(hsCfg(), f.fetch)
	if r.kind != kindUpdated || acc != "HubSpot account 123456" || f.reqs[0].URL != hsHost+"/account-info/v3/details" {
		t.Fatalf("%q %+v", acc, r)
	}
	if got := string(h.kv["hs_portal"]); !strings.Contains(got, `"portal_id":123456`) || !strings.Contains(got, "app-eu1.hubspot.com") {
		t.Fatalf("kv %s", got)
	}
	if _, r := (hubspot{}).test(hsCfg(), newFake(t, fetchResp{Status: 401}).fetch); r.kind != kindFail {
		t.Fatalf("%+v", r)
	}

	body := `{"results":[
	 {"name":"firstname","label":"First name"},
	 {"name":"email","label":"Email"},
	 {"name":"secret","label":"S","hidden":true},
	 {"name":"score","label":"C","calculated":true},
	 {"name":"createdate","label":"R","modificationMetadata":{"readOnlyValue":true}},
	 {"name":"fax","label":"Fax","modificationMetadata":{"readOnlyValue":false}}]}`
	f = newFake(t, fetchResp{Status: 200, Body: body})
	got, r := hubspot{}.fields(hsCfg(), f.fetch)
	if r.kind != kindUpdated || len(got) != 2 || got[0] != (crmField{Name: "firstname", Label: "First name"}) || got[1].Name != "fax" {
		t.Fatalf("%+v %+v", got, r)
	}
	if f.reqs[0].URL != hsHost+"/crm/v3/properties/contacts" {
		t.Fatal(f.reqs[0].URL)
	}
	if r := (hubspot{}).refresh(hsCfg(), nil); r.kind != kindUpdated {
		t.Fatalf("%+v", r)
	}
}

func TestHubSpotRecordURL(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	if u := hsRecordURL("5"); u != "" {
		t.Fatalf("url without portal: %q", u)
	}
	for ui, want := range map[string]string{
		"app-eu1.hubspot.com": "https://app-eu1.hubspot.com/contacts/42/record/0-1/5",
		"evil.example.com":    "https://app.hubspot.com/contacts/42/record/0-1/5",
		"hubspot.com.evil.io": "https://app.hubspot.com/contacts/42/record/0-1/5",
		"":                    "https://app.hubspot.com/contacts/42/record/0-1/5",
	} {
		_ = kvSet("hs_portal", hsPortal{PortalID: 42, UIDomain: ui}, 0)
		if u := hsRecordURL("5"); u != want {
			t.Errorf("%q: %q", ui, u)
		}
	}
}

func TestHubSpotNoteEscapesSubmitterMarkup(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	j := hsJob()
	j.Fields["message"] = `<a href="https://x.example">pay</a>`
	f := newFake(t, fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":false}]}`}, fetchResp{Status: 201})
	hubspot{}.upsert(j, hsCfg(), hsMap, f.fetch)
	body := jsonOf(t, f.reqs[1].Body)["properties"].(map[string]any)["hs_note_body"].(string)
	if strings.Contains(body, "<a ") || !strings.Contains(body, "&lt;a href=") {
		t.Fatalf("markup not escaped: %q", body)
	}
}
