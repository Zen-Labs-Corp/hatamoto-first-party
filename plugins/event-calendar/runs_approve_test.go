package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func approveParams() map[string]any {
	return map[string]any{
		"action": "approve", "title": "Open mic night", "start_at": "2027-02-20T20:00:00Z", "end_at": "2027-02-20T22:00:00Z",
		"venue_name": "The Anchor", "description": "Bring a song.", "submission": "12",
	}
}

func TestApproveRunStages(t *testing.T) {
	h := &runHost{name: runEditDates, runID: "run-9", params: approveParams(), created: true}
	h.install(t)
	h.step(t)
	if h.failed != "" || len(h.upserts) != 1 {
		t.Fatalf("failed = %q, upserts = %d", h.failed, len(h.upserts))
	}
	up := h.upserts[0]
	if up.SourceKey != "sub-12" || up.Schema != "calendar-event" || up.Name != "Open mic night" || up.Slug != "" {
		t.Errorf("upsert = %+v", up)
	}
	got := map[string]string{}
	for k, v := range attrsOf(t, up) {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		got[k] = s
	}
	want := map[string]string{
		"start_at": "2027-02-20T20:00:00Z", "end_at": "2027-02-20T22:00:00Z", "venue_name": "The Anchor",
		"description": "Bring a song.", "source_kind": "submission", "source_uid": "sub-12",
	}
	if len(got) != len(want) {
		t.Errorf("attributes = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if raw, _ := json.Marshal(up); strings.Contains(string(raw), "@") || strings.Contains(string(raw), "source_url") {
		t.Errorf("the upsert carries an address or source_url: %s", raw)
	}
	if !strings.Contains(jsonText(h.finished), `"staged":1`) {
		t.Errorf("finished = %v", h.finished)
	}
	for _, c := range h.calls {
		if c == "db.query" || c == "db.exec" || c == "mail.send" {
			t.Errorf("the run called %s", c)
		}
	}
}

func TestApproveRunRejectsBadInput(t *testing.T) {
	cases := map[string]func(map[string]any){
		"id not digits": func(p map[string]any) { p["submission"] = "12; drop" },
		"id empty":      func(p map[string]any) { p["submission"] = "" },
		"no title":      func(p map[string]any) { p["title"] = " " },
		"bad start":     func(p map[string]any) { p["start_at"] = "tomorrow" },
		"no start":      func(p map[string]any) { delete(p, "start_at") },
		"bad end":       func(p map[string]any) { p["end_at"] = "later" },
		"end < start":   func(p map[string]any) { p["end_at"] = "2027-02-20T19:00:00Z" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			p := approveParams()
			mut(p)
			h := &runHost{name: runEditDates, runID: "run-9", params: p, created: true}
			h.install(t)
			h.step(t)
			if h.failed == "" || strings.Contains(h.failed, "\n") || len(h.upserts) != 0 {
				t.Errorf("failed = %q, upserts = %d", h.failed, len(h.upserts))
			}
		})
	}
}

func TestApproveRunAlreadyStaged(t *testing.T) {
	h := &runHost{name: runEditDates, runID: "run-9", params: approveParams()}
	h.install(t)
	h.step(t)
	if h.failed != "This submission was already staged." {
		t.Errorf("failed = %q", h.failed)
	}
}

func TestApproveRunCapsTitleByCharacters(t *testing.T) {
	p := approveParams()
	title := strings.Repeat("祭", maxSubmitTitle) // 600 bytes, 200 characters
	p["title"] = title + "x"
	h := &runHost{name: runEditDates, runID: "run-9", params: p, created: true}
	h.install(t)
	h.step(t)
	if len(h.upserts) != 1 || h.upserts[0].Name != title {
		t.Fatalf("failed = %q, upserts = %+v", h.failed, h.upserts)
	}
}
