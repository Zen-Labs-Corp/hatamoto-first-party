package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func runBuilder(t *testing.T, req sdk.BuilderDataRequest) (sdk.BuilderDataRequest, string) {
	t.Helper()
	in, _ := json.Marshal(req)
	out, err := guest.Handle(sdk.BuilderData.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var got sdk.BuilderDataRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	return got, string(out)
}

// builderHost answers the types query and fails the test on any other call.
func builderHost(t *testing.T, types string, dbErr error) *[]call {
	t.Helper()
	var calls []call
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		if fn == sdk.FnSettingsGet {
			return jsonInto(`{"currency":"EUR"}`, out)
		}
		calls = append(calls, call{fn, sql, ar})
		if fn != sdk.FnDBQuery || !strings.Contains(sql, "FROM ticket_types WHERE event_id") {
			t.Fatalf("unexpected host call %s %s", fn, sql)
		}
		if dbErr != nil {
			return dbErr
		}
		return jsonInto(`{"rows":[[`+types+`]]}`, out)
	}
	return &calls
}

func ticketsQuery(entity string) sdk.BuilderDataRequest {
	return sdk.BuilderDataRequest{EntityID: entity, Requests: []sdk.BuilderDataQuery{
		{ID: "t", Source: sourceTickets, Limit: 48}, {ID: "x", Source: "ext.other.thing"},
	}}
}

func TestBuilderNoEntityNoHostCall(t *testing.T) {
	calls := builderHost(t, `[]`, nil)
	got, _ := runBuilder(t, ticketsQuery(""))
	if len(*calls) != 0 {
		t.Errorf("host calls = %+v", *calls)
	}
	if len(got.Results) != 1 || got.Results[0].ID != "t" || len(got.Results[0].Items) != 0 {
		t.Errorf("results = %+v", got.Results)
	}
}

func TestBuilderItems(t *testing.T) {
	calls := builderHost(t, twoTypes, nil)
	got, _ := runBuilder(t, ticketsQuery("e1"))
	if len(*calls) != 1 || (*calls)[0].args[0] != "e1" {
		t.Errorf("host calls = %+v", *calls)
	}
	if len(got.Results) != 1 {
		t.Fatalf("results = %+v", got.Results)
	}
	items := got.Results[0].Items
	if len(items) != 2 { // the type that is off sale is left out
		t.Fatalf("items = %+v", items)
	}
	std, vip := items[0], items[1]
	if std.Title != "Standard" || len(std.Attrs) != 6 {
		t.Errorf("standard = %+v", std)
	}
	want := map[string]string{"type_id": "5", "event_id": "e1", "price": "Free", "left": "6", "sold_out": "", "waitlist": ""}
	for k, v := range want {
		if got, ok := std.Attrs[k]; !ok || got != v {
			t.Errorf("standard attr %s = %q (set %v), want %q", k, got, ok, v)
		}
	}
	// sold out with a waitlist
	if vip.Title != "VIP" || vip.Attrs["left"] != "0" || vip.Attrs["sold_out"] != "1" || vip.Attrs["waitlist"] != "1" {
		t.Errorf("vip = %+v", vip)
	}
}

func TestBuilderShowsThePrice(t *testing.T) {
	builderHost(t, `[{"id":9,"event_id":"e1","name":"Paid","capacity":3,"taken":0,"waitlist":false,"on_sale":true,"position":0,"price_cents":1250}]`, nil)
	got, _ := runBuilder(t, ticketsQuery("e1"))
	if p := got.Results[0].Items[0].Attrs["price"]; p != "12.50 EUR" {
		t.Errorf("price = %q", p)
	}
}

func TestBuilderSoldOutWithoutWaitlist(t *testing.T) {
	builderHost(t, `[{"id":9,"event_id":"e1","name":"Last","capacity":3,"taken":3,"waitlist":false,"on_sale":true,"position":0}]`, nil)
	got, _ := runBuilder(t, ticketsQuery("e1"))
	a := got.Results[0].Items[0].Attrs
	if a["sold_out"] != "1" || a["waitlist"] != "" {
		t.Errorf("attrs = %+v", a)
	}
}

func TestBuilderLimit(t *testing.T) {
	builderHost(t, twoTypes, nil)
	req := ticketsQuery("e1")
	req.Requests[0].Limit = 1
	got, _ := runBuilder(t, req)
	if len(got.Results[0].Items) != 1 {
		t.Errorf("items = %+v", got.Results[0].Items)
	}
}

// A6: the join link never reaches a builder page, whatever the event holds.
func TestBuilderNeverCarriesOnlineURL(t *testing.T) {
	var host []string
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		host = append(host, fn)
		if fn == sdk.FnEntitiesGet {
			return jsonInto(`{"id":"e1","attributes":{"online_url":"https://meet.example.org/secret"}}`, out)
		}
		if strings.Contains(sql, "ticket_types") {
			return jsonInto(`{"rows":[[`+twoTypes+`]]}`, out)
		}
		return errors.New("unexpected " + sql)
	}
	_, raw := runBuilder(t, ticketsQuery("e1"))
	if strings.Contains(raw, "online_url") || strings.Contains(raw, "meet.example.org") {
		t.Errorf("output carries the join link: %s", raw)
	}
	for _, fn := range host {
		if fn == sdk.FnEntitiesGet {
			t.Error("the source must not read the event")
		}
	}
}

func TestBuilderDBErrorFailsTheFilterSoThePageIsNotCached(t *testing.T) {
	builderHost(t, ``, errors.New("pq: boom secret"))
	out, err := answerBuilder(ticketsQuery("e1"))
	if !errors.Is(err, errTicketsUnavailable) {
		t.Fatalf("err = %v, want errTicketsUnavailable", err)
	}
	if strings.Contains(err.Error(), "boom") {
		t.Errorf("host error text leaked: %v", err)
	}
	// still fail open: the render gets an empty result for its request
	if len(out.Results) != 1 || len(out.Results[0].Items) != 0 || out.Results[0].Error != "" {
		t.Errorf("results = %+v", out.Results)
	}
}
