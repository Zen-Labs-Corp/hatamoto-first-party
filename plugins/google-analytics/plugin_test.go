package main

import (
	"encoding/json"
	"errors"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// head runs the registered render.head filter with a fake settings.get.
func head(t *testing.T, settings string, hostErr error) []sdk.HeadTag {
	t.Helper()
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		if hostErr != nil {
			return hostErr
		}
		return json.Unmarshal([]byte(settings), out)
	}
	in, _ := json.Marshal(sdk.HeadContext{Path: "/"})
	out, err := guest.Handle(sdk.RenderHead.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var c sdk.HeadContext
	if err := json.Unmarshal(out, &c); err != nil {
		t.Fatal(err)
	}
	return c.Tags
}

func TestGreetingEmitsOneMeta(t *testing.T) {
	tags := head(t, `{"greeting":" hello "}`, nil)
	if len(tags) != 1 || tags[0].Kind != "meta" || tags[0].Attrs["content"] != "hello" {
		t.Fatalf("tags = %+v", tags)
	}
}

func TestEmptyOrFailingSettingAddsNothing(t *testing.T) {
	if tags := head(t, `{"greeting":"  "}`, nil); len(tags) != 0 {
		t.Fatalf("empty greeting: tags = %+v", tags)
	}
	if tags := head(t, "", errors.New("host down")); len(tags) != 0 {
		t.Fatalf("host error: tags = %+v", tags)
	}
}
