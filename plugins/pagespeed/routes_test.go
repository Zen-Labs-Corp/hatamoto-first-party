package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// routeHost serves the host calls of the routes with canned db answers.
func routeHost(t *testing.T, settings string, answers map[string]string, fetchCalls *int) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		switch fn {
		case "settings.get":
			return jsonInto(settings, out)
		case "entities.list":
			return jsonInto(`{"items":[]}`, out)
		case "log":
			return nil
		case "http.fetch":
			*fetchCalls++
			return jsonInto(`{"status":404,"body":"{}"}`, out)
		case "db.exec":
			return nil
		case "db.query":
			sql := args.(map[string]any)["sql"].(string)
			for frag, cell := range answers {
				if strings.Contains(sql, frag) {
					s, _ := json.Marshal(cell)
					return jsonInto(`{"rows":[[`+string(s)+`]]}`, out)
				}
			}
			if strings.Contains(sql, "'done'") {
				return jsonInto(`{"rows":[["{}"]]}`, out)
			}
			return jsonInto(`{"rows":[["[]"]]}`, out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func call(t *testing.T, method, pattern, body string) sdk.HTTPResponse {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: method, Pattern: pattern, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const routeSettings = `{"api_key":"` + testKey + `","public_url":"https://s.example","strategy":"both","urls":["/a","/b"]}`

func TestOverviewNoFieldDataRow(t *testing.T) {
	n := 0
	routeHost(t, routeSettings, map[string]string{
		"DISTINCT ON (url, strategy)": `[{"url":"/a","day":"2026-11-04","strategy":"mobile","lcp_ms":null,"inp_ms":null,"cls":null,"field_status":"none","lab_score":61,"lab_status":"ok","measured_at":"2026-11-04T06:10:00Z"}]`,
		"'done'":                      `{"done":1,"at":"2026-11-04T06:10:00Z"}`,
	}, &n)
	res := call(t, "GET", "/overview", "")
	if res.Status != 200 || strings.Contains(res.Body, testKey) {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	var out struct {
		Notices []string
		URLs    []map[string]json.RawMessage
		LastRun struct{ Done, Total int } `json:"last_run"`
	}
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		t.Fatal(err)
	}
	var m struct {
		FieldStatus string `json:"field_status"`
		LCP         *value
		LabScore    int `json:"lab_score"`
	}
	_ = json.Unmarshal(out.URLs[0]["mobile"], &m)
	if m.FieldStatus != "none" || m.LCP != nil || m.LabScore != 61 || len(out.Notices) != 0 || out.LastRun.Total != 4 || out.LastRun.Done != 1 {
		t.Fatalf("overview = %s", res.Body)
	}
	var d struct {
		FieldStatus string `json:"field_status"`
	}
	_ = json.Unmarshal(out.URLs[1]["desktop"], &d)
	if d.FieldStatus != "pending" {
		t.Fatalf("untouched page = %s", out.URLs[1]["desktop"])
	}
}

func TestOverviewNotices(t *testing.T) {
	n := 0
	routeHost(t, `{"urls":["/a"]}`, nil, &n)
	res := call(t, "GET", "/overview", "")
	for _, want := range []string{`"no_api_key"`, `"no_site_address"`} {
		if !strings.Contains(res.Body, want) {
			t.Fatalf("missing %s in %s", want, res.Body)
		}
	}
	routeHost(t, `{"api_key":"k","public_url":"https://s.example","urls":["/a"]}`, nil, &n)
	if res := call(t, "GET", "/overview", ""); !strings.Contains(res.Body, `"no_data"`) {
		t.Fatalf("body = %s", res.Body)
	}
}

func TestMeasureNowRejectsBadInput(t *testing.T) {
	n := 0
	routeHost(t, routeSettings, nil, &n)
	for name, body := range map[string]string{
		"untracked": `{"url":"/zzz"}`,
		"unknown":   `{"url":"/a","x":1}`,
		"no slash":  `{"url":"a"}`,
		"not json":  `nope`,
		"trailing":  `{"url":"/a"} {}`,
		"empty":     ``,
		"huge":      `{"url":"/` + strings.Repeat("a", 5000) + `"}`,
	} {
		res := call(t, "POST", "/measure-now", body)
		if res.Status < 400 || res.Status >= 500 {
			t.Errorf("%s: status %d", name, res.Status)
		}
	}
	if n != 0 {
		t.Fatalf("%d fetches for rejected input", n)
	}
}

func TestMeasureNowAnswersAndHidesKey(t *testing.T) {
	n := 0
	routeHost(t, routeSettings, map[string]string{
		"WHERE day = $1::date AND url = $2": `[{"url":"/a","day":"2026-11-04","strategy":"mobile","field_status":"none","lab_status":"pending"}]`,
	}, &n)
	res := call(t, "POST", "/measure-now", `{"url":"/a"}`)
	if res.Status != 200 || n < 2 || strings.Contains(res.Body, testKey) {
		t.Fatalf("%d %s (fetches %d)", res.Status, res.Body, n)
	}
	routeHost(t, `{"public_url":"https://s.example","urls":["/a"]}`, nil, &n)
	if res := call(t, "POST", "/measure-now", `{"url":"/a"}`); res.Status != http.StatusConflict {
		t.Fatalf("no key: %d", res.Status)
	}
}

func TestUnknownRoute(t *testing.T) {
	n := 0
	routeHost(t, routeSettings, nil, &n)
	if res := call(t, "GET", "/nope", ""); res.Status != 404 {
		t.Fatalf("status %d", res.Status)
	}
}

func TestOverviewAnswersWhenEntityListFails(t *testing.T) {
	n := 0
	routeHost(t, `{"api_key":"k","public_url":"https://s.example"}`, nil, &n)
	inner := callHost
	callHost = func(fn string, args, out any) error {
		if fn == "entities.list" {
			return &sdk.HostError{Code: sdk.ErrInvalidArgument, Message: "result over 2097152 bytes; ask for less"}
		}
		return inner(fn, args, out)
	}
	res := call(t, "GET", "/overview", "")
	if res.Status != 200 || !strings.Contains(res.Body, `"entities_unavailable"`) || !strings.Contains(res.Body, `"path":"/"`) {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
}

func TestOverviewShowsNewestLabScoreWithItsDay(t *testing.T) {
	n := 0
	routeHost(t, `{"api_key":"k","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`, map[string]string{
		"DISTINCT ON (url, strategy)": `[{"url":"/a","day":"2026-11-04","strategy":"mobile","field_status":"ok","lab_status":"pending"}]`,
		"DISTINCT ON (strategy, url)": `[{"url":"/a","day":"2026-11-02","strategy":"mobile","lab_score":88,"lab_status":"ok"}]`,
	}, &n)
	res := call(t, "GET", "/overview", "")
	var out struct{ URLs []map[string]json.RawMessage }
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Day       string `json:"day"`
		LabScore  int    `json:"lab_score"`
		LabStatus string `json:"lab_status"`
		LabDay    string `json:"lab_day"`
	}
	_ = json.Unmarshal(out.URLs[0]["mobile"], &m)
	if m.LabScore != 88 || m.LabStatus != "ok" || m.LabDay != "2026-11-02" || m.Day != "2026-11-04" {
		t.Fatalf("overview = %s", res.Body)
	}
}
