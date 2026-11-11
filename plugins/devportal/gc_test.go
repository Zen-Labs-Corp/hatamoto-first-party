package main

import (
	"errors"
	"testing"

	"hatamoto.dev/sdk"
)

func TestGCDeletesExpiredSessionsAndIsIdempotent(t *testing.T) {
	f := &fakeHost{}
	f.install(t)
	for range 2 {
		if err := gcCron(sdk.CronEvent{Name: "gc"}); err != nil {
			t.Fatal(err)
		}
	}
	requireGuard(t, f)
	calls := f.sqlCalls()
	if len(calls) != 2 || calls[0].sql != "DELETE FROM sessions WHERE expires_at < now()" || calls[1].sql != calls[0].sql || len(calls[0].args) != 0 {
		t.Fatalf("calls %+v", calls)
	}
}

func TestGCReturnsDatabaseErrors(t *testing.T) {
	(&fakeHost{db: func(hostCall, any) error { return errors.New("db down") }}).install(t)
	if err := gcCron(sdk.CronEvent{Name: "gc"}); err == nil {
		t.Fatal("a failed cleanup must not look like success")
	}
}
