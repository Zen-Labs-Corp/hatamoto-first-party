package main

import (
	"strings"
	"testing"
	"time"
)

const (
	sfInst  = "https://acme.my.salesforce.com"
	sfData  = sfInst + "/services/data/v62.0"
	sfTokOK = `{"access_token":"AT1","instance_url":"https://acme.my.salesforce.com","token_type":"Bearer","issued_at":"1"}`
)

func sfCfg() cfg {
	return cfg{
		Provider: "salesforce", MyDomain: "https://Acme.my.salesforce.com/", ClientID: " cid ", ClientSecret: " sec\n",
		OwnerID: "005000000000001AAA",
	}
}

func sfJob() job {
	return job{
		FormSlug: "contact", Email: "ann@example.com", SourcePath: "/contact",
		SubmittedAt: time.Date(2026, 11, 5, 23, 30, 0, 0, time.UTC),
		Fields:      map[string]string{"first_name": "Ann", "phone": "123", "x": "xv"},
	}
}

var sfMap = []mapRow{{"first_name", "FirstName"}, {"phone", "Phone"}, {"x", "Foo__c"}}

func sfSeed(t *testing.T, inst string) {
	t.Helper()
	if err := kvSet("sf_token", sfToken{AccessToken: "AT0", InstanceURL: inst}, 0); err != nil {
		t.Fatal(err)
	}
}

func sfNoNote(c cfg) cfg { c.AddNote = new(bool); return c }

const (
	sfNoRec  = `{"totalSize":0,"done":true,"records":[]}`
	sfOneRec = `{"totalSize":1,"done":true,"records":[{"attributes":{"type":"Lead"},"Id":"00Q000000000001AAA"}]}`
	sfCreate = `{"id":"00Q000000000009AAA","success":true,"errors":[]}`
)

func sfProps(t *testing.T, req fetchReq) map[string]any { return jsonOf(t, req.Body) }

func TestSalesforceTokenCachedInKV(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := sfNoNote(sfCfg())
	f := newFake(t,
		fetchResp{Status: 200, Body: sfTokOK}, sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate},
		sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	if r := (salesforce{}).upsert(sfJob(), c, sfMap, f.fetch); r.kind != kindCreated {
		t.Fatalf("%+v", r)
	}
	tr := f.reqs[0]
	if tr.Method != "POST" || tr.URL != "https://acme.my.salesforce.com/services/oauth2/token" || tr.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("%+v", tr)
	}
	if tr.Body != "client_id=cid&client_secret=sec&grant_type=client_credentials" {
		t.Fatalf("token body %q", tr.Body)
	}
	if h.kvTTL["sf_token"] != 7200 || !strings.Contains(string(h.kv["sf_token"]), `"access_token":"AT1"`) {
		t.Fatalf("kv %s ttl %d", h.kv["sf_token"], h.kvTTL["sf_token"])
	}
	if f.reqs[1].Headers["Authorization"] != "Bearer AT1" {
		t.Fatalf("%+v", f.reqs[1].Headers)
	}
	if r := (salesforce{}).upsert(sfJob(), c, sfMap, f.fetch); r.kind != kindCreated {
		t.Fatalf("%+v", r)
	}
	if len(f.reqs) != 5 {
		t.Fatalf("second upsert made a token request: %d requests", len(f.reqs))
	}
}

func TestSalesforce401RefreshesOnce(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	c := sfNoNote(sfCfg())
	f := newFake(t, fetchResp{Status: 401, Body: `[{"errorCode":"INVALID_SESSION_ID","message":"Session expired"}]`},
		fetchResp{Status: 200, Body: sfTokOK}, sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	r := (salesforce{}).upsert(sfJob(), c, sfMap, f.fetch)
	if r.kind != kindCreated {
		t.Fatalf("%+v", r)
	}
	if f.reqs[0].Headers["Authorization"] != "Bearer AT0" || !strings.Contains(f.reqs[1].URL, "/oauth2/token") || f.reqs[2].Headers["Authorization"] != "Bearer AT1" {
		t.Fatalf("%+v", f.reqs)
	}
	// Two 401s in a row: one refresh, two data requests, then unauthorized.
	h = &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	f = newFake(t, fetchResp{Status: 401}, fetchResp{Status: 200, Body: sfTokOK}, fetchResp{Status: 401})
	r = (salesforce{}).upsert(sfJob(), c, sfMap, f.fetch)
	if r.kind != kindFail || r.reason != "unauthorized" || len(f.reqs) != 3 {
		t.Fatalf("%+v requests %d", r, len(f.reqs))
	}
	// A refresh that fails with a transient error is retried later.
	h = &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	f = newFake(t, fetchResp{Status: 401}, fetchResp{Status: 503})
	if r = (salesforce{}).upsert(sfJob(), c, sfMap, f.fetch); r.kind != kindRetry || len(f.reqs) != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestSalesforceInstanceURLMustBeMyDomain(t *testing.T) {
	good := map[string]string{
		"https://a.my.salesforce.com":                  "https://a.my.salesforce.com",
		"https://acme--dev.sandbox.my.salesforce.com/": "https://acme--dev.sandbox.my.salesforce.com",
		"https://A.My.Salesforce.com":                  "https://a.my.salesforce.com",
	}
	for in, want := range good {
		if got, ok := sfInstanceOK(in); !ok || got != want {
			t.Errorf("%s: %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{
		"https://evil.com", "http://a.my.salesforce.com", "https://a.my.salesforce.com.evil.com", "https://a.my.salesforce.com:444",
		"https://u@a.my.salesforce.com", "https://a.my.salesforce.com/path", "https://my.salesforce.com", "https://.my.salesforce.com",
		"https://a.my.salesforce.com?x=1", "", "a.my.salesforce.com", "https://a_b.my.salesforce.com",
	} {
		if got, ok := sfInstanceOK(bad); ok {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}

	// A token answer naming a foreign instance is refused and not cached.
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 200, Body: `{"access_token":"AT1","instance_url":"https://evil.com"}`})
	r := (salesforce{}).upsert(sfJob(), sfCfg(), sfMap, f.fetch)
	if r.kind != kindFail || r.reason != "bad instance url" || len(f.reqs) != 1 || h.kv["sf_token"] != nil {
		t.Fatalf("%+v requests %d kv %s", r, len(f.reqs), h.kv["sf_token"])
	}
	// A cached foreign URL is ignored and the token is fetched again.
	h = &fakeHost{}
	h.install(t)
	sfSeed(t, "https://evil.com")
	f = newFake(t, fetchResp{Status: 200, Body: sfTokOK}, sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	if r := (salesforce{}).upsert(sfJob(), sfNoNote(sfCfg()), sfMap, f.fetch); r.kind != kindCreated {
		t.Fatalf("%+v", r)
	}
	for _, q := range f.reqs {
		if strings.Contains(q.URL, "evil.com") {
			t.Fatalf("request to %s", q.URL)
		}
	}
	// A sandbox host works end to end.
	h = &fakeHost{}
	h.install(t)
	c := sfNoNote(sfCfg())
	c.MyDomain = "acme--dev.sandbox.my.salesforce.com"
	f = newFake(t, fetchResp{Status: 200, Body: `{"access_token":"AT1","instance_url":"https://acme--dev.sandbox.my.salesforce.com"}`},
		sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	if r := (salesforce{}).upsert(sfJob(), c, sfMap, f.fetch); r.kind != kindCreated || !strings.HasPrefix(r.recordURL, "https://acme--dev.sandbox.my.salesforce.com/") {
		t.Fatalf("%+v", r)
	}
}

func TestSalesforceExistingLeadIsPatched(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	f := newFake(t, sfResp(sfOneRec), fetchResp{Status: 204}, fetchResp{Status: 201})
	r := (salesforce{}).upsert(sfJob(), sfCfg(), sfMap, f.fetch)
	if r.kind != kindUpdated || r.recordID != "00Q000000000001AAA" || r.recordURL != sfInst+"/00Q000000000001AAA" {
		t.Fatalf("%+v", r)
	}
	q := f.reqs[0]
	if q.Method != "GET" || !strings.HasPrefix(q.URL, sfData+"/query?q=SELECT+Id+FROM+Lead+WHERE+Email+%3D+%27ann%40example.com%27+AND+IsConverted+%3D+false+ORDER+BY+CreatedDate+DESC+LIMIT+1") {
		t.Fatalf("query %s", q.URL)
	}
	p := f.reqs[1]
	if p.Method != "PATCH" || p.URL != sfData+"/sobjects/Lead/00Q000000000001AAA" {
		t.Fatalf("%+v", p)
	}
	b := sfProps(t, p)
	if b["OwnerId"] != nil || b["LeadSource"] != nil || b["FirstName"] != "Ann" || b["Email"] != "ann@example.com" || b["Company"] != "Website visitor" {
		t.Fatalf("patch body %s", p.Body)
	}
	for _, x := range f.reqs {
		if x.Method == "POST" && strings.HasSuffix(x.URL, "/sobjects/Lead") {
			t.Fatal("POST for an existing lead")
		}
	}
}

func TestSalesforceExistingContactIsPatched(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	c := sfNoNote(sfCfg())
	c.Object = "contact"
	f := newFake(t, sfResp(`{"records":[{"Id":"003000000000001AAA"}]}`), fetchResp{Status: 204})
	m := append([]mapRow{{"company", "Company"}}, sfMap...)
	j := sfJob()
	j.Fields["company"] = "Acme"
	r := (salesforce{}).upsert(j, c, m, f.fetch)
	if r.kind != kindUpdated || f.reqs[1].Method != "PATCH" || f.reqs[1].URL != sfData+"/sobjects/Contact/003000000000001AAA" || len(f.reqs) != 2 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(f.reqs[0].URL, "IsConverted") || !strings.Contains(f.reqs[0].URL, "FROM+Contact+") {
		t.Fatalf("query %s", f.reqs[0].URL)
	}
	b := sfProps(t, f.reqs[1])
	if b["Company"] != nil || b["OwnerId"] != nil || b["LeadSource"] != nil {
		t.Fatalf("body %s", f.reqs[1].Body)
	}
}

func TestSalesforceCreateSetsLeadSourceOwnerCompanyLastName(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	f := newFake(t, sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	r := (salesforce{}).upsert(sfJob(), sfNoNote(sfCfg()), sfMap, f.fetch)
	if r.kind != kindCreated || r.recordID != "00Q000000000009AAA" {
		t.Fatalf("%+v", r)
	}
	p := f.reqs[1]
	if p.Method != "POST" || p.URL != sfData+"/sobjects/Lead" {
		t.Fatalf("%+v", p)
	}
	b := sfProps(t, p)
	if b["LastName"] != "ann@example.com" || b["Company"] != "Website visitor" || b["LeadSource"] != "Website" || b["OwnerId"] != "005000000000001AAA" || b["Email"] != "ann@example.com" {
		t.Fatalf("body %s", p.Body)
	}
	// Mapped last name and company win; a Contact has no LeadSource or Company.
	j := sfJob()
	j.Fields["ln"], j.Fields["co"] = "Lee", "Acme"
	m := append([]mapRow{{"ln", "LastName"}, {"co", "Company"}}, sfMap...)
	f = newFake(t, sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	(salesforce{}).upsert(j, sfNoNote(sfCfg()), m, f.fetch)
	if b := sfProps(t, f.reqs[1]); b["LastName"] != "Lee" || b["Company"] != "Acme" {
		t.Fatalf("body %s", f.reqs[1].Body)
	}
	c := sfNoNote(sfCfg())
	c.Object = "contact"
	f = newFake(t, sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	(salesforce{}).upsert(j, c, m, f.fetch)
	if b := sfProps(t, f.reqs[1]); b["Company"] != nil || b["LeadSource"] != nil || b["OwnerId"] == nil || f.reqs[1].URL != sfData+"/sobjects/Contact" {
		t.Fatalf("body %s", f.reqs[1].Body)
	}
	// An invalid owner id is not sent.
	c = sfNoNote(sfCfg())
	c.OwnerID = "nope"
	f = newFake(t, sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	(salesforce{}).upsert(sfJob(), c, sfMap, f.fetch)
	if b := sfProps(t, f.reqs[1]); b["OwnerId"] != nil {
		t.Fatalf("body %s", f.reqs[1].Body)
	}
	// A create answer without an id is a bad answer.
	f = newFake(t, sfResp(sfNoRec), fetchResp{Status: 201, Body: `{}`})
	if r := (salesforce{}).upsert(sfJob(), sfNoNote(sfCfg()), sfMap, f.fetch); r.kind != kindFail || r.reason != "bad answer" {
		t.Fatalf("%+v", r)
	}
}

func TestSalesforceSOQLEscape(t *testing.T) {
	if got := soqlEscape(`o'brien@example.com`); got != `o\'brien@example.com` {
		t.Fatal(got)
	}
	if got := soqlEscape(`a\'b`); got != `a\\\'b` {
		t.Fatal(got)
	}
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	j := sfJob()
	j.Email = `o'brien@example.com`
	f := newFake(t, sfResp(sfNoRec), fetchResp{Status: 201, Body: sfCreate})
	(salesforce{}).upsert(j, sfNoNote(sfCfg()), sfMap, f.fetch)
	if !strings.Contains(f.reqs[0].URL, "%27o%5C%27brien%40example.com%27") {
		t.Fatalf("query %s", f.reqs[0].URL)
	}
}

func TestSalesforceRejectedFieldIsDropped(t *testing.T) {
	cases := map[string]string{
		"empty fields": `[{"message":"No such column 'Foo__c' on sobject of type Lead","errorCode":"INVALID_FIELD","fields":[]}]`,
		"no fields":    `[{"message":"No such column 'Foo__c' on sobject of type Lead","errorCode":"INVALID_FIELD"}]`,
		"fields list":  `[{"message":"bad value","errorCode":"INVALID_OR_NULL_FOR_RESTRICTED_PICKLIST","fields":["Foo__c"]}]`,
		"unable to":    `[{"message":"Unable to create/update fields: Foo__c, Other. Please check the security settings of this field.","errorCode":"INVALID_FIELD_FOR_INSERT_UPDATE"}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{}
			h.install(t)
			sfSeed(t, sfInst)
			f := newFake(t, sfResp(sfNoRec), fetchResp{Status: 400, Body: body}, fetchResp{Status: 201, Body: sfCreate})
			r := (salesforce{}).upsert(sfJob(), sfNoNote(sfCfg()), sfMap, f.fetch)
			if r.kind != kindCreated || r.message != "rejected fields: Foo__c" {
				t.Fatalf("%+v", r)
			}
			if !strings.Contains(f.reqs[1].Body, "Foo__c") || strings.Contains(f.reqs[2].Body, "Foo__c") || !strings.Contains(f.reqs[2].Body, `"FirstName":"Ann"`) {
				t.Fatalf("bodies %s | %s", f.reqs[1].Body, f.reqs[2].Body)
			}
		})
	}
	// A required field is never dropped, and an unknown name is a plain failure.
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	for _, body := range []string{
		`[{"message":"Required fields are missing: [LastName]","errorCode":"REQUIRED_FIELD_MISSING","fields":["LastName"]}]`,
		`[{"message":"x","errorCode":"INVALID_FIELD","fields":["Nothing__c"]}]`,
		`not json`,
	} {
		f := newFake(t, sfResp(sfNoRec), fetchResp{Status: 400, Body: body})
		if r := (salesforce{}).upsert(sfJob(), sfNoNote(sfCfg()), sfMap, f.fetch); r.kind != kindFail || len(f.reqs) != 2 {
			t.Fatalf("%s: %+v requests %d", body, r, len(f.reqs))
		}
	}
	// Rejection on update (PATCH) works the same way.
	f := newFake(t, sfResp(sfOneRec), fetchResp{Status: 400, Body: cases["empty fields"]}, fetchResp{Status: 204})
	if r := (salesforce{}).upsert(sfJob(), sfNoNote(sfCfg()), sfMap, f.fetch); r.kind != kindUpdated || r.message != "rejected fields: Foo__c" {
		t.Fatalf("%+v", r)
	}
}

func TestSalesforceNoteFailureIsNotRetried(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	f := newFake(t, sfResp(sfOneRec), fetchResp{Status: 204}, fetchResp{Status: 503})
	r := (salesforce{}).upsert(sfJob(), sfCfg(), sfMap, f.fetch)
	if r.kind != kindUpdated || r.message != "note failed 503" || len(f.reqs) != 3 {
		t.Fatalf("%+v requests %d", r, len(f.reqs))
	}
	n := f.reqs[2]
	if n.Method != "POST" || n.URL != sfData+"/sobjects/Task" {
		t.Fatalf("%+v", n)
	}
	b := jsonOf(t, n.Body)
	if b["WhoId"] != "00Q000000000001AAA" || b["Subject"] != "Website form: contact" || b["Status"] != "Completed" ||
		b["ActivityDate"] != "2026-11-05" || !strings.HasPrefix(b["Description"].(string), "Form: contact") {
		t.Fatalf("task %s", n.Body)
	}
	// Success has no message.
	f = newFake(t, sfResp(sfOneRec), fetchResp{Status: 204}, fetchResp{Status: 201})
	if r := (salesforce{}).upsert(sfJob(), sfCfg(), sfMap, f.fetch); r.message != "" {
		t.Fatalf("%+v", r)
	}
}

func TestSalesforceFailureClassification(t *testing.T) {
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHost{}
			h.install(t)
			sfSeed(t, sfInst)
			f := newFake(t, tc.resp, fetchResp{Status: 200, Body: sfTokOK}, fetchResp{Status: 401})
			r := (salesforce{}).upsert(sfJob(), sfCfg(), sfMap, f.fetch)
			if r.kind != tc.kind {
				t.Fatalf("%+v", r)
			}
			// Only a 401 leads to a second request (the refresh); the rest stop.
			if tc.resp.Status != 401 && len(f.reqs) != 1 {
				t.Fatalf("requests %d", len(f.reqs))
			}
		})
	}
	// A token endpoint refusing the client is a failure that is not retried.
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 400, Body: `{"error":"invalid_client","error_description":"secret text"}`})
	r := (salesforce{}).upsert(sfJob(), sfCfg(), sfMap, f.fetch)
	if r.kind != kindFail || r.reason != "unauthorized" {
		t.Fatalf("%+v", r)
	}
	f = newFake(t, fetchResp{Status: 200, Body: `{"instance_url":"https://acme.my.salesforce.com"}`})
	if r := (salesforce{}).upsert(sfJob(), sfCfg(), sfMap, f.fetch); r.kind != kindFail || r.reason != "bad answer" {
		t.Fatalf("%+v", r)
	}
}

func TestSalesforceRefreshAndTest(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 200, Body: sfTokOK})
	if r := (salesforce{}).refresh(sfCfg(), f.fetch); r.kind != kindUpdated {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(string(h.kv["sf_token"]), "AT1") || h.kvTTL["sf_token"] != 7200 {
		t.Fatalf("kv %s", h.kv["sf_token"])
	}
	// A refresh always asks for a new token, even with one cached.
	f = newFake(t, fetchResp{Status: 400, Body: `{"error":"invalid_client"}`})
	if r := (salesforce{}).refresh(sfCfg(), f.fetch); r.kind != kindFail || len(f.reqs) != 1 {
		t.Fatalf("%+v", r)
	}
	f = newFake(t, fetchResp{Status: 503})
	if r := (salesforce{}).refresh(sfCfg(), f.fetch); r.kind != kindRetry {
		t.Fatalf("%+v", r)
	}

	f = newFake(t, fetchResp{Status: 200, Body: sfTokOK}, sfResp(`{"records":[{"Name":"Acme Pty Ltd"}]}`))
	acc, r := (salesforce{}).test(sfCfg(), f.fetch)
	if r.kind != kindUpdated || acc != "Acme Pty Ltd" || !strings.Contains(f.reqs[1].URL, "SELECT+Name+FROM+Organization") {
		t.Fatalf("%q %+v", acc, r)
	}
	f = newFake(t, fetchResp{Status: 400, Body: `{"error":"invalid_client"}`})
	if _, r := (salesforce{}).test(sfCfg(), f.fetch); r.kind != kindFail || r.reason != "unauthorized" {
		t.Fatalf("%+v", r)
	}
	f = newFake(t, fetchResp{Status: 200, Body: sfTokOK}, fetchResp{Status: 403})
	if _, r := (salesforce{}).test(sfCfg(), f.fetch); r.kind != kindFail {
		t.Fatalf("%+v", r)
	}
}

func TestSalesforceFieldsFilter(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	body := `{"fields":[
	 {"name":"Id","label":"Id","type":"id","createable":false,"updateable":false},
	 {"name":"OwnerId","label":"Owner","type":"reference","createable":true,"updateable":true},
	 {"name":"Email","label":"Email","type":"email","createable":true,"updateable":true,"nillable":true},
	 {"name":"LastName","label":"Last Name","type":"string","createable":true,"updateable":true,"nillable":false,"defaultedOnCreate":false},
	 {"name":"Company","label":"Company","type":"string","createable":true,"updateable":true,"nillable":false},
	 {"name":"Status","label":"Status","type":"picklist","createable":true,"updateable":true,"nillable":false,"defaultedOnCreate":true},
	 {"name":"Phone","label":"Phone","type":"phone","createable":true,"updateable":true,"nillable":true},
	 {"name":"Address","label":"Address","type":"address","createable":false,"updateable":false},
	 {"name":"Geo","label":"Geo","type":"location","createable":true,"updateable":true},
	 {"name":"Photo","label":"Photo","type":"base64","createable":true,"updateable":true},
	 {"name":"Parent","label":"Parent","type":"reference","createable":true,"updateable":true},
	 {"name":"Score","label":"Score","type":"double","createable":false,"updateable":false,"nillable":true},
	 {"name":"OnlyUpdate","label":"Only update","type":"string","createable":false,"updateable":true,"nillable":true}]}`
	f := newFake(t, sfResp(body))
	got, r := (salesforce{}).fields(sfCfg(), f.fetch)
	if r.kind != kindUpdated || f.reqs[0].URL != sfData+"/sobjects/Lead/describe" {
		t.Fatalf("%+v %s", r, f.reqs[0].URL)
	}
	want := []crmField{
		{"LastName", "Last Name", true}, {"Company", "Company", true}, {"Status", "Status", false},
		{"Phone", "Phone", false}, {"OnlyUpdate", "Only update", false},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v want %+v", i, got[i], want[i])
		}
	}
	c := sfCfg()
	c.Object = "contact"
	f = newFake(t, sfResp(`{"fields":[]}`))
	(salesforce{}).fields(c, f.fetch)
	if f.reqs[0].URL != sfData+"/sobjects/Contact/describe" {
		t.Fatal(f.reqs[0].URL)
	}
	if _, r := (salesforce{}).fields(sfCfg(), newFake(t, fetchResp{Status: 500}).fetch); r.kind != kindRetry {
		t.Fatalf("%+v", r)
	}
}

func TestSalesforceMyDomainRefusedBeforeFetch(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	sfSeed(t, sfInst)
	for _, d := range []string{"", "evil.com", "acme.my.salesforce.com.evil.com", "acme.my.salesforce.com:444", "a b.my.salesforce.com", "acme.my.salesforce.com/x"} {
		c := sfCfg()
		c.MyDomain = d
		f := newFake(t)
		if r := (salesforce{}).upsert(sfJob(), c, sfMap, f.fetch); r.kind != kindFail || r.reason != "bad my domain" || len(f.reqs) != 0 {
			t.Errorf("%q: %+v requests %d", d, r, len(f.reqs))
		}
		if r := (salesforce{}).refresh(c, f.fetch); r.kind != kindFail || len(f.reqs) != 0 {
			t.Errorf("refresh %q: %+v", d, r)
		}
		if _, r := (salesforce{}).test(c, f.fetch); r.kind != kindFail || len(f.reqs) != 0 {
			t.Errorf("test %q: %+v", d, r)
		}
		if _, r := (salesforce{}).fields(c, f.fetch); r.kind != kindFail || len(f.reqs) != 0 {
			t.Errorf("fields %q: %+v", d, r)
		}
	}
}

func sfResp(body string) fetchResp { return fetchResp{Status: 200, Body: body} }
