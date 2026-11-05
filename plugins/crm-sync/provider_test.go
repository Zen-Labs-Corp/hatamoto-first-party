package main

import (
	"slices"
	"strings"
	"testing"
)

// namesFromBody reads "bad:a,b" as the rejected names of a fake answer.
func namesFromBody(r fetchResp) []string {
	if rest, ok := strings.CutPrefix(r.Body, "bad:"); ok {
		return strings.Split(rest, ",")
	}
	return nil
}

func buildFor(sent []string) func(map[string]bool) fetchReq {
	return func(skip map[string]bool) fetchReq {
		var kept []string
		for _, s := range sent {
			if !skip[s] {
				kept = append(kept, s)
			}
		}
		return fetchReq{Method: "POST", URL: "https://example.com", Body: strings.Join(kept, ",")}
	}
}

func TestRetryWithoutRejectedDropsNamedFields(t *testing.T) {
	sent := []string{"a", "b", "c"}
	f := newFake(t, fetchResp{Status: 400, Body: "bad:c,a"}, fetchResp{Status: 200})
	resp, dropped := retryWithoutRejected(buildFor(sent), namesFromBody, sent, f.fetch)
	if resp.Status != 200 || !slices.Equal(dropped, []string{"a", "c"}) {
		t.Fatalf("resp %+v dropped %v", resp, dropped)
	}
	if len(f.reqs) != 2 || f.reqs[0].Body != "a,b,c" || f.reqs[1].Body != "b" {
		t.Fatalf("requests %+v", f.reqs)
	}
	if rejectedMessage(dropped) != "rejected fields: a, c" || rejectedMessage(nil) != "" {
		t.Fatal("rejectedMessage")
	}
}

func TestRetryWithoutRejectedOnlyOnce(t *testing.T) {
	sent := []string{"a", "b", "c"}
	f := newFake(t, fetchResp{Status: 400, Body: "bad:a"}, fetchResp{Status: 400, Body: "bad:b"})
	resp, dropped := retryWithoutRejected(buildFor(sent), namesFromBody, sent, f.fetch)
	if resp.Status != 400 || !slices.Equal(dropped, []string{"a"}) || len(f.reqs) != 2 {
		t.Fatalf("resp %+v dropped %v requests %d", resp, dropped, len(f.reqs))
	}
}

func TestRetryWithoutRejectedIgnoresUnknownNames(t *testing.T) {
	sent := []string{"a"}
	f := newFake(t, fetchResp{Status: 400, Body: "bad:zzz"})
	resp, dropped := retryWithoutRejected(buildFor(sent), namesFromBody, sent, f.fetch)
	if resp.Status != 400 || dropped != nil || len(f.reqs) != 1 {
		t.Fatalf("resp %+v dropped %v requests %d", resp, dropped, len(f.reqs))
	}
	// Other statuses and host errors are never retried this way.
	for _, r := range []fetchResp{{Status: 500, Body: "bad:a"}, {Status: 200}, {Code: "timeout", Body: "bad:a"}} {
		f := newFake(t, r)
		if _, dropped := retryWithoutRejected(buildFor(sent), namesFromBody, sent, f.fetch); dropped != nil || len(f.reqs) != 1 {
			t.Fatalf("%+v retried", r)
		}
	}
	// A 207 is handled like a 400.
	f = newFake(t, fetchResp{Status: 207, Body: "bad:a"}, fetchResp{Status: 200})
	if _, dropped := retryWithoutRejected(buildFor(sent), namesFromBody, sent, f.fetch); len(dropped) != 1 || len(f.reqs) != 2 {
		t.Fatalf("207 not retried: %v", dropped)
	}
}

func TestRecordURLOK(t *testing.T) {
	ok := []string{"https://app.hubspot.com/contacts/1", "https://acme.pipedrive.com/person/3", "https://pipedrive.com/x"}
	for _, u := range ok {
		if !recordURLOK(u, "hubspot.com", "pipedrive.com") {
			t.Errorf("%s refused", u)
		}
	}
	for _, u := range []string{
		"http://app.hubspot.com/x", "https://evilhubspot.com/x", "https://hubspot.com.evil.com/x",
		"https://u@app.hubspot.com/x", "https://app.hubspot.com:8443/x", "javascript:alert(1)", "", "//app.hubspot.com/x",
	} {
		if recordURLOK(u, "hubspot.com", "pipedrive.com") {
			t.Errorf("%s accepted", u)
		}
	}
}

func TestClassifyAndFailure(t *testing.T) {
	for _, tc := range failureCases {
		r, bad := failure(tc.resp)
		if !bad || r.kind != tc.kind {
			t.Errorf("%s: %+v bad=%v", tc.name, r, bad)
		}
		if tc.resp.Status != 0 && r.status != tc.resp.Status {
			t.Errorf("%s: status %d", tc.name, r.status)
		}
	}
	for _, s := range []int{200, 201, 204, 299} {
		if _, bad := failure(fetchResp{Status: s}); bad {
			t.Errorf("%d is a success", s)
		}
	}
	if r := classify(401); r.reason != "unauthorized" {
		t.Fatalf("401: %+v", r)
	}
	if r := classify(404); r.reason != "rejected" {
		t.Fatalf("404: %+v", r)
	}
	if r := classify(300); r.kind != kindFail {
		t.Fatalf("300: %+v", r)
	}
}

func TestJSONReq(t *testing.T) {
	r := jsonReq("POST", "https://example.com", map[string]string{"X-Key": "k"}, map[string]int{"a": 1})
	if r.Body != `{"a":1}` || r.Headers["Content-Type"] != "application/json" || r.Headers["Accept"] != "application/json" || r.Headers["X-Key"] != "k" {
		t.Fatalf("%+v", r)
	}
	if g := jsonReq("GET", "https://example.com", nil, nil); g.Body != "" || g.Headers["Content-Type"] != "" {
		t.Fatalf("%+v", g)
	}
}

func TestDeliverWithoutProviderFails(t *testing.T) {
	if r := deliver(cfg{Provider: "nope"}, job{}, nil); r.kind != kindFail || r.reason != "not configured" {
		t.Fatalf("%+v", r)
	}
}
