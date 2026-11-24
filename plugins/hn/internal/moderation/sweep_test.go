package moderation

import (
	"errors"
	"testing"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func TestSweep(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	t.Run("three deletes in order", func(t *testing.T) {
		db := &fakeDB{}
		install(t, db)
		if err := sweep(sdk.CronEvent{}); err != nil {
			t.Fatal(err)
		}
		want := []string{sqlSweepNetBans, sqlSweepRate, sqlSweepModLog}
		if len(db.Execs) != 3 {
			t.Fatalf("execs = %+v", db.Execs)
		}
		for i, w := range want {
			if db.Execs[i].SQL != w {
				t.Errorf("exec %d = %q", i, db.Execs[i].SQL)
			}
		}
	})

	t.Run("a failed delete does not stop the others", func(t *testing.T) {
		db := &fakeDB{Exec: func(sql string, _ []any) (int64, error) {
			if sql == sqlSweepNetBans {
				return 0, errors.New("boom")
			}
			return 1, nil
		}}
		install(t, db)
		if err := sweep(sdk.CronEvent{}); err == nil || len(db.Execs) != 3 {
			t.Fatalf("err = %v, execs = %d", err, len(db.Execs))
		}
	})

	t.Run("no host returns nil", func(t *testing.T) {
		prev := core.Call
		core.Call = nil
		t.Cleanup(func() { core.Call = prev })
		if err := sweep(sdk.CronEvent{}); err != nil {
			t.Fatal(err)
		}
	})
}
