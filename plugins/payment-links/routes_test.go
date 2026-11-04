package main

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func TestCSVCellNeutralisesFormulas(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+x": "'+x", "-x": "'-x", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx", "\rx": "'\rx",
		"Donate": "Donate", "": "", "a=b": "a=b",
	} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPaymentsCSVHasHeaderAndEscapesCells(t *testing.T) {
	out, err := paymentsCSV([]payment{{
		EventID: "evt_1", SessionID: "cs_1", Amount: 5000, Currency: "AUD", Email: "=cmd@example.com",
		Label: `Say "hi", friend`, Kind: "donation", Live: true, Created: mustTime("2026-11-03T10:00:00Z"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil || len(recs) != 2 {
		t.Fatalf("%v %v", recs, err)
	}
	if strings.Join(recs[0], ",") != "date,label,kind,amount,currency,email,session_id,event_id,livemode" {
		t.Fatalf("header %v", recs[0])
	}
	want := []string{"2026-11-03T10:00:00Z", `Say "hi", friend`, "donation", "50.00", "AUD", "'=cmd@example.com", "cs_1", "evt_1", "true"}
	if strings.Join(recs[1], "|") != strings.Join(want, "|") {
		t.Fatalf("row %v", recs[1])
	}
}

func TestHandleCSVSendsADownload(t *testing.T) {
	dbHost(t, nil, `[{"event_id":"evt_1","session_id":"cs_1","amount":100,"currency":"USD","label":"L","kind":"payment","created":"2026-11-03T10:00:00Z"}]`)
	res, err := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/payments.csv"})
	if err != nil || res.Status != 200 {
		t.Fatalf("%d %v", res.Status, err)
	}
	if res.Headers["Content-Type"] != "text/csv; charset=utf-8" || res.Headers["Content-Disposition"] != `attachment; filename="payments.csv"` {
		t.Fatalf("headers %v", res.Headers)
	}
	if !strings.HasPrefix(res.Body, "date,label,kind,") || !strings.Contains(res.Body, "1.00,USD") {
		t.Fatalf("body %q", res.Body)
	}
}

func TestHandlePaymentsReturnsPageAndTotalsPerCurrency(t *testing.T) {
	callHost = func(fn string, a, out any) error {
		sql := a.(map[string]any)["sql"].(string)
		cell := `[{"event_id":"evt_1","amount":5000,"currency":"AUD","label":"Donate","kind":"donation","created":"2026-11-03T10:00:00Z"}]`
		if sql == sqlMonthTotals {
			cell = `[{"month":"2026-11","currency":"AUD","total":125000,"count":14},{"month":"2026-11","currency":"USD","total":9000,"count":2}]`
		}
		return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
	}
	res, err := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/payments", Query: map[string][]string{"page": {"2"}}})
	if err != nil || res.Status != 200 {
		t.Fatalf("%d %v", res.Status, err)
	}
	var got struct {
		Payments []struct{ Display string }
		Page     int
		HasMore  bool `json:"has_more"`
		Totals   []struct {
			Currency, Display string
			Count             int
		}
	}
	if err := json.Unmarshal([]byte(res.Body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Page != 2 || got.HasMore || got.Payments[0].Display != "AUD 50.00" {
		t.Fatalf("%+v", got)
	}
	if len(got.Totals) != 2 || got.Totals[0].Display != "AUD 1,250.00" || got.Totals[1].Display != "USD 90.00" || got.Totals[1].Count != 2 {
		t.Fatalf("totals %+v", got.Totals)
	}
}

func TestHandlePaymentsPageParameter(t *testing.T) {
	calls := dbHost(t, nil, `[]`)
	for q, wantOffset := range map[string]int{"": 0, "abc": 0, "0": 0, "-3": 0, "3": 2 * pageSize, "99999999": (maxPage - 1) * pageSize} {
		*calls = nil
		serve(sdk.HTTPRequest{Method: "GET", Pattern: "/payments", Query: map[string][]string{"page": {q}}})
		if got := (*calls)[0].args[1]; got != wantOffset {
			t.Errorf("page %q: offset %v, want %d", q, got, wantOffset)
		}
	}
}

func TestAdminRoutesAnswer503WhenTheDatabaseFails(t *testing.T) {
	callHost = func(string, any, any) error { return &sdk.HostError{Code: sdk.ErrUnavailable} }
	for _, p := range []string{"/payments", "/payments.csv", "/links"} {
		res, err := serve(sdk.HTTPRequest{Method: "GET", Pattern: p})
		if err != nil || res.Status != 503 || res.Body != "" && !strings.Contains(res.Body, "unavailable") {
			t.Errorf("%s: %d %q %v", p, res.Status, res.Body, err)
		}
	}
}

func TestLinksStatusNeverCarriesTheKey(t *testing.T) {
	w := newWorld(t)
	res, err := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/links"})
	if err != nil || res.Status != 200 {
		t.Fatalf("%d %v", res.Status, err)
	}
	if strings.Contains(res.Body, testKey) || strings.Contains(res.Body, "secret") {
		t.Fatalf("key in body: %s", res.Body)
	}
	var got struct {
		KeySet         bool   `json:"key_set"`
		WebhookPath    string `json:"webhook_path"`
		Links          []link
		Notify         bool
		NotifyEmailSet bool `json:"notify_email_set"`
	}
	if err := json.Unmarshal([]byte(res.Body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.KeySet || got.WebhookPath != "/p/payment-links/webhook" || len(got.Links) != 1 || got.Links[0].Label != "Donate" || !got.Notify || !got.NotifyEmailSet {
		t.Fatalf("%+v", got)
	}
	w.settings = `{}`
	res, _ = serve(sdk.HTTPRequest{Method: "GET", Pattern: "/links"})
	if !strings.Contains(res.Body, `"key_set":false`) || !strings.Contains(res.Body, `"links":[]`) {
		t.Fatalf("empty settings: %s", res.Body)
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
