package main

import (
	"reflect"
	"testing"
)

func mlCfg() cfg { return cfg{Provider: "mailerlite", APIKey: "mlkey", ListID: "123"} }

func TestMailerLiteUpsertRequestAndExistingSubscriber(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		kind   string
	}{{"new subscriber", 201, kindSent}, {"existing subscriber", 200, kindUpdated}} {
		f := newFake(t, fetchResp{Status: tc.status})
		r := mailerlite{}.upsert(job{Email: "Ada@Example.com", FirstName: "Ada", LastName: "Lovelace"}, mlCfg(), f.fetch)
		if r.kind != tc.kind || r.status != tc.status {
			t.Fatalf("%s: %+v", tc.name, r)
		}
		req := f.reqs[0]
		if req.Method != "POST" || req.URL != "https://connect.mailerlite.com/api/subscribers" {
			t.Fatalf("%s: %s %s", tc.name, req.Method, req.URL)
		}
		if req.Headers["Authorization"] != "Bearer mlkey" || req.Headers["Content-Type"] != "application/json" || req.Headers["Accept"] != "application/json" {
			t.Fatalf("%s: headers %v", tc.name, req.Headers)
		}
		want := map[string]any{
			"email": "Ada@Example.com", "groups": []any{"123"},
			"fields": map[string]any{"name": "Ada", "last_name": "Lovelace"},
		}
		if got := jsonOf(t, req.Body); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: body %v", tc.name, got)
		}
	}
}

func TestMailerLiteOmitsEmptyFields(t *testing.T) {
	f := newFake(t, fetchResp{Status: 201})
	mailerlite{}.upsert(job{Email: "a@b.co"}, mlCfg(), f.fetch)
	if _, ok := jsonOf(t, f.reqs[0].Body)["fields"]; ok {
		t.Fatal("empty fields sent")
	}
}

func TestMailerLiteFailureClassification(t *testing.T) {
	testFailureClassification(t, mailerlite{}, mlCfg())
}

func TestMailerLiteTestPagesThroughGroups(t *testing.T) {
	f := newFake(t,
		fetchResp{Status: 200, Body: `{"data":[{"id":"1","name":"One"}],"meta":{"last_page":2}}`},
		fetchResp{Status: 200, Body: `{"data":[{"id":"123","name":"Customers"}],"meta":{"last_page":2}}`},
	)
	name, r := mailerlite{}.test(mlCfg(), f.fetch)
	if name != "Customers" || r.kind != kindSent || len(f.reqs) != 2 {
		t.Fatalf("%q %+v, %d requests", name, r, len(f.reqs))
	}
	if f.reqs[0].Method != "GET" || f.reqs[0].URL != "https://connect.mailerlite.com/api/groups?limit=100&page=1" || f.reqs[1].URL != "https://connect.mailerlite.com/api/groups?limit=100&page=2" {
		t.Fatalf("urls %s, %s", f.reqs[0].URL, f.reqs[1].URL)
	}
}

func TestMailerLiteTestMissingGroupAndPageCap(t *testing.T) {
	f := newFake(t, fetchResp{Status: 200, Body: `{"data":[{"id":"1","name":"One"}],"meta":{"last_page":1}}`})
	if _, r := (mailerlite{}).test(mlCfg(), f.fetch); r.kind != kindFail || r.reason != "list not found" || len(f.reqs) != 1 {
		t.Fatalf("%+v", r)
	}
	endless := fetchResp{Status: 200, Body: `{"data":[{"id":"1","name":"One"}],"meta":{"last_page":99}}`}
	f = newFake(t, endless, endless, endless, endless, endless)
	if _, r := (mailerlite{}).test(mlCfg(), f.fetch); r.kind != kindFail || len(f.reqs) != maxPages {
		t.Fatalf("%+v, %d requests", r, len(f.reqs))
	}
	if _, r := (mailerlite{}).test(mlCfg(), newFake(t, fetchResp{Status: 401}).fetch); r.kind != kindFail || r.reason != "unauthorized" {
		t.Fatalf("%+v", r)
	}
}
