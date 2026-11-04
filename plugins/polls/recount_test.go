package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
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
	if !reflect.DeepEqual(offsets, []int{0, 200}) {
		t.Fatalf("offsets %v", offsets)
	}
	rows, finish := recounted(t, f)
	got := map[[2]string]int{}
	for _, r := range rows {
		for _, c := range r.Choices {
			got[[2]string{c.Field, c.Option}]++
		}
		if r.Score != nil {
			t.Fatal("a poll has no score")
		}
	}
	if !reflect.DeepEqual(got, wantTally) {
		t.Fatalf("tallies\n got %v\nwant %v", got, wantTally)
	}
	if len(rows) != wantResponses {
		t.Fatalf("%d response rows", len(rows))
	}
	if len(finish.args) != 2 || finish.args[0] != "fw" || finish.args[1] != testCutoff {
		t.Fatalf("finish args %v", finish.args)
	}
}

// recounted returns every response row a recount wrote and its closing
// statement, and checks the rows were written before it with one cutoff.
func recounted(t *testing.T, f *fake) ([]responseRow, hostCall) {
	t.Helper()
	var rows []responseRow
	var finish hostCall
	for _, c := range f.named("db.exec") {
		switch c.sql {
		case upsertResponses:
			if finish.sql != "" {
				t.Fatal("rows written after the closing statement")
			}
			if c.args[0] != "fw" || c.args[1] != testCutoff {
				t.Fatalf("upsert args %v", c.args[:2])
			}
			rows = append(rows, c.args[2].([]responseRow)...)
		case finishRecount:
			finish = c
		case purgeRemoved, releaseLease:
		default:
			t.Fatalf("unexpected statement %q", c.sql)
		}
	}
	if finish.sql == "" {
		t.Fatal("no closing statement")
	}
	return rows, finish
}

// A submission stored while a recount runs is newer than the cutoff. The
// closing statement must never drop such a row by id and may only drop rows
// older than the cutoff that this recount did not rewrite; the tallies must be
// rebuilt from the rows left, not overwritten with the recounted totals.
func TestRecountKeepsAnswersStoredWhileItRuns(t *testing.T) {
	for _, want := range []string{
		"created_at < $2::text::timestamptz AND recounted IS DISTINCT FROM $2::text::timestamptz",
		"FROM keep, jsonb_array_elements(coalesce(keep.choices",
	} {
		if !strings.Contains(finishRecount, want) {
			t.Errorf("finishRecount lacks %q", want)
		}
	}
	if strings.Contains(finishRecount, "NOT IN (SELECT id") || strings.Contains(finishRecount, "excluded.n + ") {
		t.Error("finishRecount deletes by the recounted id list")
	}
	f := &fake{t: t, settings: recountSettings, forms: formsJSON}
	var offsets []int
	subs, _, _ := seeded(30)
	pagedHost(f, subs, &offsets)
	f.install()
	if _, err := recount(formCfg{Slug: "fw", Kind: kindPoll}); err != nil {
		t.Fatal(err)
	}
	var cutoffReads int
	for _, c := range f.named("db.query") {
		if c.sql == dbNow {
			cutoffReads++
		}
	}
	if cutoffReads != 1 {
		t.Fatalf("%d cutoff reads", cutoffReads)
	}
	recounted(t, f)
}

func TestRecountChunksStayUnderTheRequestLimit(t *testing.T) {
	subs, _, _ := seeded(4000)
	for i := range subs {
		subs[i].ID = fmt.Sprintf("%036d", i)
	}
	f := &fake{t: t, forms: formsJSON}
	var offsets []int
	pagedHost(f, subs, &offsets)
	f.install()
	if _, err := recount(formCfg{Slug: "fw"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := recounted(t, f)
	if len(rows) == 0 {
		t.Fatalf("%d rows", len(rows))
	}
	n := 0
	for _, c := range f.named("db.exec") {
		b, _ := json.Marshal(c.args)
		if len(b) > 1<<20 {
			t.Fatalf("a statement carries %d bytes", len(b))
		}
		if c.sql == upsertResponses {
			n++
		}
	}
	if big := chunks(rows, 1000); len(big) < 2 {
		t.Fatalf("chunks(max 1000) gave %d parts", len(big))
	}
	if n < 1 {
		t.Fatal("no upserts")
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
	if rows, _ := recounted(t, f); len(rows) != 2 {
		t.Fatalf("%d responses", len(rows))
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
	rows, _ := recounted(t, f)
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
		if c.sql == upsertResponses || c.sql == finishRecount {
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
	for _, c := range f.named("db.exec") {
		if c.sql != releaseLease {
			t.Fatal("wrote after an incomplete recount")
		}
	}
}

func TestRecountUnknownSlugIs404(t *testing.T) {
	f := &fake{t: t, settings: `{"forms":[{"form_slug":"fw"}]}`, forms: formsJSON}
	f.install()
	if status, _ := postRecount(t, "nope"); status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
	for _, c := range f.named("db.exec") {
		if c.sql == upsertResponses || c.sql == finishRecount {
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

func TestRecountTimeoutTellsTheAdmin(t *testing.T) {
	clock := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	now = func() time.Time { clock = clock.Add(5 * time.Second); return clock }
	defer func() { now = time.Now }()
	subs, _, _ := seeded(250)
	f := &fake{t: t, settings: `{"forms":[{"form_slug":"fw"}]}`, forms: formsJSON}
	var offsets []int
	pagedHost(f, subs, &offsets)
	f.install()
	status, body := postRecount(t, "fw")
	msg, _ := body["error"].(string)
	if status != http.StatusGatewayTimeout || !strings.Contains(msg, "ran out of time") || !strings.Contains(msg, "run Recount again") {
		t.Fatalf("status %d body %v", status, body)
	}
}

func TestRecountTimeoutWhileWritingKeepsTheTallies(t *testing.T) {
	subs, _, _ := seeded(30)
	f := &fake{t: t, forms: formsJSON}
	var offsets []int
	pagedHost(f, subs, &offsets)
	inner := f.answer
	f.answer = func(c hostCall, out any) error {
		if c.sql == upsertResponses { // the first write uses up the budget
			clock := now().Add(time.Minute)
			now = func() time.Time { return clock }
		}
		return inner(c, out)
	}
	f.install()
	defer func() { now = time.Now }()
	// Enough rows for two chunks.
	old := recountChunk
	recountChunk = 200
	defer func() { recountChunk = old }()
	if _, err := recount(formCfg{Slug: "fw"}); !errors.Is(err, errRecountIncomplete) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.named("db.exec") {
		if c.sql == finishRecount {
			t.Fatal("tallies rebuilt after a timeout")
		}
	}
}

func TestOverlappingRecountIsRefusedAndTouchesNothing(t *testing.T) {
	f := &fake{t: t, settings: `{"forms":[{"form_slug":"fw"}]}`, forms: formsJSON, held: true}
	f.install()
	code, body := postRecount(t, "fw")
	if code != 409 || !strings.Contains(body["error"].(string), "already running") {
		t.Fatalf("got %d %v", code, body)
	}
	for _, c := range f.named("db.exec") {
		if c.sql == upsertResponses || c.sql == finishRecount || c.sql == releaseLease {
			t.Fatalf("refused recount ran %q", c.sql)
		}
	}
	if len(f.named("forms.submissions")) != 0 {
		t.Fatal("refused recount read submissions")
	}
}

func TestRecountReleasesItsLease(t *testing.T) {
	f := &fake{t: t, forms: formsJSON}
	pagedHost(f, nil, new([]int))
	f.install()
	if _, err := recount(formCfg{Slug: "fw", Kind: kindPoll}); err != nil {
		t.Fatal(err)
	}
	ex := f.named("db.exec")
	last := ex[len(ex)-1]
	if last.sql != releaseLease || last.args[0] != "fw" || last.args[1] == "" {
		t.Fatalf("last statement %q %v", last.sql, last.args)
	}
	if !strings.Contains(releaseLease, "run_id = $2") {
		t.Fatal("release does not check the lease owner")
	}
}
