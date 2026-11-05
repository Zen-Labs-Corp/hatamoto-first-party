package main

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestEntityDeletedRemovesVotesAndPage(t *testing.T) {
	f := &fakeHost{}
	f.install(t)
	if err := onEntityDeleted(sdk.EntityEvent{ID: testID}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || !strings.HasPrefix(f.calls[0].sql, "DELETE FROM votes") || !strings.HasPrefix(f.calls[1].sql, "DELETE FROM pages") {
		t.Fatalf("calls %+v", f.calls)
	}
	for _, c := range f.calls {
		if len(c.args) != 1 || c.args[0] != testID {
			t.Fatalf("args %+v", c.args)
		}
	}
}

func TestEntityDeletedIgnoresABadID(t *testing.T) {
	f := &fakeHost{}
	f.install(t)
	if err := onEntityDeleted(sdk.EntityEvent{ID: "nope"}); err != nil || len(f.calls) != 0 {
		t.Fatalf("err %v, calls %+v", err, f.calls)
	}
}

func TestEntityDeletedFailsOpen(t *testing.T) {
	f := &fakeHost{db: func(hostCall, any) error { return errors.New("unavailable") }}
	f.install(t)
	if err := onEntityDeleted(sdk.EntityEvent{ID: testID}); err != nil {
		t.Fatal(err)
	}
}
