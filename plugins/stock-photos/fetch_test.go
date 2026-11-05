package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// call is one recorded host call, its arguments as JSON.
type call struct {
	Fn   string
	Args string
}

// fakeHost is a recording stand-in for the Hatamoto host, shared by the step
// tests.
type fakeHost struct {
	t        *testing.T
	calls    []call
	info     sdk.RunInfo
	settings map[string]any
	// search answers http.fetch by URL; nil means status 200 with an empty body.
	search func(rawURL string) (fetchResp, error)
	// errs maps a host function (or "media.import_url:<filename>") to an error.
	errs   map[string]error
	buffer map[string]string // key -> value, filled by buffer.put
	// existing lists source keys core already has: attach answers created=false.
	existing map[string]bool
}

func newHost(t *testing.T, phase, params string, hosts ...string) *fakeHost {
	h := &fakeHost{t: t, settings: map[string]any{"pexels_api_key": testKey}, errs: map[string]error{}, buffer: map[string]string{}}
	h.info = sdk.RunInfo{RunID: "r", Name: "import", Phase: phase, Params: json.RawMessage(params), Hosts: hosts}
	callHost = h.call
	return h
}

func (h *fakeHost) call(fn string, args, out any) error {
	raw, _ := json.Marshal(args)
	h.calls = append(h.calls, call{fn, string(raw)})
	key := fn
	if fn == "media.import_url" {
		var a sdk.MediaImportURLArgs
		_ = json.Unmarshal(raw, &a)
		key = fn + ":" + a.Filename
	}
	if err, ok := h.errs[key]; ok {
		return err
	}
	if err, ok := h.errs[fn]; ok {
		return err
	}
	var res any = struct{}{}
	switch fn {
	case "run.info":
		res = h.info
	case "settings.get":
		res = h.settings
	case "http.fetch":
		var r fetchReq
		_ = json.Unmarshal(raw, &r)
		resp := fetchResp{Status: 200, Body: "{}"}
		if h.search != nil {
			var err error
			if resp, err = h.search(r.URL); err != nil {
				return err
			}
		}
		res = resp
	case "media.import_url":
		var a sdk.MediaImportURLArgs
		_ = json.Unmarshal(raw, &a)
		res = sdk.MediaHandle{Handle: "h-" + a.Filename}
	case "changeset.media.attach":
		var a sdk.MediaAttach
		_ = json.Unmarshal(raw, &a)
		res = sdk.UpsertResult{SourceKey: a.SourceKey, Created: !h.existing[a.SourceKey]}
	case "buffer.get":
		var a sdk.BufferGetArgs
		_ = json.Unmarshal(raw, &a)
		v, ok := h.buffer[a.Key]
		res = sdk.BufferValue{Found: ok, ValueB64: base64.StdEncoding.EncodeToString([]byte(v))}
	case "buffer.list":
		var a sdk.BufferListArgs
		_ = json.Unmarshal(raw, &a)
		var keys []string
		for k := range h.buffer {
			if strings.HasPrefix(k, a.Prefix) && k > a.After {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		out := sdk.BufferKeys{Keys: keys}
		if a.Limit > 0 && len(keys) > a.Limit {
			out = sdk.BufferKeys{Keys: keys[:a.Limit], Next: keys[a.Limit-1]}
		}
		res = out
	case "buffer.put":
		var a sdk.BufferPutArgs
		_ = json.Unmarshal(raw, &a)
		v, _ := base64.StdEncoding.DecodeString(a.ValueB64)
		h.buffer[a.Key] = string(v)
	}
	if out != nil {
		b, _ := json.Marshal(res)
		return json.Unmarshal(b, out)
	}
	return nil
}

func (h *fakeHost) names(fn string) []call {
	var out []call
	for _, c := range h.calls {
		if c.Fn == fn {
			out = append(out, c)
		}
	}
	return out
}

// step runs one on_run_step through the SDK entry point.
func (h *fakeHost) step(cursor string) error {
	in, _ := json.Marshal(sdk.RunStep{RunID: "r", Phase: h.info.Phase, Cursor: cursor})
	h.info.Cursor = cursor
	_, err := guest.Handle(sdk.RunStepExport, in)
	return err
}

// ends asserts the step finished exactly one way and returns it.
func (h *fakeHost) ends(want string) call {
	h.t.Helper()
	var ends []call
	for _, c := range h.calls {
		if c.Fn == "run.checkpoint" || c.Fn == "run.finish" || c.Fn == "run.fail" {
			ends = append(ends, c)
		}
	}
	if len(ends) != 1 || ends[0].Fn != want {
		h.t.Fatalf("step ends = %v, want one %s", ends, want)
	}
	return ends[0]
}

func (h *fakeHost) checkpoint() cursor {
	h.t.Helper()
	var a sdk.RunCheckpointArgs
	_ = json.Unmarshal([]byte(h.ends("run.checkpoint").Args), &a)
	var c cursor
	if err := json.Unmarshal([]byte(a.Cursor), &c); err != nil {
		h.t.Fatalf("cursor %q: %v", a.Cursor, err)
	}
	return c
}

func (h *fakeHost) failReason() string {
	h.t.Helper()
	var a sdk.RunFailArgs
	_ = json.Unmarshal([]byte(h.ends("run.fail").Args), &a)
	return a.Reason
}

// pexelsPage builds a Pexels response with n photos with ids first..first+n-1.
func pexelsPage(first, n int, next bool) string {
	var items []string
	for id := first; id < first+n; id++ {
		items = append(items, fmt.Sprintf(`{"id":%d,"url":"https://www.pexels.com/photo/%d/","photographer":"P%d","alt":"alt %d","src":{"large2x":"https://images.pexels.com/photos/%d/x.jpeg"}}`, id, id, id, id, id))
	}
	np := ""
	if next {
		np = "https://api.pexels.com/v1/search?page=2"
	}
	return fmt.Sprintf(`{"next_page":%q,"photos":[%s]}`, np, strings.Join(items, ","))
}

var pexelsHosts = []string{"api.pexels.com", "images.pexels.com"}

func TestFetchFirstStepImportsThreeAndCheckpoints(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","provider":"pexels","count":12}`, pexelsHosts...)
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 30, true)}, nil }
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if got := h.checkpoint(); got.I != 3 || got.N != 3 {
		t.Fatalf("cursor = %+v", got)
	}
	if n := len(h.names("http.fetch")); n != 1 {
		t.Fatalf("http.fetch calls = %d", n)
	}
	if n := len(h.names("media.import_url")); n != 3 {
		t.Fatalf("imports = %d", n)
	}
	var item bufferItem
	if err := json.Unmarshal([]byte(h.buffer["item/pexels:1"]), &item); err != nil {
		t.Fatal(err)
	}
	if item.SourceKey != "pexels:1" || item.Handle != "h-pexels-1.jpeg" || item.Alt != "alt 1" ||
		item.Credit != "Photo by P1 on Pexels (Pexels License) https://www.pexels.com/photo/1/" || item.Caption != "P1 / Pexels" {
		t.Fatalf("item = %+v", item)
	}
	if len(h.names("run.progress")) != 1 || len(h.names("run.finish")) != 0 {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestFetchResumeRequestsPageForIndex(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","provider":"pexels","count":30}`, pexelsHosts...)
	var urls []string
	h.search = func(u string) (fetchResp, error) {
		urls = append(urls, u)
		return fetchResp{Status: 200, Body: pexelsPage(31, 30, true)}, nil
	}
	if err := h.step(`{"i":31,"n":10}`); err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || !strings.Contains(urls[0], "page=2") {
		t.Fatalf("urls = %v", urls)
	}
	if got := h.checkpoint(); got.I != 34 || got.N != 13 {
		t.Fatalf("cursor = %+v", got)
	}
	// raw index 31 is the 2nd photo of page 2 (ids 31..): imports 32, 33, 34
	if _, ok := h.buffer["item/pexels:32"]; !ok {
		t.Fatalf("buffer = %v", h.buffer)
	}
}

func TestFetchShortPageContinuesAtNextPage(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","provider":"openverse","count":30}`, "api.openverse.org", "live.staticflickr.com")
	h.search = func(string) (fetchResp, error) {
		return fetchResp{Status: 200, Body: `{"page":1,"page_count":4,"results":[{"id":"z1","title":"t","creator":"c","url":"https://live.staticflickr.com/a.jpg","license":"by","license_version":"2.0"}]}`}, nil
	}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if got := h.checkpoint(); got.I != openversePageSize || got.N != 1 {
		t.Fatalf("cursor = %+v", got)
	}
}

func TestFetchCountClamp(t *testing.T) {
	for in, want := range map[string]int{`{"query":"q","count":500}`: 30, `{"query":"q","count":0}`: 1, `{"query":"q","count":-4}`: 1, `{"query":"q","count":2.9}`: 2, `{"query":"q"}`: 12, `{"query":"q","count":"x"}`: 12} {
		p, msg := parseParams(json.RawMessage(in))
		if msg != "" || p.Count != want {
			t.Errorf("%s: count=%d msg=%q, want %d", in, p.Count, msg, want)
		}
	}
	if _, msg := parseParams(json.RawMessage(`{"query":"  "}`)); msg != "Enter a search term." {
		t.Errorf("msg = %q", msg)
	}
	if _, msg := parseParams(json.RawMessage(`{"query":"q","provider":"unsplash"}`)); msg != "Unknown provider." {
		t.Errorf("msg = %q", msg)
	}
}

func TestFetchUntickedHostSkippedNotImported(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","count":2}`, "api.pexels.com") // images.pexels.com not ticked
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 5, true)}, nil }
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if n := len(h.names("media.import_url")); n != 0 {
		t.Fatalf("imports = %d", n)
	}
	if !strings.Contains(h.buffer["skip/pexels:1"], "host not ticked: images.pexels.com") {
		t.Fatalf("buffer = %v", h.buffer)
	}
	if got := h.checkpoint(); got.I != 30 || got.N != 0 || len(got.Missing) != 1 || got.Missing[0] != "images.pexels.com" {
		t.Fatalf("cursor = %+v", got)
	}
}

func TestFetchAllHostsUntickedFailsNamingHosts(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","count":2}`, "api.pexels.com")
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 2, false)}, nil }
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if r := h.failReason(); !strings.Contains(r, "images.pexels.com") || !strings.HasPrefix(r, "No usable photos") {
		t.Fatalf("reason = %q", r)
	}
}

func TestFetchImportErrorsTolerated(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","count":2}`, pexelsHosts...)
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 5, true)}, nil }
	h.errs["media.import_url:pexels-1.jpeg"] = &sdk.HostError{Code: sdk.ErrBlocked, Message: "nope"}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.buffer["skip/pexels:1"], "import failed: blocked") {
		t.Fatalf("buffer = %v", h.buffer)
	}
	h.ends("run.finish") // photos 2 and 3 fill the count of 2
	if _, ok := h.buffer["item/pexels:2"]; !ok {
		t.Fatalf("buffer = %v", h.buffer)
	}
}

func TestFetchFinishesWhenCountReached(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","count":2}`, pexelsHosts...)
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 30, true)}, nil }
	if err := h.step(`{"i":1,"n":1}`); err != nil {
		t.Fatal(err)
	}
	h.ends("run.finish")
	if len(h.names("media.import_url")) != 1 {
		t.Fatalf("imports = %d", len(h.names("media.import_url")))
	}
}

func TestFetchFinishesWhenResultsExhausted(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","count":12}`, pexelsHosts...)
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 2, false)}, nil }
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	h.ends("run.finish")
}

func TestFetchStopsAfterFivePages(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","count":30}`, pexelsHosts...)
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 30, true)}, nil }
	if err := h.step(`{"i":148,"n":5}`); err != nil {
		t.Fatal(err)
	}
	h.ends("run.finish")
}

func TestFetchEmptyResultsFail(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"zzz"}`, pexelsHosts...)
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 0, false)}, nil }
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if r := h.failReason(); r != `No photos found for "zzz".` {
		t.Fatalf("reason = %q", r)
	}
}

func TestFetchMissingKeyFailsWithoutNetwork(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn"}`, pexelsHosts...)
	h.settings = map[string]any{}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if h.failReason() != msgKey || len(h.names("http.fetch")) != 0 {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestFetchOpenverseNeedsNoKey(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"lake","provider":"openverse","count":1}`, "api.openverse.org", "live.staticflickr.com")
	h.settings = nil
	h.search = func(string) (fetchResp, error) {
		return fetchResp{Status: 200, Body: `{"page":1,"page_count":1,"results":[{"id":"u1","title":"Lake","creator":"C","url":"https://live.staticflickr.com/a.jpg","foreign_landing_url":"https://www.flickr.com/p/1","license":"by","license_version":"2.0","source":"flickr"}]}`}, nil
	}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	h.ends("run.finish")
	if len(h.names("settings.get")) != 0 || !strings.Contains(h.buffer["item/openverse:u1"], `"source_key":"openverse:u1"`) {
		t.Fatalf("calls = %v buffer = %v", h.calls, h.buffer)
	}
}

func TestFetchKeyOnlyInAuthorizationHeader(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","count":3}`, pexelsHosts...)
	h.search = func(string) (fetchResp, error) { return fetchResp{Status: 200, Body: pexelsPage(1, 30, true)}, nil }
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.calls {
		if c.Fn != "http.fetch" && strings.Contains(c.Args, testKey) {
			t.Errorf("key in %s: %s", c.Fn, c.Args)
		}
	}
	for k, v := range h.buffer {
		if strings.Contains(v, testKey) {
			t.Errorf("key in buffer %s", k)
		}
	}
}

func TestFetchBadCursorFails(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn"}`, pexelsHosts...)
	if err := h.step("{nope"); err != nil {
		t.Fatal(err)
	}
	h.ends("run.fail")
}

func TestFetchStopsImportingWhenBudgetSpent(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"barn","provider":"pexels","count":12}`, pexelsHosts...)
	clock := time.Unix(0, 0)
	now = func() time.Time { return clock }
	defer func() { now = time.Now }()
	h.search = func(string) (fetchResp, error) {
		clock = clock.Add(2500 * time.Millisecond) // slow search
		return fetchResp{Status: 200, Body: pexelsPage(1, 30, true)}, nil
	}
	orig := callHost
	callHost = func(fn string, args, out any) error {
		if fn == "media.import_url" {
			clock = clock.Add(1500 * time.Millisecond)
		}
		return orig(fn, args, out)
	}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	// imports start at 2.5 s and 4.0 s: only the first one is within the limit
	if n := len(h.names("media.import_url")); n != 1 {
		t.Fatalf("imports = %d, want 1", n)
	}
	if got := h.checkpoint(); got.I != 1 || got.N != 1 {
		t.Fatalf("cursor = %+v", got)
	}
}

func TestSearchTimeoutLeavesRoomForImports(t *testing.T) {
	req := fakeFetch(t, fetchResp{Status: 200, Body: `{"photos":[]}`}, nil)
	if _, _, _, err := searchPexels(testKey, searchParams{Query: "x", Page: 1}); err != nil {
		t.Fatal(err)
	}
	if req.TimeoutMS != searchTimeoutMS || searchTimeoutMS > 3000 {
		t.Fatalf("timeout = %d", req.TimeoutMS)
	}
}

func TestFetchForeignHostNotSuggested(t *testing.T) {
	h := newHost(t, "fetch", `{"query":"owl","provider":"openverse","count":2}`, "api.openverse.org")
	h.search = func(string) (fetchResp, error) {
		return fetchResp{Status: 200, Body: `{"page":1,"page_count":1,"results":[{"id":"ea170550-53f4-4162-8cef-390668730821","title":"t","url":"https://inaturalist-open-data.s3.amazonaws.com/a.jpg","license":"by"}]}`}, nil
	}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if r := h.failReason(); strings.Contains(r, "amazonaws") || !strings.Contains(r, "does not support") {
		t.Fatalf("reason = %q", r)
	}
}
