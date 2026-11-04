package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func getOverview(t *testing.T, query map[string][]string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "GET", Pattern: "/overview", Query: query})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func fixedNow(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = time.Now })
}

func TestOverviewMergesCountsAndSeries(t *testing.T) {
	fixedNow(t)
	cell := `{"all":1284,"last30":212,"files":{"` + idA + `":{"total":903,"days":{"2026-11-04":5,"2026-10-06":2,"2026-10-05":9}}}}`
	f := libraryHost(t, twoFiles, cell)
	res := getOverview(t, map[string][]string{"offset": {"50"}})
	if res.Status != 200 || res.Headers["Content-Type"] != "application/json" {
		t.Fatalf("res %+v", res)
	}
	var o overview
	if err := json.Unmarshal([]byte(res.Body), &o); err != nil {
		t.Fatal(err)
	}
	if o.Totals.All != 1284 || o.Totals.Last30 != 212 || o.Offset != 50 || o.Limit != 50 || o.Total != 2 || len(o.Files) != 2 {
		t.Fatalf("overview %+v", o)
	}
	a, b := o.Files[0], o.Files[1]
	if a.ID != idA || a.Filename != "brochure.pdf" || a.SizeLabel != "88 KB" || a.Total != 903 || a.Last30 != 7 {
		t.Fatalf("file a %+v", a)
	}
	// Window is 2026-10-06 .. 2026-11-04: 05 Oct is outside, 06 Oct first, 04 Nov last.
	if len(a.Series) != 30 || a.Series[0] != 2 || a.Series[29] != 5 {
		t.Fatalf("series %v", a.Series)
	}
	if b.Total != 0 || b.Last30 != 0 || len(b.Series) != 30 {
		t.Fatalf("file b (no counts) %+v", b)
	}
	list := f.calls[0].raw.(map[string]any)
	if list["limit"] != pageSize || list["offset"] != 50 {
		t.Fatalf("media.list args %v", list)
	}
	q := f.calls[1]
	if q.args[0] != idA+","+idB || q.args[1] != "2026-10-06" || q.args[2] != "2026-11-04" {
		t.Fatalf("query args %v", q.args)
	}
}

func TestOverviewOffsetClamp(t *testing.T) {
	fixedNow(t)
	for in, want := range map[string]int{"": 0, "-5": 0, "abc": 0, "7": 7, "99999999999": maxOffset, "5000000": maxOffset} {
		f := libraryHost(t, `[]`, `{"all":0,"last30":0,"files":{}}`)
		res := getOverview(t, map[string][]string{"offset": {in}})
		if res.Status != 200 {
			t.Fatalf("offset %q: %+v", in, res)
		}
		if got := f.calls[0].raw.(map[string]any)["offset"]; got != want {
			t.Errorf("offset %q -> %v, want %d", in, got, want)
		}
	}
}

func TestOverviewEmptyLibrary(t *testing.T) {
	fixedNow(t)
	libraryHost(t, `[]`, `{"all":0,"last30":0,"files":{}}`)
	res := getOverview(t, nil)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.Body), &raw); err != nil || string(raw["files"]) != "[]" {
		t.Fatalf("body %s", res.Body)
	}
}

func TestOverviewErrors(t *testing.T) {
	fixedNow(t)
	for _, fn := range []string{"media.list", "db.query"} {
		f := libraryHost(t, twoFiles, `{}`)
		f.reply[fn] = func(any, any) error { return errors.New("pq: secret") }
		res := getOverview(t, nil)
		if res.Status != 503 || res.Body != `{"error":"unavailable"}` {
			t.Errorf("%s: %+v", fn, res)
		}
	}
}
