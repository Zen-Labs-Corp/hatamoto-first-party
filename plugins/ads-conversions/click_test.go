package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const clickSID = "7f3c1d52-0000-4000-8000-00000000a91e"

func click(body string, ct string) sdk.HTTPRequest {
	return sdk.HTTPRequest{Method: "POST", Pattern: "/click", Body: body, Headers: map[string]string{"content-type": ct}}
}

func TestClickAccepted(t *testing.T) {
	h := newHost(t)
	res, err := postClick(click(`{"submission_id":"`+clickSID+`","form":"contact","ids":{"gclid":"G1","ttclid":"T1","evil":"x","li_fat_id":""}}`, "application/json; charset=utf-8"))
	if err != nil || res.Status != 204 || res.Body != "" {
		t.Fatalf("%d %q %v", res.Status, res.Body, err)
	}
	up := h.execs("INSERT INTO clicks")
	if len(up) != 1 || !strings.Contains(sqlOf(up[0]), "ON CONFLICT (submission_id) DO UPDATE") ||
		argsOf(up[0])[0] != clickSID || argsOf(up[0])[1] != `{"gclid":"G1","ttclid":"T1"}` {
		t.Fatalf("upsert %+v", up)
	}
	if len(h.execs("DELETE FROM clicks WHERE created_at")) != 1 {
		t.Fatal("old clicks must be deleted on each insert")
	}
	if len(h.callsOf("http.fetch"))+len(h.callsOf("mail.send")) != 0 {
		t.Fatal("/click must not call a platform")
	}
}

func TestClickAnswers204WithoutOracle(t *testing.T) {
	h := newHost(t)
	// No ids at all, an unknown submission and a failing database all look the same.
	for _, body := range []string{`{"submission_id":"` + clickSID + `","ids":{}}`, `{"submission_id":"` + clickSID + `"}`} {
		if res, _ := postClick(click(body, "application/json")); res.Status != 204 {
			t.Fatalf("%s: %d", body, res.Status)
		}
	}
	h.failExec = true
	if res, _ := postClick(click(`{"submission_id":"`+clickSID+`","ids":{"gclid":"G1"}}`, "application/json")); res.Status != 204 {
		t.Fatalf("db down: %d", res.Status)
	}
}

func TestClickRejectsMalformedInput(t *testing.T) {
	h := newHost(t)
	big := `{"submission_id":"` + clickSID + `","ids":{"gclid":"` + strings.Repeat("a", 4100) + `"}}`
	cases := map[string]sdk.HTTPRequest{
		"not json type":   click(`{"submission_id":"`+clickSID+`"}`, "text/plain"),
		"no content type": {Method: "POST", Body: `{"submission_id":"` + clickSID + `"}`},
		"not json":        click(`nope`, "application/json"),
		"oversized":       click(big, "application/json"),
		"not a uuid":      click(`{"submission_id":"1234","ids":{"gclid":"G1"}}`, "application/json"),
		"missing id":      click(`{"ids":{"gclid":"G1"}}`, "application/json"),
		"bad form":        click(`{"submission_id":"`+clickSID+`","form":"a b/c","ids":{"gclid":"G1"}}`, "application/json"),
		"bad click id":    click(`{"submission_id":"`+clickSID+`","ids":{"gclid":"a b;c"}}`, "application/json"),
		"long click id":   click(`{"submission_id":"`+clickSID+`","ids":{"gclid":"`+strings.Repeat("a", 257)+`"}}`, "application/json"),
		"non-string id":   click(`{"submission_id":"`+clickSID+`","ids":{"ttclid":5}}`, "application/json"),
		"binary body":     {Method: "POST", BodyB64: "AAEC", Headers: map[string]string{"content-type": "application/json"}},
	}
	for name, req := range cases {
		if res, _ := postClick(req); res.Status != 400 {
			t.Errorf("%s: status %d", name, res.Status)
		}
	}
	if len(h.execs("")) != 0 {
		t.Fatal("rejected input must not write")
	}
}
