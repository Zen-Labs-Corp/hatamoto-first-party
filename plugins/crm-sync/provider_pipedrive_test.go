package main

import (
	"strings"
	"testing"
	"time"
)

const pdBaseURL = "https://acme.pipedrive.com"

func pdCfg() cfg {
	return cfg{Provider: "pipedrive", APIToken: " pdtok\n", CompanyDomain: "Acme.pipedrive.com", OwnerID: "9"}
}

func pdJob() job {
	return job{
		FormSlug: "contact", Email: "ann@example.com", SourcePath: "/contact",
		SubmittedAt: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC),
		Fields:      map[string]string{"first_name": "Ann", "last_name": "Lee", "phone": "123", "company": "Acme Pty", "ref": "r1"},
	}
}

var pdMap = []mapRow{{"first_name", "first_name"}, {"last_name", "last_name"}, {"phone", "phones"}, {"company", "org_name"}, {"ref", "abc123"}}

const (
	pdNoHit  = `{"success":true,"data":{"items":[]}}`
	pdPerson = `{"success":true,"data":{"items":[{"result_score":0.5,"item":{"id":42,"type":"person"}}]}}`
	pdSaved  = `{"success":true,"data":{"id":42}}`
	pdNoLead = `{"success":true,"data":null}`
	pdLeadGT = `{"success":true,"data":[{"id":"0f1e2d3c"}]}`
)

func pdResp(body string) fetchResp { return fetchResp{Status: 200, Body: body} }

func noNote(c cfg) cfg { c.AddNote = new(bool); return c }

func noLead(c cfg) cfg { c.CreateLead = new(bool); return c }

func TestPipedriveExistingPersonIsPatchedNotPosted(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := noNote(noLead(pdCfg()))
	f := newFake(t, pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), pdResp(pdSaved))
	r := pipedrive{}.upsert(pdJob(), c, pdMap, f.fetch)
	if r.kind != kindUpdated || r.recordID != "42" || r.recordURL != pdBaseURL+"/person/42" {
		t.Fatalf("%+v", r)
	}
	if f.reqs[0].Method != "GET" || !strings.HasPrefix(f.reqs[0].URL, pdBaseURL+"/api/v2/persons/search?") {
		t.Fatalf("%+v", f.reqs[0])
	}
	for _, want := range []string{"term=ann%40example.com", "fields=email", "exact_match=true", "limit=1"} {
		if !strings.Contains(f.reqs[0].URL, want) {
			t.Errorf("search url lacks %s: %s", want, f.reqs[0].URL)
		}
	}
	if f.reqs[0].Headers["x-api-token"] != "pdtok" {
		t.Fatalf("%+v", f.reqs[0].Headers)
	}
	for _, q := range f.reqs {
		if q.Method == "POST" && strings.HasSuffix(q.URL, "/persons") {
			t.Fatal("POST /persons for an existing person")
		}
	}
	w := f.reqs[3]
	if w.Method != "PATCH" || w.URL != pdBaseURL+"/api/v2/persons/42" {
		t.Fatalf("%+v", w)
	}
	b := jsonOf(t, w.Body)
	if b["emails"] != nil || b["owner_id"] != nil || b["first_name"] != "Ann" || b["org_id"] != float64(7) || b["phones"] == nil {
		t.Fatalf("body %s", w.Body)
	}
}

func TestPipedriveNewPersonPostsEmailsAndNames(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := noNote(noLead(pdCfg()))
	f := newFake(t, pdResp(pdNoHit), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), fetchResp{Status: 201, Body: pdSaved})
	r := pipedrive{}.upsert(pdJob(), c, pdMap, f.fetch)
	if r.kind != kindCreated || r.recordID != "42" {
		t.Fatalf("%+v", r)
	}
	if f.reqs[2].Method != "POST" || f.reqs[2].URL != pdBaseURL+"/api/v2/organizations" || !strings.Contains(f.reqs[2].Body, `"name":"Acme Pty"`) {
		t.Fatalf("%+v", f.reqs[2])
	}
	w := f.reqs[3]
	if w.Method != "POST" || w.URL != pdBaseURL+"/api/v2/persons" {
		t.Fatalf("%+v", w)
	}
	b := jsonOf(t, w.Body)
	em := b["emails"].([]any)[0].(map[string]any)
	if em["value"] != "ann@example.com" || em["primary"] != true || em["label"] != "work" || b["owner_id"] != float64(9) ||
		b["org_id"] != float64(7) || b["first_name"] != "Ann" || b["last_name"] != "Lee" {
		t.Fatalf("body %s", w.Body)
	}
}

func TestPipedriveExistingLeadIsReused(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := noNote(pdCfg())
	f := newFake(t, pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), pdResp(pdSaved), pdResp(pdLeadGT))
	r := pipedrive{}.upsert(pdJob(), c, pdMap, f.fetch)
	if r.kind != kindUpdated || r.recordURL != pdBaseURL+"/leads/inbox/0f1e2d3c" {
		t.Fatalf("%+v", r)
	}
	if len(f.reqs) != 5 {
		t.Fatalf("%d requests", len(f.reqs))
	}
	g := f.reqs[4]
	if g.Method != "GET" || !strings.Contains(g.URL, "/api/v1/leads?") || !strings.Contains(g.URL, "person_id=42") || strings.Contains(g.URL, "archived_status") {
		t.Fatalf("%+v", g)
	}
}

func TestPipedriveCreatesLeadWhenNone(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := noNote(pdCfg())
	for _, empty := range []string{pdNoLead, `{"success":true,"data":[]}`} {
		f := newFake(t, pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), pdResp(pdSaved), pdResp(empty), fetchResp{Status: 201, Body: `{"data":{"id":"uuid-1"}}`})
		r := pipedrive{}.upsert(pdJob(), c, pdMap, f.fetch)
		if r.kind != kindUpdated || r.recordURL != pdBaseURL+"/leads/inbox/uuid-1" {
			t.Fatalf("%+v", r)
		}
		p := f.reqs[5]
		b := jsonOf(t, p.Body)
		if p.Method != "POST" || p.URL != pdBaseURL+"/api/v1/leads" || b["title"] != "Ann Lee (website form contact)" ||
			b["person_id"] != float64(42) || b["organization_id"] != float64(7) || b["owner_id"] != float64(9) {
			t.Fatalf("%+v", p)
		}
	}
}

func TestPipedriveOrgFindOrCreate(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := noNote(noLead(pdCfg()))
	// Hit, with the id nested one level up as well.
	f := newFake(t, pdResp(pdNoHit), pdResp(`{"data":{"items":[{"id":55}]}}`), pdResp(pdSaved))
	pipedrive{}.upsert(pdJob(), c, pdMap, f.fetch)
	if len(f.reqs) != 3 || !strings.Contains(f.reqs[1].URL, "/api/v2/organizations/search?") || !strings.Contains(f.reqs[1].URL, "fields=name") {
		t.Fatalf("%d requests", len(f.reqs))
	}
	if jsonOf(t, f.reqs[2].Body)["org_id"] != float64(55) {
		t.Fatalf("body %s", f.reqs[2].Body)
	}
	// Miss: created, then linked.
	f = newFake(t, pdResp(pdNoHit), pdResp(pdNoHit), pdResp(`{"data":{"id":8}}`), pdResp(pdSaved))
	pipedrive{}.upsert(pdJob(), c, pdMap, f.fetch)
	if jsonOf(t, f.reqs[3].Body)["org_id"] != float64(8) {
		t.Fatalf("body %s", f.reqs[3].Body)
	}
	// Org create answer without an id is a bad answer.
	f = newFake(t, pdResp(pdNoHit), pdResp(pdNoHit), pdResp(`{}`))
	if r := (pipedrive{}).upsert(pdJob(), c, pdMap, f.fetch); r.kind != kindFail || r.reason != "bad answer" {
		t.Fatalf("%+v", r)
	}
}

func TestPipedriveCustomFieldsGoUnderCustomFields(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := noNote(noLead(pdCfg()))
	f := newFake(t, pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), pdResp(pdSaved))
	pipedrive{}.upsert(pdJob(), c, pdMap, f.fetch)
	b := jsonOf(t, f.reqs[3].Body)
	cf, _ := b["custom_fields"].(map[string]any)
	if cf["abc123"] != "r1" || b["abc123"] != nil || b["org_name"] != nil {
		t.Fatalf("body %s", f.reqs[3].Body)
	}
}

func TestPipedriveNameRules(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	m := []mapRow{{"first_name", "first_name"}, {"last_name", "last_name"}, {"nick", "name"}}
	cases := []struct {
		name   string
		fields map[string]string
		create bool
		want   map[string]any
	}{
		{"create both", map[string]string{"first_name": "Ann", "last_name": "Lee"}, true, map[string]any{"first_name": "Ann", "last_name": "Lee"}},
		{"create first only", map[string]string{"first_name": "Ann"}, true, map[string]any{"name": "Ann"}},
		{"create name mapped", map[string]string{"nick": "AL"}, true, map[string]any{"name": "AL"}},
		{"create none", map[string]string{}, true, map[string]any{"name": "ann@example.com"}},
		{"update both", map[string]string{"first_name": "Ann", "last_name": "Lee"}, false, map[string]any{"first_name": "Ann", "last_name": "Lee"}},
		{"update first only", map[string]string{"first_name": "Ann"}, false, map[string]any{}},
		{"update name mapped", map[string]string{"nick": "AL"}, false, map[string]any{}},
		{"update none", map[string]string{}, false, map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := pdJob()
			j.Fields = tc.fields
			first := pdNoHit
			if !tc.create {
				first = pdPerson
			}
			f := newFake(t, pdResp(first), pdResp(pdSaved))
			pipedrive{}.upsert(j, noNote(noLead(pdCfg())), m, f.fetch)
			b := jsonOf(t, f.reqs[1].Body)
			for _, k := range []string{"first_name", "last_name", "name"} {
				if b[k] != tc.want[k] {
					t.Errorf("%s = %v, want %v (body %s)", k, b[k], tc.want[k], f.reqs[1].Body)
				}
			}
		})
	}
}

func TestPipedriveRejectedFieldIsDropped(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	c := noNote(noLead(pdCfg()))
	f := newFake(t, pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`),
		fetchResp{Status: 400, Body: `{"success":false,"error":"Field abc123 is invalid"}`}, pdResp(pdSaved))
	r := pipedrive{}.upsert(pdJob(), c, pdMap, f.fetch)
	if r.kind != kindUpdated || r.message != "rejected fields: abc123" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(f.reqs[3].Body, "abc123") || strings.Contains(f.reqs[4].Body, "abc123") || !strings.Contains(f.reqs[4].Body, `"first_name":"Ann"`) {
		t.Fatalf("bodies %s | %s", f.reqs[3].Body, f.reqs[4].Body)
	}
	// Nothing recognisable in the text: a plain failure, no second write.
	f = newFake(t, pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), fetchResp{Status: 400, Body: `{"success":false,"error":"nope"}`})
	if r := (pipedrive{}).upsert(pdJob(), c, pdMap, f.fetch); r.kind != kindFail || len(f.reqs) != 4 || r.message != "" {
		t.Fatalf("%+v", r)
	}
}

func TestPipedriveNoteIsHTMLEscaped(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	j := pdJob()
	j.Fields["ref"] = `<script>alert("x")</script> & 'y'`
	f := newFake(t, pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), pdResp(pdSaved), pdResp(pdLeadGT), fetchResp{Status: 201})
	r := pipedrive{}.upsert(j, pdCfg(), pdMap, f.fetch)
	if r.kind != kindUpdated || r.message != "" {
		t.Fatalf("%+v", r)
	}
	n := f.reqs[5]
	if n.Method != "POST" || n.URL != pdBaseURL+"/api/v1/notes" {
		t.Fatalf("%+v", n)
	}
	b := jsonOf(t, n.Body)
	content := b["content"].(string)
	if strings.Contains(content, "<script>") || strings.Contains(content, "\n") || !strings.Contains(content, "&lt;script&gt;") ||
		!strings.Contains(content, "&amp;") || !strings.Contains(content, "Form: contact<br>Page: /contact<br>") {
		t.Fatalf("content %q", content)
	}
	if b["person_id"] != float64(42) || b["lead_id"] != "0f1e2d3c" {
		t.Fatalf("body %s", n.Body)
	}
	if noteHTML("a\nb<") != "a<br>b&lt;" {
		t.Fatal(noteHTML("a\nb<"))
	}
}

func TestPipedriveNoteFailureIsNotRetried(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), pdResp(pdSaved), pdResp(pdLeadGT), fetchResp{Status: 503})
	r := pipedrive{}.upsert(pdJob(), pdCfg(), pdMap, f.fetch)
	if r.kind != kindUpdated || r.message != "note failed 503" || len(f.reqs) != 6 {
		t.Fatalf("%+v reqs %d", r, len(f.reqs))
	}
}

func TestPipedriveLeadFailureKeepsThePersonResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		lead []fetchResp
		want string
	}{
		{"forbidden", []fetchResp{{Status: 403}}, "lead failed 403"},
		{"server error on create", []fetchResp{pdResp(pdNoLead), {Status: 503}}, "lead failed 503"},
		{"host error", []fetchResp{{Code: "timeout"}}, "lead failed timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHost{}
			h.install(t)
			resps := append([]fetchResp{pdResp(pdPerson), pdResp(pdNoHit), pdResp(`{"data":{"id":7}}`), pdResp(pdSaved)}, tc.lead...)
			resps = append(resps, fetchResp{Status: 201})
			f := newFake(t, resps...)
			r := pipedrive{}.upsert(pdJob(), pdCfg(), pdMap, f.fetch)
			if r.kind != kindUpdated || r.recordID != "42" || r.recordURL != pdBaseURL+"/person/42" || r.message != tc.want {
				t.Fatalf("%+v", r)
			}
			n := f.reqs[len(f.reqs)-1]
			b := jsonOf(t, n.Body)
			if n.URL != pdBaseURL+"/api/v1/notes" || b["person_id"] != float64(42) || b["lead_id"] != nil {
				t.Fatalf("note %+v", n)
			}
			for _, q := range f.reqs {
				if q.Method == "POST" && strings.HasSuffix(q.URL, "/api/v2/persons") {
					t.Fatal("person written twice")
				}
			}
		})
	}
}

func TestPipedriveFailureClassification(t *testing.T) {
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHost{}
			h.install(t)
			f := newFake(t, tc.resp)
			r := pipedrive{}.upsert(pdJob(), pdCfg(), pdMap, f.fetch)
			if r.kind != tc.kind || len(f.reqs) != 1 {
				t.Fatalf("%+v reqs %d", r, len(f.reqs))
			}
		})
	}
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, fetchResp{Status: 401})
	if r := (pipedrive{}).upsert(pdJob(), pdCfg(), pdMap, f.fetch); r.reason != "unauthorized" {
		t.Fatalf("%+v", r)
	}
}

func TestPipedriveCompanyDomainRefusedBeforeFetch(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	for _, d := range []string{"", "evil.com/x", "a.b.pipedrive.com", "acme.com", "-x", "a b"} {
		c := pdCfg()
		c.CompanyDomain = d
		f := newFake(t)
		if r := (pipedrive{}).upsert(pdJob(), c, pdMap, f.fetch); r.kind != kindFail || r.reason != "bad company domain" || len(f.reqs) != 0 {
			t.Errorf("%q: %+v reqs %d", d, r, len(f.reqs))
		}
		if _, r := (pipedrive{}).test(c, f.fetch); r.kind != kindFail || len(f.reqs) != 0 {
			t.Errorf("test %q: %+v", d, r)
		}
		if _, r := (pipedrive{}).fields(c, f.fetch); r.kind != kindFail || len(f.reqs) != 0 {
			t.Errorf("fields %q: %+v", d, r)
		}
	}
}

func TestPipedriveFields(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	p1 := `{"data":[{"field_code":"name","field_name":"Name","is_custom_field":false},{"field_code":"abc123","field_name":"Ref","is_custom_field":true}],"additional_data":{"next_cursor":"CUR"}}`
	p2 := `{"data":[{"field_code":"def456","field_name":"","is_custom_field":true},{"field_code":"phones","is_custom_field":false}],"additional_data":{"next_cursor":null}}`
	f := newFake(t, pdResp(p1), pdResp(p2))
	got, r := pipedrive{}.fields(pdCfg(), f.fetch)
	if r.kind != kindUpdated || len(f.reqs) != 2 {
		t.Fatalf("%+v reqs %d", r, len(f.reqs))
	}
	if !strings.Contains(f.reqs[0].URL, "/api/v2/personFields?") || !strings.Contains(f.reqs[0].URL, "limit=500") || !strings.Contains(f.reqs[1].URL, "cursor=CUR") {
		t.Fatalf("%s %s", f.reqs[0].URL, f.reqs[1].URL)
	}
	var names []string
	for _, x := range got {
		names = append(names, x.Name)
		if x.Required {
			t.Errorf("%s required", x.Name)
		}
	}
	if strings.Join(names, ",") != "first_name,last_name,name,phones,abc123,def456,org_name" || got[5].Label != "def456" || got[4].Label != "Ref" {
		t.Fatalf("%v %+v", names, got)
	}
	// Paging stops after three pages.
	f = newFake(t, pdResp(p1), pdResp(p1), pdResp(p1))
	if _, r := (pipedrive{}).fields(pdCfg(), f.fetch); r.kind != kindUpdated || len(f.reqs) != 3 {
		t.Fatalf("%+v reqs %d", r, len(f.reqs))
	}
	if _, r := (pipedrive{}).fields(pdCfg(), newFake(t, fetchResp{Status: 500}).fetch); r.kind != kindRetry {
		t.Fatalf("%+v", r)
	}
}

func TestPipedriveTest(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	f := newFake(t, pdResp(`{"success":true,"data":{"company_name":"Acme Pty","company_domain":"acme"}}`))
	acc, r := pipedrive{}.test(pdCfg(), f.fetch)
	if r.kind != kindUpdated || acc != "Acme Pty" || f.reqs[0].URL != pdBaseURL+"/api/v1/users/me" || f.reqs[0].Headers["x-api-token"] != "pdtok" {
		t.Fatalf("%q %+v", acc, r)
	}
	if acc, _ := (pipedrive{}).test(pdCfg(), newFake(t, pdResp(`{"data":{}}`)).fetch); acc != "acme" {
		t.Fatalf("fallback %q", acc)
	}
	if _, r := (pipedrive{}).test(pdCfg(), newFake(t, fetchResp{Status: 401}).fetch); r.kind != kindFail {
		t.Fatalf("%+v", r)
	}
	if r := (pipedrive{}).refresh(pdCfg(), nil); r.kind != kindUpdated {
		t.Fatalf("%+v", r)
	}
}
