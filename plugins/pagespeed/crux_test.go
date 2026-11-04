package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const testKey = "SECRET-key/1+x"

// fakeFetch serves http.fetch with fn and records the requests.
func fakeFetch(t *testing.T, reqs *[]map[string]any, fn func(args map[string]any) (fetched, error)) {
	t.Helper()
	budgetEnd = time.Time{}
	callHost = func(name string, args, out any) error {
		if name != "http.fetch" {
			t.Fatalf("unexpected host call %q", name)
		}
		m := args.(map[string]any)
		if reqs != nil {
			*reqs = append(*reqs, m)
		}
		res, err := fn(m)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(res)
		return json.Unmarshal(b, out)
	}
}

func TestCruxNotFoundIsNoData(t *testing.T) {
	fakeFetch(t, nil, func(map[string]any) (fetched, error) {
		return fetched{Status: 404, Body: `{"error":{"status":"NOT_FOUND"}}`}, nil
	})
	f, err := queryCrux(testKey, "https://s.example/x", "PHONE", false)
	if err != nil || f.Status != fieldNone || f.LCP != nil {
		t.Fatalf("got %+v, %v", f, err)
	}
}

func TestCruxParsesNumbersAndStrings(t *testing.T) {
	var reqs []map[string]any
	fakeFetch(t, &reqs, func(map[string]any) (fetched, error) {
		return fetched{Status: 200, Body: `{"record":{"metrics":{
			"largest_contentful_paint":{"percentiles":{"p75":2300}},
			"interaction_to_next_paint":{"percentiles":{"p75":180}},
			"cumulative_layout_shift":{"percentiles":{"p75":"0.14"}}}}}`}, nil
	})
	f, err := queryCrux(testKey, "https://s.example", "DESKTOP", true)
	if err != nil || f.Status != fieldOK || *f.LCP != 2300 || *f.INP != 180 || *f.CLS != 0.14 {
		t.Fatalf("got %+v, %v", f, err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(reqs[0]["body"].(string)), &body)
	if body["origin"] != "https://s.example" || body["formFactor"] != "DESKTOP" || body["url"] != nil {
		t.Fatalf("body = %v", body)
	}
}

func TestCruxErrorsHideTheKey(t *testing.T) {
	fakeFetch(t, nil, func(map[string]any) (fetched, error) { return fetched{Status: 403, Body: "key " + testKey}, nil })
	_, err := queryCrux(testKey, "https://s.example/x", "PHONE", false)
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
	fakeFetch(t, nil, func(map[string]any) (fetched, error) {
		return fetched{}, errors.New(`Post "https://x/?key=` + "SECRET-key%2F1%2Bx" + `": dial failed ` + testKey)
	})
	_, err = queryCrux(testKey, "https://s.example/x", "PHONE", false)
	if err == nil || strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}
