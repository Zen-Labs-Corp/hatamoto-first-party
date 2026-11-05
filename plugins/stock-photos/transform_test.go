package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func putItem(h *fakeHost, key string) {
	v, _ := json.Marshal(bufferItem{SourceKey: key, Handle: "h-" + key, Alt: "alt " + key, Credit: "credit " + key, Caption: "cap " + key})
	h.buffer["item/"+key] = string(v)
}

func attached(h *fakeHost) []sdk.MediaAttach {
	var out []sdk.MediaAttach
	for _, c := range h.names("changeset.media.attach") {
		var a sdk.MediaAttach
		_ = json.Unmarshal([]byte(c.Args), &a)
		out = append(out, a)
	}
	return out
}

func summary(t *testing.T, h *fakeHost) (s struct {
	Staged       int       `json:"staged"`
	Skipped      []skipped `json:"skipped"`
	SkippedTotal int       `json:"skipped_total"`
}) {
	t.Helper()
	var a sdk.RunFinishArgs
	if err := json.Unmarshal([]byte(h.ends("run.finish").Args), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(a.Summary, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTransformAttachesEveryItemWithAllFields(t *testing.T) {
	h := newHost(t, "transform", `{"query":"q"}`)
	putItem(h, "pexels:1")
	putItem(h, "openverse:abc")
	h.buffer["skip/openverse:zzz"] = `{"reason":"host not ticked: x.example"}`
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	got := attached(h)
	if len(got) != 2 {
		t.Fatalf("attached = %+v", got)
	}
	want := sdk.MediaAttach{SourceKey: "openverse:abc", Handle: "h-openverse:abc", Alt: "alt openverse:abc", Caption: "cap openverse:abc", Credit: "credit openverse:abc"}
	if got[0] != want {
		t.Fatalf("attach = %+v, want %+v", got[0], want)
	}
	s := summary(t, h)
	if s.Staged != 2 || s.SkippedTotal != 1 || len(s.Skipped) != 1 || s.Skipped[0].Key != "openverse:zzz" || !strings.Contains(s.Skipped[0].Reason, "x.example") {
		t.Fatalf("summary = %+v", s)
	}
	if len(h.names("http.fetch")) != 0 {
		t.Fatal("transform used the network")
	}
}

func TestTransformEmptyBufferFinishesWithZero(t *testing.T) {
	h := newHost(t, "transform", `{}`)
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if s := summary(t, h); s.Staged != 0 || s.Skipped == nil || len(s.Skipped) != 0 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestTransformSkipsNotOwnedItem(t *testing.T) {
	h := newHost(t, "transform", `{}`)
	putItem(h, "pexels:1")
	putItem(h, "pexels:2")
	h.errs["changeset.media.attach"] = &sdk.HostError{Code: sdk.ErrNotOwned, Message: "owner media"}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	s := summary(t, h)
	if s.Staged != 0 || s.SkippedTotal != 2 || len(s.Skipped) != 2 || s.Skipped[0].Reason != "not staged: not_owned" {
		t.Fatalf("summary = %+v", s)
	}
}

func TestTransformOtherErrorFailsRun(t *testing.T) {
	h := newHost(t, "transform", `{}`)
	putItem(h, "pexels:1")
	h.errs["changeset.media.attach"] = &sdk.HostError{Code: sdk.ErrInternal, Message: "boom"}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	if h.failReason() != "Stock photo search failed." {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestTransformPagesThroughManyItemsAndCapsSummary(t *testing.T) {
	h := newHost(t, "transform", `{}`)
	for i := 0; i < 130; i++ {
		putItem(h, fmt.Sprintf("pexels:%03d", i))
	}
	for i := 0; i < 70; i++ {
		h.buffer[fmt.Sprintf("skip/pexels:s%03d", i)] = `{"reason":"r"}`
	}
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	s := summary(t, h)
	if s.Staged != 130 || len(attached(h)) != 130 || len(s.Skipped) != maxSkipped || s.SkippedTotal != 70 {
		t.Fatalf("staged=%d attached=%d skipped=%d total=%d", s.Staged, len(attached(h)), len(s.Skipped), s.SkippedTotal)
	}
}

func TestTransformResumesAfterCursor(t *testing.T) {
	h := newHost(t, "transform", `{}`)
	putItem(h, "pexels:1")
	putItem(h, "pexels:2")
	putItem(h, "pexels:3")
	if err := h.step(`{"after":"item/pexels:1","staged":1}`); err != nil {
		t.Fatal(err)
	}
	got := attached(h)
	if len(got) != 2 || got[0].SourceKey != "pexels:2" {
		t.Fatalf("attached = %+v", got)
	}
	if s := summary(t, h); s.Staged != 3 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestTransformCountsOnlyCreatedMedia(t *testing.T) {
	h := newHost(t, "transform", `{"query":"q"}`)
	h.existing = map[string]bool{"pexels:1": true}
	putItem(h, "pexels:1")
	putItem(h, "pexels:2")
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	var a sdk.RunFinishArgs
	_ = json.Unmarshal([]byte(h.ends("run.finish").Args), &a)
	var s struct {
		Staged  int `json:"staged"`
		Already int `json:"already_in_library"`
	}
	_ = json.Unmarshal(a.Summary, &s)
	if s.Staged != 1 || s.Already != 1 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestTransformSummaryNamesSearchStopFirst(t *testing.T) {
	h := newHost(t, "transform", `{}`)
	for i := 0; i < 70; i++ {
		h.buffer[fmt.Sprintf("skip/openverse:s%03d", i)] = `{"reason":"r"}`
	}
	h.buffer["skip/search:stopped"] = `{"reason":"The provider is rate limiting"}`
	if err := h.step(""); err != nil {
		t.Fatal(err)
	}
	s := summary(t, h)
	if len(s.Skipped) != maxSkipped || s.Skipped[0].Key != "search:stopped" || s.Skipped[0].Reason == "" || s.SkippedTotal != 71 {
		t.Fatalf("first=%+v len=%d total=%d", s.Skipped[0], len(s.Skipped), s.SkippedTotal)
	}
	for _, e := range s.Skipped[1:] {
		if e.Key == "search:stopped" {
			t.Fatalf("listed twice: %+v", s.Skipped)
		}
	}
}
