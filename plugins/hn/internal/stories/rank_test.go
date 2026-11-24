package stories

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func TestRankCronSQL(t *testing.T) {
	pinNow(t, voteNow)
	db := &fakeDB{}
	install(t, db)
	if err := newRegistry(t, nil).CronHandler("rank")(sdk.CronEvent{Name: "rank"}); err != nil {
		t.Fatal(err)
	}
	if len(db.Execs) != 2 {
		t.Fatalf("execs %v", execSQL(db))
	}
	up := db.Execs[0]
	for _, want := range []string{"interval '7 days'", "NOT dead", "kind <> 'job'", "power(", "1.8"} {
		if !strings.Contains(up.SQL, want) {
			t.Errorf("update lacks %q: %s", want, up.SQL)
		}
	}
	if up.Args[0] != "2026-01-02T12:00:00Z" {
		t.Errorf("args %v", up.Args)
	}
	if del := db.Execs[1].SQL; !strings.Contains(del, "DELETE FROM item_numbers") || !strings.Contains(del, "NOT EXISTS") {
		t.Errorf("sweep: %s", del)
	}
}

func TestRankCronNoHost(t *testing.T) {
	prev := core.Call
	core.Call = nil
	t.Cleanup(func() { core.Call = prev })
	if err := newRegistry(t, nil).CronHandler("rank")(sdk.CronEvent{Name: "rank"}); err != nil {
		t.Errorf("no host: %v", err)
	}
}

func TestRankCronExecError(t *testing.T) {
	pinNow(t, voteNow)
	boom := errors.New("boom")
	db := &fakeDB{Exec: func(string, []any) (int64, error) { return 0, boom }}
	install(t, db)
	if err := newRegistry(t, nil).CronHandler("rank")(sdk.CronEvent{Name: "rank"}); err == nil {
		t.Error("error not returned")
	}
	if len(db.Execs) != 1 {
		t.Errorf("execs %v", execSQL(db))
	}
}

func TestRankTieOrder(t *testing.T) {
	got := scoreSQL("points", "$1")
	for _, want := range []string{"(points - 1)", "power(", "/ 3600.0 + 2, 1.8)", "$1::timestamptz - created_at"} {
		if !strings.Contains(got, want) {
			t.Errorf("scoreSQL lacks %q: %s", want, got)
		}
	}
}
