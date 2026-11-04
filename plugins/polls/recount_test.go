package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// seeded builds n submissions: every 7th is spam, every 11th is after the close
// time; answers cycle through the options, with checkbox lists and a stray value.
func seeded(n int) (subs []submission, wantTally map[[2]string]int, wantResponses int) {
	wantTally = map[[2]string]int{}
	for i := 0; i < n; i++ {
		s := submission{ID: fmt.Sprintf("s%03d", i), Status: "new", CreatedAt: "2026-11-01T10:00:00Z",
			Data: map[string]any{"fw": []string{"go", "rust", "zig"}[i%3], "tools": []any{"vim"}, "note": "hi"}}
		if i%2 == 0 {
			s.Data["tools"] = []any{"emacs", "nano"}
		}
		if i%7 == 0 {
			s.Status = "spam"
		}
		if i%11 == 0 {
			s.CreatedAt = "2026-11-05T10:00:00Z"
		}
		subs = append(subs, s)
		if s.Status == "spam" || s.CreatedAt != "2026-11-01T10:00:00Z" {
			continue
		}
		wantResponses++
		fw := s.Data["fw"].(string)
		if fw == "zig" {
			fw = otherOption
		}
		wantTally[[2]string{"fw", fw}]++
		for _, v := range s.Data["tools"].([]any) {
			wantTally[[2]string{"tools", v.(string)}]++
		}
	}
	return subs, wantTally, wantResponses
}

const recountSettings = `{"forms":[{"form_slug":"fw","kind":"poll","closes_at":"2026-11-04T00:00"}]}`

// pagedHost serves subs through forms.submissions, newest first, honouring limit and offset.
func pagedHost(f *fake, subs []submission, offsets *[]int) {
	f.answer = func(c hostCall, out any) error {
		if c.fn != "forms.submissions" {
			return nil
		}
		m := c.raw.(map[string]any)
		limit, off := m["limit"].(int), m["offset"].(int)
		*offsets = append(*offsets, off)
		end := min(off+limit, len(subs))
		items := []submission{}
		if off < len(subs) {
			items = subs[off:end]
		}
		b, _ := json.Marshal(map[string]any{"items": items, "total": len(subs)})
		return json.Unmarshal(b, out)
	}
}

func postRecount(t *testing.T, slug string) (int, map[string]any) {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "POST", Pattern: "/forms/{slug}/recount", PathValues: map[string]string{"slug": slug}})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("route returned an error: %v", err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(res.Body), &body)
	return res.Status, body
}

func TestRecountMatchesAnIndependentCount(t *testing.T) {
	subs, wantTally, wantResponses := seeded(250)
	f := &fake{t: t, settings: recountSettings, forms: formsJSON}
	var offsets []int
	pagedHost(f, subs, &offsets)
	f.install()

	status, body := postRecount(t, "fw")
	if status != http.StatusOK || body["responses"] != float64(wantResponses) {
		t.Fatalf("status %d body %v, want %d responses", status, body, wantResponses)
	}
	if !reflect.DeepEqual(offsets, []int{0, 100, 200}) {
		t.Fatalf("offsets %v", offsets)
	}
	var replaces []hostCall
	for _, c := range f.named("db.exec") {
		if c.sql == replaceForm {
			replaces = append(replaces, c)
		}
	}
	if len(replaces) != 1 {
		t.Fatalf("%d replace statements", len(replaces))
	}
	a := replaces[0].args
	if len(a) != 3 || a[0] != "fw" {
		t.Fatalf("args %v", a)
	}
	got := map[[2]string]int{}
	for _, r := range a[2].([]countRow) {
		got[[2]string{r.Field, r.Option}] = r.N
	}
	if !reflect.DeepEqual(got, wantTally) {
		t.Fatalf("tallies\n got %v\nwant %v", got, wantTally)
	}
	rows := a[1].([]responseRow)
	if len(rows) != wantResponses {
		t.Fatalf("%d response rows", len(rows))
	}
	for _, r := range rows {
		if r.Score != nil {
			t.Fatal("a poll has no score")
		}
	}
}

func TestRecountDeduplicatesAndSkipsSpam(t *testing.T) {
	subs := []submission{
		{ID: "a", Status: "new", Data: map[string]any{"fw": "go"}},
		{ID: "a", Status: "new", Data: map[string]any{"fw": "go"}}, // shifted between pages
		{ID: "b", Status: "spam", Data: map[string]any{"fw": "go"}},
		{ID: "c", Status: "archived", Data: map[string]any{"fw": "rust"}},
	}
	f := &fake{t: t, settings: `{"forms":[{"form_slug":"fw"}]}`, forms: formsJSON}
	var offsets []int
	pagedHost(f, subs, &offsets)
	f.install()
	if _, err := recount(formCfg{Slug: "fw", Kind: kindPoll}); err != nil {
		t.Fatal(err)
	}
	a := f.named("db.exec")[0].args
	if n := len(a[1].([]responseRow)); n != 2 {
		t.Fatalf("%d responses", n)
	}
}

func TestRecountScoresQuizzes(t *testing.T) {
	subs := []submission{
		{ID: "a", Status: "new", Data: map[string]any{"fw": "go"}},
		{ID: "b", Status: "new", Data: map[string]any{"fw": "rust"}},
	}
	f := &fake{t: t, forms: formsJSON}
	var offsets []int
	pagedHost(f, subs, &offsets)
	f.install()
	cfg := formCfg{Slug: "fw", Kind: kindQuiz, Key: []keyPair{{"fw", "go"}}}
	if _, err := recount(cfg); err != nil {
		t.Fatal(err)
	}
	rows := f.named("db.exec")[0].args[1].([]responseRow)
	if *rows[0].Score != 1 || *rows[1].Score != 0 || *rows[0].Max != 1 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestRecountFailureWritesNothing(t *testing.T) {
	f := &fake{t: t, settings: `{"forms":[{"form_slug":"fw"}]}`, forms: formsJSON,
		errs: map[string]error{"forms.submissions": errors.New("down")}}
	f.install()
	status, body := postRecount(t, "fw")
	if status != http.StatusInternalServerError || body["error"] == nil {
		t.Fatalf("status %d body %v", status, body)
	}
	for _, c := range f.named("db.exec") {
		if c.sql == replaceForm {
			t.Fatal("tallies were replaced after a read failure")
		}
	}
}

func TestRecountStopsAtTheDeadlineWithoutWriting(t *testing.T) {
	clock := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	now = func() time.Time { clock = clock.Add(5 * time.Second); return clock }
	defer func() { now = time.Now }()
	subs, _, _ := seeded(250)
	f := &fake{t: t, forms: formsJSON}
	var offsets []int
	pagedHost(f, subs, &offsets)
	f.install()
	if _, err := recount(formCfg{Slug: "fw"}); !errors.Is(err, errRecountIncomplete) {
		t.Fatalf("err = %v", err)
	}
	if len(f.named("db.exec")) != 0 {
		t.Fatal("wrote after an incomplete recount")
	}
}

func TestRecountUnknownSlugIs404(t *testing.T) {
	f := &fake{t: t, settings: `{"forms":[{"form_slug":"fw"}]}`, forms: formsJSON}
	f.install()
	if status, _ := postRecount(t, "nope"); status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
	for _, c := range f.named("db.exec") {
		if c.sql == replaceForm {
			t.Fatal("replaced an unconfigured form")
		}
	}
}

func TestPurgeReceivesTheConfiguredSlugs(t *testing.T) {
	f := &fake{t: t}
	f.install()
	if err := purge([]formCfg{{Slug: "a"}, {Slug: "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := purge(nil); err != nil {
		t.Fatal(err)
	}
	calls := f.named("db.exec")
	if len(calls) != 2 || calls[0].sql != purgeRemoved {
		t.Fatalf("calls %+v", calls)
	}
	if !reflect.DeepEqual(calls[0].args[0], []string{"a", "b"}) {
		t.Fatalf("args %v", calls[0].args)
	}
	if b, _ := json.Marshal(calls[1].args[0]); string(b) != "[]" {
		t.Fatalf("empty setting must send [], got %s", b)
	}
}

func TestRecountRoutePurgesAndToleratesPurgeFailure(t *testing.T) {
	subs, _, _ := seeded(5)
	f := &fake{t: t, settings: `{"forms":[{"form_slug":"fw"}]}`, forms: formsJSON}
	var offsets []int
	pagedHost(f, subs, &offsets)
	inner := f.answer
	f.answer = func(c hostCall, out any) error {
		if c.sql == purgeRemoved {
			return errors.New("db down")
		}
		return inner(c, out)
	}
	f.install()
	status, body := postRecount(t, "fw")
	if status != http.StatusOK || body["responses"] == nil {
		t.Fatalf("status %d body %v", status, body)
	}
}
