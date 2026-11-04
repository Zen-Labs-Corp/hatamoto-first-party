package main

import (
	"reflect"
	"testing"
)

func kitCfg() cfg { return cfg{Provider: "kit", APIKey: "kitkey", ListID: "55"} }

func TestKitUpsertThenForm(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		kind   string
	}{{"new subscriber", 201, kindSent}, {"existing subscriber", 200, kindUpdated}} {
		f := newFake(t, fetchResp{Status: tc.status}, fetchResp{Status: 201})
		r := kit{}.upsert(job{Email: "Ada@Example.com", FirstName: "Ada", LastName: "Lovelace"}, kitCfg(), f.fetch)
		if r.kind != tc.kind || r.status != tc.status || len(f.reqs) != 2 {
			t.Fatalf("%s: %+v, %d requests", tc.name, r, len(f.reqs))
		}
		up, form := f.reqs[0], f.reqs[1]
		if up.Method != "POST" || up.URL != "https://api.kit.com/v4/subscribers" {
			t.Fatalf("%s: %s %s", tc.name, up.Method, up.URL)
		}
		if up.Headers["X-Kit-Api-Key"] != "kitkey" || up.Headers["Content-Type"] != "application/json" || up.Headers["Accept"] != "application/json" {
			t.Fatalf("%s: headers %v", tc.name, up.Headers)
		}
		// No last_name: it needs a custom field the account may not have.
		if got := jsonOf(t, up.Body); !reflect.DeepEqual(got, map[string]any{"email_address": "Ada@Example.com", "first_name": "Ada", "state": "inactive"}) {
			t.Fatalf("%s: upsert body %v", tc.name, got)
		}
		if form.Method != "POST" || form.URL != "https://api.kit.com/v4/forms/55/subscribers" || form.Headers["X-Kit-Api-Key"] != "kitkey" {
			t.Fatalf("%s: form request %s %s", tc.name, form.Method, form.URL)
		}
		if got := jsonOf(t, form.Body); !reflect.DeepEqual(got, map[string]any{"email_address": "Ada@Example.com"}) {
			t.Fatalf("%s: form body %v", tc.name, got)
		}
	}
}

func TestKitStateFollowsDoubleOptIn(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		opt  *bool
		want string
	}{{"default", nil, "inactive"}, {"on", &on, "inactive"}, {"off", &off, "active"}} {
		c := kitCfg()
		c.DoubleOptIn = tc.opt
		f := newFake(t, fetchResp{Status: 201}, fetchResp{Status: 201})
		kit{}.upsert(job{Email: "a@b.co"}, c, f.fetch)
		if got := jsonOf(t, f.reqs[0].Body)["state"]; got != tc.want {
			t.Errorf("%s: state %v, want %s", tc.name, got, tc.want)
		}
	}
}

func TestKitOmitsEmptyFirstName(t *testing.T) {
	f := newFake(t, fetchResp{Status: 201}, fetchResp{Status: 201})
	kit{}.upsert(job{Email: "a@b.co"}, kitCfg(), f.fetch)
	if _, ok := jsonOf(t, f.reqs[0].Body)["first_name"]; ok {
		t.Fatal("empty first_name sent")
	}
}

func TestKitFailureClassification(t *testing.T) {
	testFailureClassification(t, kit{}, kitCfg())
}

func TestKitFormStepFailureIsClassified(t *testing.T) {
	for _, tc := range failureCases {
		f := newFake(t, fetchResp{Status: 201}, tc.resp)
		if r := (kit{}).upsert(job{Email: "a@b.co"}, kitCfg(), f.fetch); r.kind != tc.kind {
			t.Errorf("form step %s: %q, want %q", tc.name, r.kind, tc.kind)
		}
	}
}

func TestKitTagsResolvedByNameAcrossPages(t *testing.T) {
	c := kitCfg()
	c.Tags = []string{"Site", "missing", "news"}
	f := newFake(t,
		fetchResp{Status: 201}, fetchResp{Status: 201},
		fetchResp{Status: 200, Body: `{"tags":[{"id":1,"name":"site"},{"id":2,"name":"other"}],"pagination":{"has_next_page":true,"end_cursor":"c1"}}`},
		fetchResp{Status: 200, Body: `{"tags":[{"id":3,"name":"News"}],"pagination":{"has_next_page":false,"end_cursor":"c2"}}`},
		fetchResp{Status: 201}, fetchResp{Status: 201},
	)
	r := kit{}.upsert(job{Email: "a@b.co"}, c, f.fetch)
	if r.kind != kindSent || len(f.reqs) != 6 {
		t.Fatalf("%+v, %d requests", r, len(f.reqs))
	}
	if f.reqs[2].Method != "GET" || f.reqs[2].URL != "https://api.kit.com/v4/tags" || f.reqs[3].URL != "https://api.kit.com/v4/tags?after=c1" {
		t.Fatalf("tag lookups %s, %s", f.reqs[2].URL, f.reqs[3].URL)
	}
	if f.reqs[4].Method != "POST" || f.reqs[4].URL != "https://api.kit.com/v4/tags/1/subscribers" || f.reqs[5].URL != "https://api.kit.com/v4/tags/3/subscribers" {
		t.Fatalf("tag posts %s, %s", f.reqs[4].URL, f.reqs[5].URL)
	}
	if got := jsonOf(t, f.reqs[4].Body); !reflect.DeepEqual(got, map[string]any{"email_address": "a@b.co"}) {
		t.Fatalf("tag body %v", got)
	}
}

func TestKitTagStepFailuresAreClassified(t *testing.T) {
	c := kitCfg()
	c.Tags = []string{"site"}
	tags := fetchResp{Status: 200, Body: `{"tags":[{"id":1,"name":"site"}],"pagination":{"has_next_page":false}}`}
	if r := (kit{}).upsert(job{Email: "a@b.co"}, c, newFake(t, fetchResp{Status: 201}, fetchResp{Status: 201}, fetchResp{Status: 503}).fetch); r.kind != kindRetry {
		t.Fatalf("tag list 503: %+v", r)
	}
	if r := (kit{}).upsert(job{Email: "a@b.co"}, c, newFake(t, fetchResp{Status: 201}, fetchResp{Status: 201}, tags, fetchResp{Status: 422}).fetch); r.kind != kindFail {
		t.Fatalf("tag add 422: %+v", r)
	}
	// Unknown tags only: nothing to add, delivery still succeeds.
	none := fetchResp{Status: 200, Body: `{"tags":[],"pagination":{"has_next_page":false}}`}
	if r := (kit{}).upsert(job{Email: "a@b.co"}, c, newFake(t, fetchResp{Status: 201}, fetchResp{Status: 201}, none).fetch); r.kind != kindSent {
		t.Fatalf("unknown tag: %+v", r)
	}
}

func TestKitTestPagesThroughForms(t *testing.T) {
	f := newFake(t,
		fetchResp{Status: 200, Body: `{"forms":[{"id":1,"name":"One"}],"pagination":{"has_next_page":true,"end_cursor":"x"}}`},
		fetchResp{Status: 200, Body: `{"forms":[{"id":55,"name":"Newsletter"}],"pagination":{"has_next_page":false}}`},
	)
	name, r := kit{}.test(kitCfg(), f.fetch)
	if name != "Newsletter" || r.kind != kindSent {
		t.Fatalf("%q %+v", name, r)
	}
	if f.reqs[0].Method != "GET" || f.reqs[0].URL != "https://api.kit.com/v4/forms" || f.reqs[1].URL != "https://api.kit.com/v4/forms?after=x" || f.reqs[0].Headers["X-Kit-Api-Key"] != "kitkey" {
		t.Fatalf("requests %+v", f.reqs)
	}
	f = newFake(t, fetchResp{Status: 200, Body: `{"forms":[],"pagination":{"has_next_page":false}}`})
	if _, r := (kit{}).test(kitCfg(), f.fetch); r.kind != kindFail || r.reason != "list not found" {
		t.Fatalf("%+v", r)
	}
	if _, r := (kit{}).test(kitCfg(), newFake(t, fetchResp{Status: 401}).fetch); r.kind != kindFail || r.reason != "unauthorized" {
		t.Fatalf("%+v", r)
	}
}

func TestProviderForKnowsAllFour(t *testing.T) {
	for _, n := range []string{"mailchimp", "brevo", "mailerlite", "kit"} {
		if providerFor(n) == nil {
			t.Fatalf("%s missing", n)
		}
	}
	if providerFor("other") != nil || providerFor("") != nil {
		t.Fatal("unknown provider resolved")
	}
}
