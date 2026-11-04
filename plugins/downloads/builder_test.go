package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	idA = "11111111-1111-4111-8111-111111111111"
	idB = "22222222-2222-4222-8222-222222222222"
)

func runBuilder(t *testing.T, reqs ...sdk.BuilderDataQuery) sdk.BuilderDataRequest {
	t.Helper()
	in, _ := json.Marshal(sdk.BuilderDataRequest{Path: "/", Requests: reqs})
	out, err := guest.Handle(sdk.BuilderData.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var res sdk.BuilderDataRequest
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// libraryHost serves media.list with the given items JSON and totals JSON.
func libraryHost(t *testing.T, items, totalsCell string) *fakeHost {
	f := &fakeHost{reply: map[string]func(any, any) error{
		"media.list": func(_, out any) error { return decode(t, `{"items":`+items+`,"total":2}`, out) },
		"db.query": func(_, out any) error {
			return decode(t, `{"rows":[[`+totalsCell+`]]}`, out)
		},
	}}
	f.install(t)
	return f
}

const twoFiles = `[{"id":"` + idA + `","filename":"brochure.pdf","mime_type":"application/pdf","file_size":90112,"public_url":"/media/a"},
{"id":"` + idB + `","filename":"photo.jpg","mime_type":"image/jpeg","file_size":2516582,"public_url":"/media/b"}]`

func TestBuilderOtherSourceIgnored(t *testing.T) {
	f := libraryHost(t, twoFiles, `{}`)
	res := runBuilder(t, sdk.BuilderDataQuery{ID: "x", Source: "ext.other.files", Limit: 5})
	if len(res.Results) != 0 || len(f.calls) != 0 {
		t.Fatalf("res %+v calls %+v", res, f.calls)
	}
}

func TestBuilderFilesItems(t *testing.T) {
	f := libraryHost(t, twoFiles, `{"`+idA+`":903}`)
	res := runBuilder(t,
		sdk.BuilderDataQuery{ID: "a", Source: filesSource, Limit: 1},
		sdk.BuilderDataQuery{ID: "b", Source: filesSource, Limit: 48})
	if len(res.Results) != 2 || len(res.Results[0].Items) != 1 || len(res.Results[1].Items) != 2 {
		t.Fatalf("res %+v", res)
	}
	it := res.Results[1].Items[0]
	want := map[string]string{"count": "903", "size": "90112", "size_label": "88 KB", "mime_type": "application/pdf"}
	if it.Title != "brochure.pdf" || it.URL != "/p/downloads/f/"+idA || len(it.Attrs) != len(want) {
		t.Fatalf("item %+v", it)
	}
	for k, v := range want {
		if it.Attrs[k] != v {
			t.Errorf("attr %s = %q, want %q", k, it.Attrs[k], v)
		}
	}
	if got := res.Results[1].Items[1].Attrs; got["count"] != "0" || got["size_label"] != "2.4 MB" {
		t.Fatalf("second item attrs %v", got)
	}
	if f.count("media.list") != 1 || f.count("db.query") != 1 {
		t.Fatalf("calls %+v", f.calls)
	}
}

func TestBuilderLimitClamp(t *testing.T) {
	for in, want := range map[int]float64{0: 1, -3: 1, 1: 1, 99: 48} {
		f := libraryHost(t, `[]`, `{}`)
		runBuilder(t, sdk.BuilderDataQuery{ID: "a", Source: filesSource, Limit: in})
		got := f.calls[0].raw.(map[string]any)["limit"]
		if fmt.Sprint(got) != fmt.Sprint(int(want)) {
			t.Errorf("limit %d -> %v, want %v", in, got, want)
		}
	}
}

func TestBuilderHostErrorIsGeneric(t *testing.T) {
	f := libraryHost(t, twoFiles, `{}`)
	f.reply["media.list"] = func(any, any) error { return errors.New("pq: secret detail") }
	res := runBuilder(t, sdk.BuilderDataQuery{ID: "a", Source: filesSource, Limit: 5})
	if len(res.Results) != 1 || res.Results[0].Error != "downloads unavailable" || len(res.Results[0].Items) != 0 {
		t.Fatalf("res %+v", res)
	}
	f = libraryHost(t, twoFiles, `{}`)
	f.reply["db.query"] = func(any, any) error { return errors.New("pq: secret detail") }
	res = runBuilder(t, sdk.BuilderDataQuery{ID: "a", Source: filesSource, Limit: 5})
	if len(res.Results) != 1 || res.Results[0].Error != "downloads unavailable" || strings.Contains(res.Results[0].Error, "secret") {
		t.Fatalf("db error: %+v", res)
	}
}

func TestSizeLabelAndTrim(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 512: "512 B", 1024: "1 KB", 4096: "4 KB", 90112: "88 KB", 1536: "1.5 KB",
		2516582: "2.4 MB", 1 << 30: "1 GB", 5 << 40: "5120 GB"} {
		if got := sizeLabel(n); got != want {
			t.Errorf("sizeLabel(%d) = %q, want %q", n, got, want)
		}
	}
	long := strings.Repeat("é", 2000) // 4000 bytes
	if got := trim(long); len(got) > 2048 || !strings.HasPrefix(long, got) || len(got)%2 != 0 {
		t.Fatalf("trim = %d bytes", len(got))
	}
}

func runCount(t *testing.T, args string) string {
	t.Helper()
	out, err := guest.Handle(guest.FuncExport("count"), []byte(args))
	if err != nil {
		t.Fatalf("func returned an error: %v", err)
	}
	return string(out)
}

func TestCountFunc(t *testing.T) {
	f := libraryHost(t, `[]`, `{"`+idA+`":903}`)
	if got := runCount(t, `["`+strings.ToUpper(idA)+`"]`); got != "903" {
		t.Fatalf("known: %q", got)
	}
	if f.calls[0].sql != qTotals || f.calls[0].args[0] != idA {
		t.Fatalf("call %+v", f.calls[0])
	}
	if got := runCount(t, `["`+idB+`"]`); got != "0" {
		t.Fatalf("unknown: %q", got)
	}
	n := len(f.calls)
	for _, bad := range []string{`[]`, `[12]`, `["nope"]`, `["` + idA + `","x"]`, `[null]`} {
		if got := runCount(t, bad); got != "0" {
			t.Errorf("%s: %q", bad, got)
		}
	}
	if len(f.calls) != n {
		t.Fatal("malformed input reached the database")
	}
	f.reply["db.query"] = func(any, any) error { return errors.New("down") }
	if got := runCount(t, `["`+idA+`"]`); got != "0" {
		t.Fatalf("db error: %q", got)
	}
}

func TestTotalsSumsDailyAndMonthly(t *testing.T) {
	if !strings.Contains(qTotals, "downloads_daily") || !strings.Contains(qTotals, "downloads_monthly") || !strings.Contains(qTotals, "UNION ALL") {
		t.Fatal("totals query must add daily and monthly rows")
	}
}
