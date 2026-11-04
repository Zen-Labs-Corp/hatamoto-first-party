package main

import (
	"reflect"
	"testing"
)

func brevoCfg() cfg { return cfg{Provider: "brevo", APIKey: "xkeysib-1", ListID: "7"} }

func TestBrevoUpsertRequestAndExistingContact(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		kind   string
	}{{"new contact", 201, kindSent}, {"existing contact", 204, kindUpdated}} {
		f := newFake(t, fetchResp{Status: tc.status})
		r := brevo{}.upsert(job{Email: "Ada@Example.com", FirstName: "Ada", LastName: "Lovelace"}, brevoCfg(), f.fetch)
		if r.kind != tc.kind || r.status != tc.status {
			t.Fatalf("%s: %+v", tc.name, r)
		}
		req := f.reqs[0]
		if req.Method != "POST" || req.URL != "https://api.brevo.com/v3/contacts" {
			t.Fatalf("%s: %s %s", tc.name, req.Method, req.URL)
		}
		if req.Headers["api-key"] != "xkeysib-1" || req.Headers["Content-Type"] != "application/json" || req.Headers["Accept"] != "application/json" {
			t.Fatalf("%s: headers %v", tc.name, req.Headers)
		}
		want := map[string]any{
			"email": "Ada@Example.com", "listIds": []any{float64(7)}, "updateEnabled": true,
			"attributes": map[string]any{"FIRSTNAME": "Ada", "LASTNAME": "Lovelace"},
		}
		if got := jsonOf(t, req.Body); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: body %v", tc.name, got)
		}
	}
}

func TestBrevoOmitsEmptyAttributes(t *testing.T) {
	f := newFake(t, fetchResp{Status: 201})
	brevo{}.upsert(job{Email: "a@b.co"}, brevoCfg(), f.fetch)
	if _, ok := jsonOf(t, f.reqs[0].Body)["attributes"]; ok {
		t.Fatal("empty attributes sent")
	}
}

func TestBrevoFailureClassification(t *testing.T) {
	testFailureClassification(t, brevo{}, brevoCfg())
}

func TestBrevoInvalidListNeverFetches(t *testing.T) {
	for _, id := range []string{"", "abc", "0", "-3", "1.5"} {
		c := brevoCfg()
		c.ListID = id
		f := newFake(t)
		r := brevo{}.upsert(job{Email: "a@b.co"}, c, f.fetch)
		if r.kind != kindFail || r.reason != "invalid list" || len(f.reqs) != 0 {
			t.Fatalf("list %q: %+v", id, r)
		}
	}
}

func TestBrevoTest(t *testing.T) {
	f := newFake(t, fetchResp{Status: 200, Body: `{"id":7,"name":"Customers"}`})
	name, r := brevo{}.test(brevoCfg(), f.fetch)
	if name != "Customers" || r.kind != kindSent {
		t.Fatalf("%q %+v", name, r)
	}
	if f.reqs[0].Method != "GET" || f.reqs[0].URL != "https://api.brevo.com/v3/contacts/lists/7" {
		t.Fatalf("%s %s", f.reqs[0].Method, f.reqs[0].URL)
	}
	if _, r := (brevo{}).test(brevoCfg(), newFake(t, fetchResp{Status: 404}).fetch); r.kind != kindFail {
		t.Fatalf("%+v", r)
	}
}

func TestBrevoTrimsKeyAndListID(t *testing.T) {
	c := cfg{Provider: "brevo", APIKey: " xkeysib-1\n", ListID: " 7 "}
	f := newFake(t, fetchResp{Status: 201})
	if r := (brevo{}).upsert(job{Email: "a@b.co"}, c, f.fetch); r.kind != kindSent {
		t.Fatalf("%+v", r)
	}
	if f.reqs[0].Headers["api-key"] != "xkeysib-1" {
		t.Fatalf("key %q", f.reqs[0].Headers["api-key"])
	}
	if got := jsonOf(t, f.reqs[0].Body)["listIds"]; !reflect.DeepEqual(got, []any{float64(7)}) {
		t.Fatalf("listIds %v", got)
	}
}
