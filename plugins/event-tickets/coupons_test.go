package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestCleanCouponCode(t *testing.T) {
	for in, want := range map[string]string{"spring-10": "SPRING-10", "  ab_c ": "AB_C"} {
		if got, ok := cleanCouponCode(in); !ok || got != want {
			t.Errorf("cleanCouponCode(%q) = %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "ab", strings.Repeat("A", 33), "A B C", "A'B", "é-code", "a;drop"} {
		if _, ok := cleanCouponCode(in); ok {
			t.Errorf("%q must be refused", in)
		}
	}
}

func couponHost(t *testing.T, answer string) *[]call {
	t.Helper()
	var calls []call
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		if fn != sdk.FnDBQuery {
			t.Fatalf("unexpected host call %s", fn)
		}
		calls = append(calls, call{fn, sql, ar})
		return jsonInto(`{"rows":[[`+answer+`]]}`, out)
	}
	return &calls
}

func TestLookupCoupon(t *testing.T) {
	calls := couponHost(t, `{"id":4,"code":"SPRING","kind":"percent","value":10,"event_id":"","type_id":0}`)
	c, err := lookupCoupon(" spring ", "e1")
	if err != nil || c == nil || c.ID != 4 || c.Kind != "percent" || c.Value != 10 {
		t.Fatalf("%+v %v", c, err)
	}
	if len(*calls) != 1 {
		t.Fatalf("%d calls, want one", len(*calls))
	}
	sql := (*calls)[0].sql
	for _, want := range []string{"active", "expires_at > now()", "used < max_uses", "event_id IS NULL OR event_id = $2"} {
		if !strings.Contains(sql, want) {
			t.Errorf("the lookup must check %q", want)
		}
	}
	if got := jsonText((*calls)[0].args); got != `["SPRING","e1"]` {
		t.Errorf("args %s (the code is upper-cased, input travels as arguments)", got)
	}
}

func TestLookupCouponNone(t *testing.T) {
	couponHost(t, `null`)
	if c, err := lookupCoupon("NOPE", "e1"); c != nil || err != nil {
		t.Errorf("%+v %v", c, err)
	}
	// a malformed code never reaches the database
	calls := couponHost(t, `null`)
	if c, err := lookupCoupon("a b", "e1"); c != nil || err != nil || len(*calls) != 0 {
		t.Errorf("%+v %v %d", c, err, len(*calls))
	}
}

func TestListBundles(t *testing.T) {
	calls := couponHost(t, `[{"id":1,"type_ids":[1,2],"all_types":true,"min_qty":2,"kind":"fixed","value":500}]`)
	rules, err := listBundles("e1")
	if err != nil || len(rules) != 1 || len(*calls) != 1 {
		t.Fatalf("%+v %v", rules, err)
	}
	r := rules[0]
	if !r.AllTypes || r.MinQty != 2 || r.Kind != "fixed" || r.Value != 500 || len(r.TypeIDs) != 2 || r.TypeIDs[1] != 2 {
		t.Errorf("%+v", r)
	}
}
