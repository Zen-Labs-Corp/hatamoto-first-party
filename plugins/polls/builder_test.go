package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func runBuilder(t *testing.T, f *fake, req sdk.BuilderDataRequest) sdk.BuilderDataRequest {
	t.Helper()
	f.t = t
	f.install()
	in, _ := json.Marshal(req)
	out, err := guest.Handle(sdk.BuilderData.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var got sdk.BuilderDataRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

const twoForms = `{"forms":[{"form_slug":"hidden","show_results":"never"},{"form_slug":"fw","show_results":"always"}]}`

func TestBuilderItemsFromTheFirstPublicForm(t *testing.T) {
	f := &fake{settings: twoForms, forms: formsJSON, answer: answerResults(resultsDoc)}
	got := runBuilder(t, f, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "a", Source: builderSource}}})
	if len(got.Results) != 1 || got.Results[0].ID != "a" {
		t.Fatalf("results %+v", got.Results)
	}
	items := got.Results[0].Items
	// radio: go, rust, Other; checkbox: vim, emacs, nano, Other
	if len(items) != 7 {
		t.Fatalf("%d items", len(items))
	}
	first := items[0]
	if first.Title != "Go" || first.Category != "Which framework?" || first.Attrs["count"] != "6" || first.Attrs["percent"] != "60" ||
		first.Attrs["form"] != "fw" || first.Attrs["field"] != "fw" {
		t.Fatalf("first %+v", first)
	}
	if first.URL != "" || first.Image != "" || first.PublishedAt != "" {
		t.Fatalf("unexpected optional fields %+v", first)
	}
	if n := len(f.named("db.query")); n != 1 {
		t.Fatalf("%d queries", n)
	}
	if len(f.named("settings.get")) != 1 || len(f.named("forms.list")) != 1 {
		t.Fatal("settings and forms must each be read once")
	}
}

func TestBuilderLimits(t *testing.T) {
	for limit, want := range map[int]int{2: 2, 0: 7, 100: 7} {
		f := &fake{settings: twoForms, forms: formsJSON, answer: answerResults(resultsDoc)}
		got := runBuilder(t, f, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "a", Source: builderSource, Limit: limit}}})
		if n := len(got.Results[0].Items); n != want {
			t.Errorf("limit %d: %d items, want %d", limit, n, want)
		}
	}
}

func TestBuilderIgnoresOtherSourcesAndAnsweredIDs(t *testing.T) {
	f := &fake{settings: twoForms, forms: formsJSON, answer: answerResults(resultsDoc)}
	req := sdk.BuilderDataRequest{
		Requests: []sdk.BuilderDataQuery{{ID: "x", Source: "ext.other.thing"}, {ID: "done", Source: builderSource}},
		Results:  []sdk.BuilderDataResult{{ID: "done", Value: "kept"}},
	}
	got := runBuilder(t, f, req)
	if len(got.Results) != 1 || got.Results[0].Value != "kept" {
		t.Fatalf("results %+v", got.Results)
	}
	if len(f.calls) != 0 {
		t.Fatalf("host calls for nothing to do: %+v", f.calls)
	}
}

func TestBuilderNoPublicFormGivesEmptyItems(t *testing.T) {
	f := &fake{settings: `{"forms":[{"form_slug":"fw","show_results":"never"}]}`, forms: formsJSON, answer: answerResults(resultsDoc)}
	got := runBuilder(t, f, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "a", Source: builderSource}}})
	if len(got.Results) != 1 || len(got.Results[0].Items) != 0 || got.Results[0].Error != "" {
		t.Fatalf("results %+v", got.Results)
	}
	if len(f.named("db.query")) != 0 {
		t.Fatal("no query without a public form")
	}
}

func TestBuilderFailsOpen(t *testing.T) {
	req := sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "a", Source: builderSource}}}
	for name, f := range map[string]*fake{
		"settings": {settings: twoForms, forms: formsJSON, errs: map[string]error{"settings.get": errors.New("down")}},
		"forms":    {settings: twoForms, forms: formsJSON, errs: map[string]error{"forms.list": errors.New("down")}},
		"db": {settings: twoForms, forms: formsJSON, answer: func(c hostCall, out any) error {
			return errors.New("down")
		}},
	} {
		got := runBuilder(t, f, req)
		if len(got.Results) != 0 || len(got.Requests) != 1 {
			t.Errorf("%s: request changed: %+v", name, got)
		}
	}
}

func TestBuilderCutsLongStringsOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("é", 2000) // 4000 bytes
	forms := strings.Replace(formsJSON, `"label":"Go"`, `"label":"`+long+`"`, 1)
	f := &fake{settings: twoForms, forms: forms, answer: answerResults(resultsDoc)}
	got := runBuilder(t, f, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "a", Source: builderSource}}})
	title := got.Results[0].Items[0].Title
	if len(title) > 2048 || !utf8.ValidString(title) || len(title) < 2046 {
		t.Fatalf("title %d bytes, valid %v", len(title), utf8.ValidString(title))
	}
}
