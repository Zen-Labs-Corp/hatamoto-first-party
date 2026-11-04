package main

import (
	"crypto/md5"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

func mcCfg() cfg {
	return cfg{Provider: "mailchimp", APIKey: "0123abcd-us21", ListID: "aud1"}
}

func TestMailchimpUpsertRequest(t *testing.T) {
	sum := md5.Sum([]byte("ada@example.com"))
	member := "https://us21.api.mailchimp.com/3.0/lists/aud1/members/" + hex.EncodeToString(sum[:])
	cases := []struct {
		name string
		c    func(cfg) cfg
		want map[string]any
	}{
		{"double opt-in by default", func(c cfg) cfg { return c }, map[string]any{
			"email_address": "Ada@Example.com", "status_if_new": "pending",
			"merge_fields": map[string]any{"FNAME": "Ada", "LNAME": "Lovelace"}}},
		{"single opt-in", func(c cfg) cfg { off := false; c.DoubleOptIn = &off; return c }, map[string]any{
			"email_address": "Ada@Example.com", "status_if_new": "subscribed",
			"merge_fields": map[string]any{"FNAME": "Ada", "LNAME": "Lovelace"}}},
	}
	for _, tc := range cases {
		f := newFake(t, fetchResp{Status: 200})
		r := mailchimp{}.upsert(job{Email: "Ada@Example.com", FirstName: "Ada", LastName: "Lovelace"}, tc.c(mcCfg()), f.fetch)
		if r.kind != kindSent || r.status != 200 {
			t.Fatalf("%s: %+v", tc.name, r)
		}
		req := f.reqs[0]
		if req.Method != "PUT" || req.URL != member {
			t.Fatalf("%s: %s %s", tc.name, req.Method, req.URL)
		}
		if !strings.HasPrefix(req.Headers["Authorization"], "Basic ") || req.Headers["Content-Type"] != "application/json" || req.Headers["Accept"] != "application/json" {
			t.Fatalf("%s: headers %v", tc.name, req.Headers)
		}
		if got := jsonOf(t, req.Body); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: body %v", tc.name, got)
		}
	}
}

func TestMailchimpOmitsEmptyMergeFields(t *testing.T) {
	f := newFake(t, fetchResp{Status: 200})
	mailchimp{}.upsert(job{Email: "a@b.co"}, mcCfg(), f.fetch)
	if _, ok := jsonOf(t, f.reqs[0].Body)["merge_fields"]; ok {
		t.Fatal("empty merge_fields sent")
	}
}

func TestMailchimpTagsAfterUpsert(t *testing.T) {
	c := mcCfg()
	c.Tags = []string{"site", " ", "news"}
	f := newFake(t, fetchResp{Status: 200}, fetchResp{Status: 204})
	r := mailchimp{}.upsert(job{Email: "ada@example.com"}, c, f.fetch)
	if r.kind != kindSent || len(f.reqs) != 2 {
		t.Fatalf("%+v, %d requests", r, len(f.reqs))
	}
	req := f.reqs[1]
	if req.Method != "POST" || !strings.HasSuffix(req.URL, "/tags") || !strings.Contains(req.URL, "/members/") {
		t.Fatalf("%s %s", req.Method, req.URL)
	}
	want := map[string]any{"tags": []any{
		map[string]any{"name": "site", "status": "active"},
		map[string]any{"name": "news", "status": "active"},
	}}
	if got := jsonOf(t, req.Body); !reflect.DeepEqual(got, want) {
		t.Fatalf("tags body %v", got)
	}
	// A failing tag call is classified like any other step.
	f = newFake(t, fetchResp{Status: 200}, fetchResp{Status: 503})
	if r := (mailchimp{}).upsert(job{Email: "ada@example.com"}, c, f.fetch); r.kind != kindRetry || r.status != 503 {
		t.Fatalf("tag failure %+v", r)
	}
}

func TestMailchimpFailureClassification(t *testing.T) {
	testFailureClassification(t, mailchimp{}, mcCfg())
}

func TestMailchimpBadDataCentreNeverFetches(t *testing.T) {
	for _, key := range []string{"nodash", "abc-eu1", "abc-us", "abc-us123", "abc-US1", "abc-us1.evil.com", "abc-us1/x", "-us1", ""} {
		c := mcCfg()
		c.APIKey = key
		f := newFake(t)
		r := mailchimp{}.upsert(job{Email: "a@b.co"}, c, f.fetch)
		if r.kind != kindFail || r.reason != "invalid key" || len(f.reqs) != 0 {
			t.Fatalf("key %q: %+v, %d requests", key, r, len(f.reqs))
		}
		if _, r := (mailchimp{}).test(c, f.fetch); r.kind != kindFail || len(f.reqs) != 0 {
			t.Fatalf("test with key %q: %+v", key, r)
		}
	}
	for _, key := range []string{"k-us1", "k-us21", "k-with-dash-us9"} {
		c := mcCfg()
		c.APIKey = key
		if _, _, ok := (mailchimp{}).base(c); !ok {
			t.Fatalf("key %q refused", key)
		}
	}
}

func TestMailchimpListIDIsEscaped(t *testing.T) {
	c := mcCfg()
	c.ListID = "a/b?c"
	f := newFake(t, fetchResp{Status: 200})
	mailchimp{}.upsert(job{Email: "a@b.co"}, c, f.fetch)
	if !strings.Contains(f.reqs[0].URL, "/lists/a%2Fb%3Fc/members/") {
		t.Fatalf("url %s", f.reqs[0].URL)
	}
}

func TestMailchimpTest(t *testing.T) {
	f := newFake(t, fetchResp{Status: 200, Body: `{"name":"Newsletter subscribers"}`})
	name, r := mailchimp{}.test(mcCfg(), f.fetch)
	if name != "Newsletter subscribers" || r.kind != kindSent {
		t.Fatalf("%q %+v", name, r)
	}
	if f.reqs[0].Method != "GET" || f.reqs[0].URL != "https://us21.api.mailchimp.com/3.0/lists/aud1?fields=name" {
		t.Fatalf("%s %s", f.reqs[0].Method, f.reqs[0].URL)
	}
	if _, r := (mailchimp{}).test(mcCfg(), newFake(t, fetchResp{Status: 401}).fetch); r.kind != kindFail || r.reason != "unauthorized" {
		t.Fatalf("%+v", r)
	}
	if _, r := (mailchimp{}).test(mcCfg(), newFake(t, fetchResp{Status: 200, Body: `{}`}).fetch); r.kind != kindFail {
		t.Fatalf("%+v", r)
	}
}
