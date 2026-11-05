package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func tiktokSettings(t *testing.T, extra string) settings {
	h := newHost(t)
	if extra == "" {
		h.configure()
	} else {
		h.configure(extra)
	}
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func tiktokJob() job {
	return job{SubmissionID: "7f3c1d52-0000-4000-8000-00000000a91e", Platform: platTikTok, EmHash: "emhash", PhHash: "phhash",
		IDs: clickIDs{"ttclid": "TT1"}, PagePath: "/thanks", SubmittedAt: testNow}
}

func TestBuildTikTokGolden(t *testing.T) {
	s := tiktokSettings(t, `"value":12.5,"currency":"EUR","tiktok_test_event_code":"TEST123"`)
	req := buildTikTok(s, tiktokJob(), false)
	if req.Method != "POST" || req.URL != "https://business-api.tiktok.com/open_api/v1.3/event/track/" {
		t.Fatalf("req = %s %s", req.Method, req.URL)
	}
	if req.Headers["Access-Token"] != "tok-secret" || req.Headers["Content-Type"] != "application/json" {
		t.Fatalf("headers = %v", req.Headers)
	}
	const want = `{"data":[{"event":"SubmitForm","event_id":"7f3c1d52-0000-4000-8000-00000000a91e","event_time":1793872800,` +
		`"page":{"url":"https://example.com/thanks"},"properties":{"currency":"EUR","value":12.5},` +
		`"user":{"email":"emhash","phone":"phhash","ttclid":"TT1"}}],"event_source":"web","event_source_id":"CABC123","test_event_code":"TEST123"}`
	if string(req.Body) != want {
		t.Fatalf("body = %s\nwant   %s", req.Body, want)
	}
}

func TestBuildTikTokWithoutClickIDOrOptionals(t *testing.T) {
	s := tiktokSettings(t, `"public_url":""`)
	j := tiktokJob()
	j.IDs = nil
	j.PhHash = ""
	body := string(buildTikTok(s, j, false).Body)
	const want = `{"data":[{"event":"SubmitForm","event_id":"7f3c1d52-0000-4000-8000-00000000a91e","event_time":1793872800,"user":{"email":"emhash"}}],` +
		`"event_source":"web","event_source_id":"CABC123"}`
	if body != want {
		t.Fatalf("body = %s", body)
	}
}

func TestBuildTikTokEventIDIsSubmissionIDAndTokenOnlyInHeader(t *testing.T) {
	s := tiktokSettings(t, `"tiktok_event":"Lead"`)
	req := buildTikTok(s, tiktokJob(), true)
	var b struct {
		Data []struct {
			Event   string `json:"event"`
			EventID string `json:"event_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(req.Body, &b); err != nil || len(b.Data) != 1 || b.Data[0].EventID != tiktokJob().SubmissionID || b.Data[0].Event != "Lead" {
		t.Fatalf("body = %s err %v", req.Body, err)
	}
	if strings.Contains(string(req.Body)+req.URL, "tok-secret") {
		t.Fatal("token leaked outside the header")
	}
}

func TestBuildTikTokTestEventCodeOnlyWhenSet(t *testing.T) {
	var m map[string]any
	_ = json.Unmarshal(buildTikTok(tiktokSettings(t, ``), tiktokJob(), true).Body, &m)
	if _, ok := m["test_event_code"]; ok {
		t.Fatal("test_event_code without a setting")
	}
	_ = json.Unmarshal(buildTikTok(tiktokSettings(t, `"tiktok_test_event_code":" T1 "`), tiktokJob(), false).Body, &m)
	if m["test_event_code"] != "T1" {
		t.Fatalf("test_event_code = %v", m["test_event_code"])
	}
}

func TestHasIdentifierTikTok(t *testing.T) {
	for j, want := range map[string]bool{"email": true, "phone": true, "ttclid": true, "none": false} {
		var in job
		switch j {
		case "email":
			in.EmHash = "h"
		case "phone":
			in.PhHash = "h"
		case "ttclid":
			in.IDs = clickIDs{"ttclid": "T"}
		case "none":
			in.IDs = clickIDs{"gclid": "G", "li_fat_id": "L"}
			in.EmGoogleHash = "h"
		}
		if hasIdentifierTikTok(in) != want {
			t.Errorf("%s: want %v", j, want)
		}
	}
}

func TestClassifyTikTok(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   string
		code   string
	}{
		{200, `{"code":0,"message":"OK"}`, kindSent, "0"},
		{200, `{"code":40001,"message":"no auth"}`, kindAuth, "40001"},
		{200, `{"code":40100,"message":"x"}`, kindAuth, "40100"},
		{200, `{"code":40105,"message":"x"}`, kindAuth, "40105"},
		{200, `{"code":40016,"message":"rate"}`, kindRetry, "40016"},
		{200, `{"code":50000,"message":"system"}`, kindRetry, "50000"},
		{200, `{"code":51002,"message":"system"}`, kindRetry, "51002"},
		{200, `{"code":40002,"message":"bad param"}`, kindFailed, "40002"},
		{200, `{"code":40106,"message":"x"}`, kindFailed, "40106"},
		{200, `not json`, kindFailed, "200"},
		{200, `{"message":"no code"}`, kindFailed, "200"},
		{200, ``, kindFailed, "200"},
		{401, ``, kindAuth, "401"},
		{429, ``, kindRetry, "429"},
		{502, ``, kindRetry, "502"},
		{400, `{"code":0}`, kindFailed, "400"},
		{204, ``, kindFailed, "204"},
	}
	for _, c := range cases {
		o := classifyTikTok(c.status, []byte(c.body))
		if o.Kind != c.kind || o.Code != c.code {
			t.Errorf("classifyTikTok(%d, %s) = %+v, want %s %s", c.status, c.body, o, c.kind, c.code)
		}
	}
}

func TestClassifyTikTokMessageIsCutAndOnlyForFailures(t *testing.T) {
	long := make([]byte, 0, 600)
	for i := 0; i < 600; i++ {
		long = append(long, 'm')
	}
	o := classifyTikTok(200, []byte(`{"code":40002,"message":"`+string(long)+`"}`))
	if len(o.Message) != maxMessage {
		t.Fatalf("message length = %d", len(o.Message))
	}
	if o := classifyTikTok(200, []byte(`{"code":0,"message":"OK"}`)); o.Message != "" {
		t.Fatalf("sent message = %q", o.Message)
	}
}
