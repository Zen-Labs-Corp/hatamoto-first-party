package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// hostCall is one recorded host call.
type hostCall struct {
	Fn   string
	Args string
}

type visionReq struct {
	URL     string
	Headers map[string]string
	Body    string
}

// fakeHost is an in-memory host for a suggest run: settings, the media
// library, the run API with its phases, the buffer and the changeset.
type fakeHost struct {
	t        *testing.T
	settings map[string]any
	media    []map[string]any
	// listFilter drops items from a page after paging, as core does for
	// media no public page uses.
	listFilter func(m map[string]any) bool
	// vision answers http.fetch; the default is a short description.
	vision func(req visionReq) (fetched, error)
	// stageErr fails changeset.media.alt for a media id.
	stageErr map[string]error

	phase    string
	cursor   string
	buf      map[string][]byte
	calls    []hostCall
	requests []visionReq
	progress []string
	staged   []sdk.MediaAlt
	summary  json.RawMessage
	done     bool
	failed   string
	steps    int
}

func newFake(t *testing.T) *fakeHost {
	t.Helper()
	f := &fakeHost{
		t:        t,
		settings: map[string]any{"provider": "openai", "api_key": testKey, "site_url": "https://example.com/"},
		buf:      map[string][]byte{},
		stageErr: map[string]error{},
		phase:    sdk.RunPhaseFetch,
	}
	f.vision = func(visionReq) (fetched, error) { return answer("A calm lake at dawn"), nil }
	callHost = f.call
	t.Cleanup(func() { callHost = nil })
	return f
}

// answer is an OpenAI success response with text.
func answer(text string) fetched {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}}}})
	return fetched{Status: 200, Body: string(b)}
}

// img is a media item; alt "" leaves alt_text out, as core does.
func img(id, alt string) map[string]any {
	m := map[string]any{"id": id, "filename": id + ".jpg", "mime_type": "image/jpeg", "public_url": "/media/" + id + ".jpg"}
	if alt != "" {
		m["alt_text"] = alt
	}
	return m
}

func (f *fakeHost) denied() error {
	return &sdk.HostError{Code: sdk.ErrPhaseDenied, Message: "not in this phase"}
}

func (f *fakeHost) call(fn string, args, out any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		f.t.Fatalf("args of %s: %v", fn, err)
	}
	f.calls = append(f.calls, hostCall{fn, string(raw)})
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	str := func(k string) string { s, _ := m[k].(string); return s }
	num := func(k string) int { n, _ := m[k].(float64); return int(n) }
	inFetch := f.phase == sdk.RunPhaseFetch

	switch fn {
	case "settings.get":
		return roundTrip(f.settings, out)
	case "run.info":
		return roundTrip(sdk.RunInfo{RunID: "r", Name: "suggest", Phase: f.phase, Cursor: f.cursor}, out)
	case "run.checkpoint":
		f.cursor = str("cursor")
		if len(f.cursor) > 4096 {
			f.t.Fatalf("cursor of %d bytes", len(f.cursor))
		}
	case "run.progress":
		if len(str("message")) > 512 {
			f.t.Fatalf("progress message of %d bytes", len(str("message")))
		}
		f.progress = append(f.progress, str("message"))
	case "run.finish":
		f.summary, _ = json.Marshal(m["summary"])
		if inFetch {
			f.phase, f.cursor = sdk.RunPhaseTransform, ""
		} else {
			f.done = true
		}
	case "run.fail":
		f.failed = str("reason")
	case "media.list":
		return f.list(m, out)
	case "http.fetch":
		if !inFetch {
			return f.denied()
		}
		var req visionReq
		req.URL, req.Body = str("url"), str("body")
		req.Headers = map[string]string{}
		for k, v := range m["headers"].(map[string]any) {
			req.Headers[k] = v.(string)
		}
		f.requests = append(f.requests, req)
		res, err := f.vision(req)
		if err != nil {
			return err
		}
		return roundTrip(res, out)
	case "buffer.put":
		if !inFetch {
			return f.denied()
		}
		v, err := base64.StdEncoding.DecodeString(str("value_b64"))
		if err != nil {
			f.t.Fatalf("buffer.put value: %v", err)
		}
		f.buf[str("key")] = v
	case "buffer.get":
		if inFetch {
			return f.denied()
		}
		v, ok := f.buf[str("key")]
		return roundTrip(sdk.BufferValue{Found: ok, ValueB64: base64.StdEncoding.EncodeToString(v)}, out)
	case "buffer.list":
		if inFetch {
			return f.denied()
		}
		return roundTrip(f.bufferList(str("prefix"), str("after"), num("limit")), out)
	case "changeset.media.alt":
		if inFetch {
			return f.denied()
		}
		var in sdk.MediaAlt
		_ = json.Unmarshal(raw, &in)
		if err := f.stageErr[in.MediaID]; err != nil {
			return err
		}
		f.staged = append(f.staged, in)
		return roundTrip(sdk.UpsertResult{SourceKey: "k", Created: true}, out)
	default:
		f.t.Fatalf("unexpected host call %q", fn)
	}
	return nil
}

func (f *fakeHost) list(m map[string]any, out any) error {
	off, _ := m["offset"].(float64)
	lim, _ := m["limit"].(float64)
	if lim == 0 {
		lim = 50
	}
	if p, _ := m["mime_type"].(string); p == "" {
		f.t.Fatal("media.list without mime_type")
	}
	var items []map[string]any
	for i := int(off); i < len(f.media) && i < int(off+lim); i++ {
		if f.listFilter == nil || f.listFilter(f.media[i]) {
			items = append(items, f.media[i])
		}
	}
	return roundTrip(map[string]any{"items": items, "total": len(f.media)}, out)
}

func (f *fakeHost) bufferList(prefix, after string, limit int) sdk.BufferKeys {
	var keys []string
	for k := range f.buf {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var res sdk.BufferKeys
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
		res.Next = keys[len(keys)-1]
	}
	res.Keys = keys
	return res
}

func roundTrip(in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (f *fakeHost) count(fn string) int {
	n := 0
	for _, c := range f.calls {
		if c.Fn == fn {
			n++
		}
	}
	return n
}

// step runs one on_run_step with the stored phase and cursor.
func (f *fakeHost) step() error {
	f.steps++
	_, err := onStep(sdk.RunStep{RunID: "r", Phase: f.phase, Cursor: f.cursor})
	return err
}

// runAll steps until the run is done or failed (at most 1000 steps).
func runAll(t *testing.T, f *fakeHost) {
	t.Helper()
	for i := 0; i < 1000 && !f.done && f.failed == ""; i++ {
		if err := f.step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	if !f.done && f.failed == "" {
		t.Fatal("run did not end in 1000 steps")
	}
}

// noKeyLeak fails when the key is in anything but the provider request.
func (f *fakeHost) noKeyLeak() {
	f.t.Helper()
	for _, c := range f.calls {
		if c.Fn != "http.fetch" && strings.Contains(c.Args, testKey) {
			f.t.Errorf("key in %s args: %s", c.Fn, c.Args)
		}
	}
	for _, v := range f.buf {
		if strings.Contains(string(v), testKey) {
			f.t.Errorf("key in buffer value %s", v)
		}
	}
	all := f.failed + f.cursor + string(f.summary) + strings.Join(f.progress, "\n")
	if strings.Contains(all, testKey) {
		f.t.Errorf("key in run state: %s", all)
	}
}

func (f *fakeHost) setBuf(k, v string) { f.buf[k] = []byte(v) }

func ids(prefix string, n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("%s%03d", prefix, i))
	}
	return out
}
