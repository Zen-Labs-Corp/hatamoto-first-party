package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func (f *fakeHost) addImages(prefix string, n int, alt string) {
	for _, id := range ids(prefix, n) {
		f.media = append(f.media, img(id, alt))
	}
}

func (f *fakeHost) lastMeta() meta {
	f.t.Helper()
	var m meta
	if err := json.Unmarshal(f.buf["meta"], &m); err != nil {
		f.t.Fatalf("meta: %v (%q)", err, f.buf["meta"])
	}
	return m
}

func (f *fakeHost) suggestionKeys() []string {
	return f.bufferList("s/", "", 0).Keys
}

func fetchPhase(t *testing.T, f *fakeHost) {
	t.Helper()
	for i := 0; i < 1000 && f.phase == "fetch" && f.failed == ""; i++ {
		if err := f.step(); err != nil {
			t.Fatal(err)
		}
	}
	if f.phase == "fetch" && f.failed == "" {
		t.Fatal("fetch phase did not end")
	}
}

func TestFetchOnlyMissingReachesProvider(t *testing.T) {
	f := newFake(t)
	f.media = append(f.media, img("a", "Has alt"), img("b", ""), img("c", "  "), img("d", "Also alt"))
	fetchPhase(t, f)
	if len(f.requests) != 2 {
		t.Fatalf("%d provider calls, want 2 (b and c)", len(f.requests))
	}
	for _, r := range f.requests {
		if strings.Contains(r.Body, "/a.jpg") || strings.Contains(r.Body, "/d.jpg") {
			t.Fatalf("image with alt text was sent: %s", r.Body)
		}
	}
	if got := f.suggestionKeys(); len(got) != 2 || got[0] != "s/b" || got[1] != "s/c" {
		t.Fatalf("suggestions %v", got)
	}
	if string(f.buf["s/b"]) != `{"alt":"A calm lake at dawn"}` {
		t.Fatalf("s/b = %s", f.buf["s/b"])
	}
	f.noKeyLeak()
}

func TestFetchAllImagesWhenOnlyMissingFalse(t *testing.T) {
	f := newFake(t)
	f.settings["only_missing"] = false
	f.media = append(f.media, img("a", "Has alt"), img("b", ""))
	fetchPhase(t, f)
	if len(f.requests) != 2 {
		t.Fatalf("%d provider calls, want 2", len(f.requests))
	}
}

func TestFetchLimitClamp(t *testing.T) {
	f := newFake(t)
	f.addImages("i", 30, "")
	f.settings["limit"] = 5.0
	fetchPhase(t, f)
	if len(f.requests) != 5 || f.lastMeta().Checked != 5 {
		t.Fatalf("limit 5: %d calls, meta %+v", len(f.requests), f.lastMeta())
	}

	f = newFake(t)
	f.addImages("i", 60, "")
	fetchPhase(t, f) // blank limit = 50
	if len(f.requests) != 50 {
		t.Fatalf("default limit: %d calls, want 50", len(f.requests))
	}

	f = newFake(t)
	f.addImages("i", 230, "")
	f.settings["limit"] = 9999.0
	fetchPhase(t, f)
	if len(f.requests) != 200 {
		t.Fatalf("max limit: %d calls, want 200", len(f.requests))
	}
}

func TestFetchOneProviderCallPerStep(t *testing.T) {
	f := newFake(t)
	f.addImages("i", 7, "")
	for f.phase == "fetch" {
		before := len(f.requests)
		if err := f.step(); err != nil {
			t.Fatal(err)
		}
		if n := len(f.requests) - before; n > 1 {
			t.Fatalf("step %d made %d provider calls", f.steps, n)
		}
		if f.steps > 100 {
			t.Fatal("too many steps")
		}
	}
	if len(f.requests) != 7 {
		t.Fatalf("%d provider calls", len(f.requests))
	}
}

func TestFetchResumesFromCheckpoint(t *testing.T) {
	f := newFake(t)
	f.addImages("i", 3, "")
	f.vision = func(visionReq) (fetched, error) { return answer("A thing"), nil }
	if err := f.step(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.requests[0].Body, "/i001.jpg") {
		t.Fatalf("step 1 did not take the first image: %s", f.requests[0].Body)
	}
	var c fetchCursor
	if err := json.Unmarshal([]byte(f.cursor), &c); err != nil || c.I != 1 || c.N != 1 || c.S != 1 {
		t.Fatalf("cursor %q: %+v %v", f.cursor, c, err)
	}
	if err := f.step(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.requests[1].Body, "/i002.jpg") {
		t.Fatalf("step 2 did not take the second image: %s", f.requests[1].Body)
	}
	if err := json.Unmarshal([]byte(f.cursor), &c); err != nil || c.N != 2 || c.S != 2 {
		t.Fatalf("counts not carried: %q", f.cursor)
	}
	fetchPhase(t, f)
	if m := f.lastMeta(); m.Checked != 3 || m.Suggested != 3 {
		t.Fatalf("meta %+v", m)
	}
}

func TestFetchDecorativeIsListedNotStaged(t *testing.T) {
	f := newFake(t)
	f.addImages("i", 2, "")
	f.vision = func(req visionReq) (fetched, error) {
		if strings.Contains(req.Body, "i001") {
			return answer("DECORATIVE"), nil
		}
		return answer("A path"), nil
	}
	fetchPhase(t, f)
	if _, ok := f.buf["s/i001"]; ok {
		t.Fatal("decorative image has a suggestion")
	}
	if string(f.buf["d/i001"]) != "i001.jpg" {
		t.Fatalf("d/i001 = %q", f.buf["d/i001"])
	}
	if m := f.lastMeta(); m.Decorative != 1 || m.Suggested != 1 {
		t.Fatalf("meta %+v", m)
	}
}

func TestFetchProviderErrorSkipsTheImage(t *testing.T) {
	f := newFake(t)
	f.addImages("i", 3, "")
	f.vision = func(req visionReq) (fetched, error) {
		if strings.Contains(req.Body, "i002") {
			return fetched{Status: 500, Body: "boom " + testKey}, nil
		}
		return answer("Fine"), nil
	}
	fetchPhase(t, f)
	if f.failed != "" {
		t.Fatalf("run failed: %s", f.failed)
	}
	if got := f.suggestionKeys(); len(got) != 2 {
		t.Fatalf("suggestions %v", got)
	}
	if m := f.lastMeta(); m.Errors != 1 || m.Checked != 3 || m.Suggested != 2 {
		t.Fatalf("meta %+v", m)
	}
	f.noKeyLeak()
}

func TestFetchOtherProviderFailures(t *testing.T) {
	answers := map[string]func() (fetched, error){
		"host error": func() (fetched, error) { return fetched{}, errors.New("deadline exceeded") },
		"truncated":  func() (fetched, error) { return fetched{Status: 200, Body: `{"choi`, Truncated: true}, nil },
		"not json":   func() (fetched, error) { return fetched{Status: 200, Body: "<html>"}, nil },
		"empty":      func() (fetched, error) { return answer("   "), nil },
		"only lead":  func() (fetched, error) { return answer("Image of"), nil },
	}
	for name, fn := range answers {
		f := newFake(t)
		f.addImages("i", 2, "")
		f.vision = func(visionReq) (fetched, error) { return fn() }
		fetchPhase(t, f)
		if f.failed != "" || f.lastMeta().Errors != 2 || len(f.suggestionKeys()) != 0 {
			t.Errorf("%s: failed=%q meta=%+v", name, f.failed, f.lastMeta())
		}
	}
}

func TestFetchBuildsAbsoluteURLs(t *testing.T) {
	f := newFake(t)
	f.media = append(f.media, img("a", ""))
	f.media = append(f.media, map[string]any{"id": "b", "filename": "b.png", "mime_type": "image/png", "public_url": "https://cdn.test/b.png"})
	fetchPhase(t, f)
	if len(f.requests) != 2 {
		t.Fatalf("%d requests", len(f.requests))
	}
	if !strings.Contains(f.requests[0].Body, `"url":"https://example.com/media/a.jpg"`) {
		t.Fatalf("relative URL not made absolute (trailing slash of site_url trimmed): %s", f.requests[0].Body)
	}
	if !strings.Contains(f.requests[1].Body, `"url":"https://cdn.test/b.png"`) {
		t.Fatalf("absolute URL changed: %s", f.requests[1].Body)
	}
}

func TestFetchRelativeURLWithoutSiteURLFailsTheRun(t *testing.T) {
	f := newFake(t)
	delete(f.settings, "site_url")
	f.media = append(f.media, img("a", ""))
	fetchPhase(t, f)
	if !strings.Contains(f.failed, "site address") || len(f.requests) != 0 {
		t.Fatalf("failed=%q requests=%d", f.failed, len(f.requests))
	}
}

func TestFetchPlainHTTPImageIsAnError(t *testing.T) {
	f := newFake(t)
	f.media = append(f.media, map[string]any{"id": "a", "filename": "a.jpg", "mime_type": "image/jpeg", "public_url": "http://cdn.test/a.jpg"})
	fetchPhase(t, f)
	if len(f.requests) != 0 || f.lastMeta().Errors != 1 || f.failed != "" {
		t.Fatalf("requests=%d meta=%+v failed=%q", len(f.requests), f.lastMeta(), f.failed)
	}
}

func TestFetchMissingKeyFailsTheRun(t *testing.T) {
	f := newFake(t)
	f.settings["api_key"] = " "
	f.addImages("i", 1, "")
	fetchPhase(t, f)
	if f.failed != msgNoKey || len(f.requests) != 0 {
		t.Fatalf("failed=%q", f.failed)
	}
}

func TestFetchSkipsUnsupportedTypes(t *testing.T) {
	f := newFake(t)
	f.media = append(f.media,
		map[string]any{"id": "s", "filename": "s.svg", "mime_type": "image/svg+xml", "public_url": "/media/s.svg"},
		map[string]any{"id": "t", "filename": "t.tiff", "mime_type": "image/tiff", "public_url": "/media/t.tiff"},
		img("a", ""))
	fetchPhase(t, f)
	if len(f.requests) != 1 {
		t.Fatalf("%d provider calls", len(f.requests))
	}
	if m := f.lastMeta(); m.Skipped != 2 || m.Checked != 1 {
		t.Fatalf("meta %+v", m)
	}
}

func TestFetchKeepsPagingOnShortOrEmptyPages(t *testing.T) {
	f := newFake(t)
	f.addImages("i", 65, "")
	// Only the last page has qualifying images; the first pages come back empty.
	f.listFilter = func(m map[string]any) bool { return m["id"].(string) >= "i061" }
	fetchPhase(t, f)
	if f.failed != "" || len(f.requests) != 5 {
		t.Fatalf("failed=%q requests=%d", f.failed, len(f.requests))
	}
	// The run ends on offset >= total, not on a short page.
	if m := f.lastMeta(); m.Checked != 5 {
		t.Fatalf("meta %+v", m)
	}
}

func TestFetchListFailureFailsTheRun(t *testing.T) {
	f := newFake(t)
	f.addImages("i", 1, "")
	callHost = func(fn string, args, out any) error {
		if fn == "media.list" {
			return errors.New("down")
		}
		return f.call(fn, args, out)
	}
	fetchPhase(t, f)
	if f.failed == "" {
		t.Fatal("expected the run to fail")
	}
}

func TestFetchCursorStaysSmallAndKeyFree(t *testing.T) {
	f := newFake(t)
	f.addImages("i", 45, "")
	f.settings["limit"] = 45.0
	fetchPhase(t, f)
	if len(f.cursor) > 200 {
		t.Fatalf("cursor %d bytes", len(f.cursor))
	}
	f.noKeyLeak()
}
