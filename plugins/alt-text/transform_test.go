package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// toTransform puts the fake into the transform phase.
func (f *fakeHost) toTransform() {
	f.phase, f.cursor = sdk.RunPhaseTransform, ""
}

func (f *fakeHost) transformAll() {
	f.t.Helper()
	for i := 0; i < 100 && !f.done && f.failed == ""; i++ {
		if err := f.step(); err != nil {
			f.t.Fatal(err)
		}
	}
	if !f.done {
		f.t.Fatalf("transform did not finish (failed=%q)", f.failed)
	}
}

func (f *fakeHost) summaryMap() map[string]any {
	var m map[string]any
	if err := json.Unmarshal(f.summary, &m); err != nil {
		f.t.Fatalf("summary %s: %v", f.summary, err)
	}
	return m
}

func TestTransformStagesBufferedSuggestions(t *testing.T) {
	f := newFake(t)
	f.toTransform()
	f.setBuf("s/a", `{"alt":"A red door"}`)
	f.setBuf("s/b", `{"alt":"Two cats"}`)
	f.setBuf("d/c", "divider.png")
	f.setBuf("meta", `{"checked":4,"suggested":2,"decorative":1,"errors":1,"skipped":3}`)
	f.transformAll()

	if len(f.staged) != 2 || f.staged[0].MediaID != "a" || f.staged[0].Alt != "A red door" || f.staged[1].MediaID != "b" {
		t.Fatalf("staged %+v", f.staged)
	}
	allowed := map[string]bool{"buffer.list": true, "buffer.get": true, "changeset.media.alt": true,
		"run.checkpoint": true, "run.progress": true, "run.finish": true}
	for _, c := range f.calls {
		if !allowed[c.Fn] {
			t.Errorf("transform phase called %s", c.Fn)
		}
	}
	sum := f.summaryMap()
	if sum["checked"] != 4.0 || sum["suggested"] != 2.0 || sum["decorative"] != 1.0 || sum["errors"] != 1.0 || sum["skipped"] != 3.0 {
		t.Fatalf("summary %v", sum)
	}
	if files := sum["decorative_files"].([]any); len(files) != 1 || files[0] != "divider.png" {
		t.Fatalf("decorative_files %v", sum["decorative_files"])
	}
	if want := "4 images checked, 2 suggestions, 1 decorative (skipped), 1 errors"; f.progress[len(f.progress)-1] != want {
		t.Fatalf("progress %q, want %q", f.progress, want)
	}
	f.noKeyLeak()
}

func TestTransformResumesAcrossPages(t *testing.T) {
	f := newFake(t)
	f.toTransform()
	for _, id := range ids("m", 250) {
		f.setBuf("s/"+id, `{"alt":"Alt `+id+`"}`)
	}
	f.setBuf("meta", `{"checked":250,"suggested":250}`)
	if err := f.step(); err != nil {
		t.Fatal(err)
	}
	if len(f.staged) != 100 || f.done {
		t.Fatalf("after step 1: %d staged, done=%v", len(f.staged), f.done)
	}
	f.transformAll()
	if len(f.staged) != 250 {
		t.Fatalf("%d staged, want 250", len(f.staged))
	}
	seen := map[string]bool{}
	for _, s := range f.staged {
		if seen[s.MediaID] {
			t.Fatalf("%s staged twice", s.MediaID)
		}
		seen[s.MediaID] = true
	}
	if f.summaryMap()["suggested"] != 250.0 {
		t.Fatalf("summary %s", f.summary)
	}
}

func TestTransformStagingErrorIsCountedAndRunContinues(t *testing.T) {
	f := newFake(t)
	f.toTransform()
	f.setBuf("s/a", `{"alt":"One"}`)
	f.setBuf("s/b", `{"alt":"Two"}`)
	f.setBuf("s/c", `{"alt":"Three"}`)
	f.setBuf("s/d", `not json`)
	f.setBuf("meta", `{"checked":4,"suggested":4,"errors":1}`)
	f.stageErr["b"] = &sdk.HostError{Code: sdk.ErrNotOwned, Message: "owned"}
	f.stageErr["c"] = errors.New("boom")
	f.transformAll()
	if len(f.staged) != 1 || f.staged[0].MediaID != "a" {
		t.Fatalf("staged %+v", f.staged)
	}
	sum := f.summaryMap()
	if sum["suggested"] != 1.0 || sum["errors"] != 4.0 || f.failed != "" {
		t.Fatalf("summary %v failed=%q", sum, f.failed)
	}
}

func TestTransformMessageStaysWithinCapWithManyDecorative(t *testing.T) {
	f := newFake(t)
	f.toTransform()
	for _, id := range ids("d", 150) {
		f.setBuf("d/"+id, "a-rather-long-file-name-for-"+id+".png")
	}
	f.setBuf("meta", `{"checked":200,"suggested":50,"decorative":150}`)
	f.transformAll() // the fake fails the test on a progress message over 512 bytes
	files := f.summaryMap()["decorative_files"].([]any)
	if len(files) != 100 {
		t.Fatalf("%d names, want the first 100", len(files))
	}
	for _, p := range f.progress {
		if strings.Contains(p, ".png") {
			t.Fatalf("file name in the progress message: %q", p)
		}
	}
}

func TestTransformWithoutMetaOrSuggestions(t *testing.T) {
	f := newFake(t)
	f.toTransform()
	f.transformAll()
	sum := f.summaryMap()
	if sum["checked"] != 0.0 || sum["suggested"] != 0.0 || len(sum["decorative_files"].([]any)) != 0 {
		t.Fatalf("summary %v", sum)
	}
}

func TestSuggestRunEndToEnd(t *testing.T) {
	f := newFake(t)
	f.media = append(f.media, img("a1", "Has alt"), img("b1", ""), img("a2", "Has alt"), img("b2", ""), img("a3", "Has alt"), img("b3", ""))
	f.vision = func(req visionReq) (fetched, error) {
		switch {
		case strings.Contains(req.Body, "b1"):
			return answer("\"Image of a harbour at night.\""), nil
		case strings.Contains(req.Body, "b2"):
			return answer("DECORATIVE"), nil
		}
		return fetched{Status: 429, Body: "slow down"}, nil
	}
	runAll(t, f)
	if f.failed != "" {
		t.Fatalf("failed: %s", f.failed)
	}
	if len(f.requests) != 3 {
		t.Fatalf("%d provider calls, want 3 (only the images without alt)", len(f.requests))
	}
	if len(f.staged) != 1 || f.staged[0].MediaID != "b1" || f.staged[0].Alt != "A harbour at night" {
		t.Fatalf("staged %+v", f.staged)
	}
	sum := f.summaryMap()
	want := map[string]float64{"checked": 3, "suggested": 1, "decorative": 1, "errors": 1}
	for k, v := range want {
		if sum[k] != v {
			t.Errorf("summary[%s] = %v, want %v (%v)", k, sum[k], v, sum)
		}
	}
	if files := sum["decorative_files"].([]any); len(files) != 1 || files[0] != "b2.jpg" {
		t.Fatalf("decorative_files %v", files)
	}
	if last := f.progress[len(f.progress)-1]; last != fmt.Sprintf(progressPattern, 3, 1, 1, 1) {
		t.Fatalf("last progress %q", last)
	}
	f.noKeyLeak()
}
