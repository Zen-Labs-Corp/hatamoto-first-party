package parity

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

var procNow = mustTime("2026-11-25T12:00:00Z")

func tp(d time.Duration) *time.Time {
	t := procNow.Add(-d)
	return &t
}

func TestNoprocrastDecide(t *testing.T) {
	min := time.Minute
	for _, tc := range []struct {
		name              string
		v                 *visit
		allow, window     bool
		comeBack          int
		maxvisit, minaway int
	}{
		{"no row", nil, true, true, 0, 20, 180},
		{"null window", &visit{}, true, true, 0, 20, 180},
		{"away exactly minaway", &visit{WindowStart: tp(300 * min), LastSeen: tp(180 * min)}, true, true, 0, 20, 180},
		{"inside the window", &visit{WindowStart: tp(5 * min), LastSeen: tp(1 * min)}, true, false, 0, 20, 180},
		{"one minute before the limit", &visit{WindowStart: tp(19 * min), LastSeen: tp(1 * min)}, true, false, 0, 20, 180},
		{"exactly maxvisit", &visit{WindowStart: tp(20 * min), LastSeen: tp(1 * min)}, false, false, 179, 20, 180},
		{"over the limit", &visit{WindowStart: tp(25 * min), LastSeen: tp(1 * min)}, false, false, 179, 20, 180},
		{"partial minute rounds up", &visit{WindowStart: tp(25 * min), LastSeen: tp(90 * time.Second)}, false, false, 179, 20, 180},
		{"last minute of away", &visit{WindowStart: tp(200 * min), LastSeen: tp(179*min + 30*time.Second)}, false, false, 1, 20, 180},
		{"clamped settings", &visit{WindowStart: tp(2 * min), LastSeen: tp(30 * time.Second)}, false, false, 1, 0, 0},
	} {
		allow, window, back := decide(procNow, tc.v, tc.maxvisit, tc.minaway)
		if allow != tc.allow || window != tc.window || back != tc.comeBack {
			t.Errorf("%s: got %v %v %d, want %v %v %d", tc.name, allow, window, back, tc.allow, tc.window, tc.comeBack)
		}
	}
}

// procDB has k1 with noprocrast on and the given visits row ("" = none).
func procDB(row string, flag bool) *fakeDB {
	u := user(1, "alice")
	u["noprocrast"] = flag
	db := &fakeDB{Users: map[string]map[string]any{"k1": u}}
	db.Query = func(sql string, _ []any) (any, error) {
		if sql == sqlVisit && row != "" {
			return map[string]any{"rows": [][]any{{row}}}, nil
		}
		return map[string]any{"rows": [][]any{}}, nil
	}
	return db
}

func visitsJSON(window, seen time.Duration) string {
	return `{"window_start":"` + tp(window).Format(time.RFC3339) + `","last_seen":"` + tp(seen).Format(time.RFC3339) + `"}`
}

func procBlocks() []sdk.Block {
	return []sdk.Block{{ID: "hn_userbar", Type: "hn_userbar"}, {ID: "b7", Type: "hn_feed"}}
}

func TestNoprocrastBlocksPastTheWindow(t *testing.T) {
	pinNow(t, procNow)
	db := procDB(visitsJSON(25*time.Minute, time.Minute), true)
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/news", "k1", nil, procBlocks()...)
	if len(out.Blocks) != 2 || out.Blocks[0].Type != "hn_userbar" || out.Blocks[1].ID != "hn_noprocrast" || out.Blocks[1].Type != "hn_saved" {
		t.Fatalf("blocks %+v", out.Blocks)
	}
	c := out.Blocks[1].Content
	if c["procrast"] != true || c["maxvisit"] != "20" || c["minaway"] != "179" || c["profile_href"] != "/user?id=alice" {
		t.Errorf("content %v", c)
	}
	if len(db.Execs) != 0 {
		t.Errorf("blocked view wrote %v", db.Execs)
	}
	// the notice renders through the shared section
	html := run(t, "section-hn_saved", c)
	if !strings.Contains(html, "Come back in 179 minutes") {
		t.Errorf("notice: %s", html)
	}
}

func TestNoprocrastAllowsInsideTheWindow(t *testing.T) {
	pinNow(t, procNow)
	db := procDB(visitsJSON(5*time.Minute, time.Minute), true)
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/news", "k1", nil, procBlocks()...)
	if len(out.Blocks) != 2 || out.Blocks[1].ID != "b7" {
		t.Fatalf("blocks %+v", out.Blocks)
	}
	if len(db.Execs) != 1 || db.Execs[0].SQL != sqlVisitSeen || db.Execs[0].Args[0] != float64(1) || db.Execs[0].Args[1] != "2026-11-25T12:00:00Z" {
		t.Errorf("execs %v", db.Execs)
	}
}

func TestNoprocrastStartsAWindow(t *testing.T) {
	pinNow(t, procNow)
	for name, row := range map[string]string{"no row": "", "away long enough": visitsJSON(400*time.Minute, 200*time.Minute)} {
		db := procDB(row, true)
		install(t, db)
		r := newRegistry(t, nil)
		out := render(t, r, "/news", "k1", nil, procBlocks()...)
		if len(out.Blocks) != 2 || out.Blocks[1].ID != "b7" {
			t.Fatalf("%s: blocks %+v", name, out.Blocks)
		}
		if len(db.Execs) != 1 || db.Execs[0].SQL != sqlVisitStart || db.Execs[0].Args[1] != "2026-11-25T12:00:00Z" {
			t.Errorf("%s: execs %v", name, db.Execs)
		}
	}
}

func TestNoprocrastLeavesOthersAlone(t *testing.T) {
	pinNow(t, procNow)
	over := visitsJSON(25*time.Minute, time.Minute)

	db := procDB(over, false)
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/news", "k1", nil, procBlocks()...)
	if len(out.Blocks) != 2 || out.Blocks[1].ID != "b7" || len(listVisits(db)) != 0 {
		t.Errorf("flag off: %+v, queries %v", out.Blocks, db.Queries)
	}

	db = procDB(over, true)
	h := install(t, db)
	for _, path := range []string{"/user", "/login"} {
		out = render(t, r, path, "k1", nil, procBlocks()...)
		if len(out.Blocks) != 2 || out.Blocks[1].ID != "b7" {
			t.Errorf("%s: %+v", path, out.Blocks)
		}
	}
	if len(db.Execs) != 0 || len(listVisits(db)) != 0 {
		t.Errorf("exempt paths touched visits: %v %v", db.Execs, db.Queries)
	}

	// anonymous: no host call at all
	h.Calls = nil
	out = render(t, r, "/news", "", nil, procBlocks()...)
	if len(out.Blocks) != 2 || out.Blocks[1].ID != "b7" || len(h.Calls) != 0 {
		t.Errorf("anonymous: %+v, calls %v", out.Blocks, h.Calls)
	}
}

func listVisits(db *fakeDB) (out []sqlCall) {
	for _, q := range db.Queries {
		if q.SQL == sqlVisit {
			out = append(out, q)
		}
	}
	return out
}

func TestNoprocrastFailsOpen(t *testing.T) {
	pinNow(t, procNow)
	db := procDB("", true)
	db.Query = func(string, []any) (any, error) { return nil, hostErr("db_error") }
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/news", "k1", nil, procBlocks()...)
	if len(out.Blocks) != 2 || out.Blocks[1].ID != "b7" || len(db.Execs) != 0 {
		t.Errorf("blocks %+v execs %v", out.Blocks, db.Execs)
	}
}
