package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const routeSettings = `{"provider":"hubspot","access_token":"SENTINEL-TOKEN","forms":["contact"]}`

// routeStub answers from fields set by the test and sends one request in
// test and fields, so the route's fetch budget is exercised.
type routeStub struct {
	account string
	testRes result
	crmList []crmField
	fldRes  result
}

func (s *routeStub) upsert(job, cfg, []mapRow, fetcher) result { return result{kind: kindCreated} }
func (s *routeStub) refresh(cfg, fetcher) result               { return result{kind: kindUpdated} }

func (s *routeStub) test(c cfg, f fetcher) (string, result) {
	if resp := f(fetchReq{Method: "GET", URL: "https://api.hubapi.com/x"}); resp.Code != "" {
		return "", hostResult(resp.Code)
	}
	return s.account, s.testRes
}

func (s *routeStub) fields(c cfg, f fetcher) ([]crmField, result) {
	return s.crmList, s.fldRes
}

// routeEnv installs the fake host (plus http.fetch, counted in *fetches) and
// the stub as the hubspot provider.
func routeEnv(t *testing.T, settings string, stub *routeStub) (*fakeHost, *int) {
	t.Helper()
	h := &fakeHost{settings: settings, forms: `{"items":[
		{"slug":"contact","fields":[{"name":"email","label":"Email"},{"name":"phone","label":"Phone"}]},
		{"slug":"other","fields":[{"name":"secret","label":"Secret"}]},
		{"slug":"contact","fields":[{"name":"phone","label":"Phone again"},{"name":"city"}]}]}`}
	h.install(t)
	fetches := new(int)
	inner := callHost
	callHost = func(fn string, a, out any) error {
		if fn == "http.fetch" {
			*fetches++
			return json.Unmarshal([]byte(`{"status":200,"body":"{}"}`), out)
		}
		return inner(fn, a, out)
	}
	old := providers["hubspot"]
	providers["hubspot"] = stub
	t.Cleanup(func() { providers["hubspot"] = old })
	return h, fetches
}

func call(t *testing.T, method, pattern, body string) (int, map[string]any, string) {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: method, Pattern: pattern, Body: body})
	if err != nil {
		t.Fatalf("route returned an error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Body), &m); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	return res.Status, m, res.Body
}

func TestRouteIgnoresBudgetLeftByCron(t *testing.T) {
	_, fetches := routeEnv(t, routeSettings, &routeStub{account: "HubSpot account 1", testRes: result{kind: kindUpdated}})
	budgetEnd = now().Add(time.Second)
	base := now
	now = func() time.Time { return base().Add(10 * time.Second) }
	t.Cleanup(func() { now = base })
	_, m, body := call(t, "POST", "/test", "")
	if m["ok"] != true || *fetches != 1 {
		t.Fatalf("fetches %d body %s", *fetches, body)
	}
	if !budgetEnd.IsZero() {
		t.Fatal("route left budgetEnd set")
	}
}

func TestLogRouteHasNoSecrets(t *testing.T) {
	h, _ := routeEnv(t, routeSettings, &routeStub{})
	h.queryRows = []string{`[{"created_at":"2026-11-04T10:00:00Z","form_slug":"contact","provider":"hubspot","status":"created","record_id":"1","record_url":"https://app.hubspot.com/x","code":201,"message":""}]`}
	code, m, body := call(t, "GET", "/log", "")
	if code != 200 || m["provider"] != "hubspot" || m["object"] != "lead" || m["configured"] != true {
		t.Fatalf("%d %s", code, body)
	}
	if rows, _ := m["rows"].([]any); len(rows) != 1 {
		t.Fatalf("rows %v", m["rows"])
	}
	if strings.Contains(body, "SENTINEL") || strings.Contains(body, "access_token") {
		t.Fatalf("secret in %s", body)
	}
	if sql := h.calls[len(h.calls)-1].sql; !strings.Contains(sql, "LIMIT 100") || !strings.Contains(sql, "record_url") {
		t.Fatalf("sql %s", sql)
	}
}

func TestTestRouteOK(t *testing.T) {
	routeEnv(t, routeSettings, &routeStub{account: "HubSpot account 7", testRes: result{kind: kindUpdated}})
	code, m, _ := call(t, "POST", "/test", "")
	if code != 200 || m["ok"] != true || m["account"] != "HubSpot account 7" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestTestRouteFailure(t *testing.T) {
	cases := []struct {
		res  result
		want string
	}{
		{result{kind: kindFail, status: 400, reason: "rejected"}, "the CRM said 400"},
		{result{kind: kindFail, status: 401, reason: "unauthorized"}, "unauthorized"},
		{result{kind: kindRetry, status: 503}, "the CRM said 503"},
		{result{kind: kindRetry, reason: "timeout"}, "network error"},
		{result{kind: kindFail, reason: "bad instance url"}, "bad instance url"},
		{result{kind: kindFail, reason: "SENTINEL-TEXT"}, "check failed"},
	}
	for _, tc := range cases {
		routeEnv(t, routeSettings, &routeStub{testRes: tc.res})
		code, m, body := call(t, "POST", "/test", "")
		if code != 200 || m["ok"] != false || m["error"] != tc.want || strings.Contains(body, "SENTINEL") {
			t.Errorf("%+v: %d %s", tc.res, code, body)
		}
	}
}

func TestTestRouteNotConfigured(t *testing.T) {
	for _, s := range []string{`{}`, `{"provider":"hubspot"}`} {
		routeEnv(t, s, &routeStub{})
		code, m, _ := call(t, "POST", "/test", "")
		if code != 200 || m["ok"] != false || m["error"] != "not configured" {
			t.Fatalf("%s: %d %v", s, code, m)
		}
	}
}

func TestFieldsRouteFormFields(t *testing.T) {
	routeEnv(t, routeSettings, &routeStub{
		crmList: []crmField{{Name: "firstname", Label: "First name"}, {Name: "company", Label: "Company", Required: true}},
		fldRes:  result{kind: kindUpdated},
	})
	code, m, body := call(t, "GET", "/fields", "")
	if code != 200 || m["error"] != nil {
		t.Fatalf("%d %s", code, body)
	}
	if crm, _ := m["crm_fields"].([]any); len(crm) != 2 {
		t.Fatalf("crm_fields %v", m["crm_fields"])
	}
	want := `[{"label":"city","name":"city"},{"label":"Email","name":"email"},{"label":"Phone","name":"phone"}]`
	got, _ := json.Marshal(m["form_fields"])
	if string(got) != want {
		t.Fatalf("form_fields %s", got)
	}
}

func TestFieldsRouteCRMErrorKeepsFormFields(t *testing.T) {
	routeEnv(t, routeSettings, &routeStub{fldRes: result{kind: kindFail, status: 403}})
	code, m, body := call(t, "GET", "/fields", "")
	if code != 200 || m["error"] != "unauthorized" {
		t.Fatalf("%d %s", code, body)
	}
	if crm, ok := m["crm_fields"].([]any); !ok || len(crm) != 0 {
		t.Fatalf("crm_fields %v", m["crm_fields"])
	}
	if ff, _ := m["form_fields"].([]any); len(ff) != 3 {
		t.Fatalf("form_fields %v", m["form_fields"])
	}
}

func TestFieldsRouteNotConfigured(t *testing.T) {
	routeEnv(t, `{"provider":"hubspot","forms":["contact"]}`, &routeStub{})
	_, m, _ := call(t, "GET", "/fields", "")
	if m["error"] != "not configured" {
		t.Fatalf("%v", m)
	}
	if crm, ok := m["crm_fields"].([]any); !ok || len(crm) != 0 {
		t.Fatalf("crm_fields %v", m["crm_fields"])
	}
}

func TestMapRoutePutAndGet(t *testing.T) {
	routeEnv(t, routeSettings, &routeStub{})
	_, m, _ := call(t, "GET", "/map", "")
	rows, _ := m["rows"].([]any)
	if m["saved"] != false || len(rows) != 4 {
		t.Fatalf("before save %v", m)
	}
	body := `{"rows":[{"form_field":" city ","crm_field":"city"},{"form_field":"phone","crm_field":"phone"}]}`
	code, m, raw := call(t, "PUT", "/map", body)
	if code != 200 || m["ok"] != true || m["saved"] != true {
		t.Fatalf("%d %s", code, raw)
	}
	_, m, _ = call(t, "GET", "/map", "")
	got, _ := json.Marshal(m["rows"])
	if m["saved"] != true || string(got) != `[{"crm_field":"city","form_field":"city"},{"crm_field":"phone","form_field":"phone"}]` {
		t.Fatalf("after save %v", m)
	}
	if def, _ := m["default_rows"].([]any); len(def) != 4 {
		t.Fatalf("default_rows %v", m["default_rows"])
	}
}

func TestFieldsRouteHidesPrivilegedFields(t *testing.T) {
	routeEnv(t, routeSettings, &routeStub{
		crmList: []crmField{{Name: "firstname"}, {Name: "hubspot_owner_id"}, {Name: "OwnerId"}},
		fldRes:  result{kind: kindUpdated},
	})
	_, m, body := call(t, "GET", "/fields", "")
	if crm, _ := m["crm_fields"].([]any); len(crm) != 1 {
		t.Fatalf("crm_fields %s", body)
	}
}

func TestMapRoutePrivilegedFieldIs400(t *testing.T) {
	routeEnv(t, routeSettings, &routeStub{})
	code, m, body := call(t, "PUT", "/map", `{"rows":[{"form_field":"a","crm_field":"hubspot_owner_id"}]}`)
	if code != 400 || !strings.Contains(body, "hubspot_owner_id") {
		t.Fatalf("%d %v", code, m)
	}
}

func TestMapRouteRejectsBadRows(t *testing.T) {
	var many []string
	for i := range 51 {
		many = append(many, fmt.Sprintf(`{"form_field":"f%d","crm_field":"c%d"}`, i, i))
	}
	cases := map[string]string{
		"duplicate":   `{"rows":[{"form_field":"a","crm_field":"x"},{"form_field":"b","crm_field":"x"}]}`,
		"51 rows":     `{"rows":[` + strings.Join(many, ",") + `]}`,
		"empty name":  `{"rows":[{"form_field":"  ","crm_field":"x"}]}`,
		"empty crm":   `{"rows":[{"form_field":"a","crm_field":""}]}`,
		"long name":   `{"rows":[{"form_field":"` + strings.Repeat("a", 101) + `","crm_field":"x"}]}`,
		"bad json":    `{"rows":`,
		"no rows":     `{}`,
		"unknown key": `{"rows":[],"extra":1}`,
		"oversize":    `{"rows":[{"form_field":"` + strings.Repeat("a", 33<<10) + `","crm_field":"x"}]}`,
	}
	for name, body := range cases {
		h, _ := routeEnv(t, routeSettings, &routeStub{})
		code, m, _ := call(t, "PUT", "/map", body)
		if code != 400 || m["error"] == nil || m["error"] == "" {
			t.Errorf("%s: %d %v", name, code, m)
		}
		if _, ok := h.kv["map_hubspot"]; ok {
			t.Errorf("%s: mapping was saved", name)
		}
	}
}

func TestMapPutNeedsProvider(t *testing.T) {
	routeEnv(t, `{}`, &routeStub{})
	code, m, _ := call(t, "PUT", "/map", `{"rows":[{"form_field":"a","crm_field":"b"}]}`)
	if code != 400 || m["error"] != "choose a provider first" {
		t.Fatalf("%d %v", code, m)
	}
	_, m, _ = call(t, "GET", "/map", "")
	if rows, ok := m["rows"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("rows %v", m["rows"])
	}
}

func TestMapPutEmptyResetsToDefault(t *testing.T) {
	h, _ := routeEnv(t, routeSettings, &routeStub{})
	call(t, "PUT", "/map", `{"rows":[{"form_field":"a","crm_field":"b"}]}`)
	if _, ok := h.kv["map_hubspot"]; !ok {
		t.Fatal("not saved")
	}
	code, m, _ := call(t, "PUT", "/map", `{"rows":[]}`)
	if code != 200 || m["ok"] != true || m["saved"] != false {
		t.Fatalf("%d %v", code, m)
	}
	if _, ok := h.kv["map_hubspot"]; ok {
		t.Fatal("mapping not deleted")
	}
	_, m, _ = call(t, "GET", "/map", "")
	if rows, _ := m["rows"].([]any); m["saved"] != false || len(rows) != 4 {
		t.Fatalf("%v", m)
	}
}

func TestUnknownRouteIs404JSON(t *testing.T) {
	routeEnv(t, routeSettings, &routeStub{})
	code, m, _ := call(t, "GET", "/nope", "")
	if code != 404 || m["error"] != "not found" {
		t.Fatalf("%d %v", code, m)
	}
}
