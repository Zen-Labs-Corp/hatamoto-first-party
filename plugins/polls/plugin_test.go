package main

import (
	"errors"
	"strings"
	"testing"
)

func TestLogfSendsLevelAndMessage(t *testing.T) {
	var fn string
	var args map[string]string
	callHost = func(f string, a, _ any) error {
		fn, args = f, a.(map[string]string)
		return errors.New("rate_limited")
	}
	if err := logf("warn", "skipped %s", "x"); err == nil {
		t.Fatal("host error must surface")
	}
	if fn != "log" || args["level"] != "warn" || args["message"] != "skipped x" {
		t.Fatalf("got %q %v", fn, args)
	}
}

func TestDBExecPassesStatementAndArgs(t *testing.T) {
	var fn string
	var args map[string]any
	callHost = func(f string, a, _ any) error {
		fn, args = f, a.(map[string]any)
		return nil
	}
	if err := dbExec("DELETE FROM t WHERE a = $1", 1); err != nil {
		t.Fatal(err)
	}
	if fn != "db.exec" || !strings.HasPrefix(args["sql"].(string), "DELETE") {
		t.Fatalf("got %q %v", fn, args)
	}
}

func TestDBJSONUnwrapsValueAndString(t *testing.T) {
	for name, cell := range map[string]string{
		"jsonb value": `[{"n":1}]`,
		"json string": `"[{\"n\":1}]"`,
	} {
		callHost = jsonHost(`{"rows":[[` + cell + `]]}`)
		var got []struct{ N int }
		if err := dbJSON("SELECT 1", &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 1 || got[0].N != 1 {
			t.Fatalf("%s: got %+v", name, got)
		}
	}
	callHost = jsonHost(`{"rows":[]}`)
	if err := dbJSON("SELECT 1", new(any)); err == nil {
		t.Fatal("want an error for no rows")
	}
}
