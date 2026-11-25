package moderation

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func gateReq(kind core.GateKind, u *core.User, net, addr, domain string) *core.GateReq {
	return &core.GateReq{Kind: kind, User: u, Domain: domain, Req: core.NewRequest(sdk.HTTPRequest{NetKey: net, AddrKey: addr})}
}

func member(karma int) *core.User { return &core.User{ID: 7, Username: "ann", Karma: karma} }

// wantRefusal checks err is a refusal with that code, whose notice text equals Error().
func wantRefusal(t *testing.T, err error, code core.Notice) {
	t.Helper()
	var n interface{ Notice() core.Notice }
	if !errors.As(err, &n) {
		t.Fatalf("err = %v, want a refusal with a notice", err)
	}
	if n.Notice() != code {
		t.Fatalf("notice = %q, want %q", n.Notice(), code)
	}
	if core.NoticeText(string(code)) == "" || err.Error() != core.NoticeText(string(code)) {
		t.Fatalf("Error() = %q, notice text = %q", err.Error(), core.NoticeText(string(code)))
	}
}

func TestGates(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	r := newRegistry(t)

	t.Run("submit", func(t *testing.T) {
		answer := func(doc map[string]any) *fakeDB {
			return &fakeDB{Query: func(sql string, _ []any) (any, error) {
				if sql != sqlSubmitCheck {
					t.Errorf("unexpected query %q", sql)
				}
				return jsonRow(doc), nil
			}}
		}
		cases := []struct {
			name string
			doc  map[string]any
			want core.Notice
		}{
			{"blocked", map[string]any{"blocked": true}, nBlocked},
			{"banned domain", map[string]any{"domain": true}, nDomain},
			{"net banned", map[string]any{"net": true}, nNet},
			{"clean", map[string]any{}, ""},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				h := install(t, answer(c.doc))
				err := r.RunGates(core.GateSubmit, gateReq(core.GateSubmit, member(10), "net1", "", "example.com"))
				if c.want == "" {
					if err != nil {
						t.Fatalf("err = %v", err)
					}
				} else {
					wantRefusal(t, err, c.want)
				}
				noEntityCalls(t, h)
			})
		}
		t.Run("the host is sent without www", func(t *testing.T) {
			db := answer(map[string]any{})
			install(t, db)
			_ = r.RunGates(core.GateSubmit, gateReq(core.GateSubmit, member(10), "", "", "www.Blog.Example.com"))
			if len(db.Queries) != 1 {
				t.Fatalf("%d queries", len(db.Queries))
			}
			if got := db.Queries[0].Args[1]; !sameList(got, "blog.example.com") {
				t.Fatalf("domains arg = %v", got)
			}
		})
		t.Run("a unicode host is sent as punycode", func(t *testing.T) {
			db := answer(map[string]any{})
			install(t, db)
			_ = r.RunGates(core.GateSubmit, gateReq(core.GateSubmit, member(10), "", "", "münchen-qa.example"))
			if got := db.Queries[0].Args[1]; !sameList(got, "xn--mnchen-qa-q9a.example") {
				t.Fatalf("domains arg = %v", got)
			}
		})
		t.Run("deep host is sent whole and matched by suffix in SQL", func(t *testing.T) {
			deep := "a.b.c.d.e.f.g.h.i.j.example.com"
			db := answer(map[string]any{})
			install(t, db)
			_ = r.RunGates(core.GateSubmit, gateReq(core.GateSubmit, member(10), "", "", deep+"."))
			if got := db.Queries[0].Args[1]; !sameList(got, deep) {
				t.Fatalf("domains arg = %v", got)
			}
			if !strings.Contains(sqlSubmitCheck, "h.host LIKE '%.' || d.domain") {
				t.Error("query does not match parent domains")
			}
		})
		t.Run("db error passes", func(t *testing.T) {
			install(t, &fakeDB{Query: func(string, []any) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }})
			if err := r.RunGates(core.GateSubmit, gateReq(core.GateSubmit, member(10), "n", "", "example.com")); err != nil {
				t.Fatalf("err = %v", err)
			}
		})
	})

	t.Run("vote", func(t *testing.T) {
		counts := func(blocked bool, u, a, n int) *fakeDB {
			return &fakeDB{Query: func(sql string, _ []any) (any, error) {
				if sql != sqlVoteCheck {
					t.Errorf("unexpected query %q", sql)
				}
				return jsonRow(map[string]any{"blocked": blocked, "user_n": u, "addr_n": a, "net_n": n}), nil
			}}
		}
		cases := []struct {
			name string
			db   *fakeDB
			want core.Notice
		}{
			{"blocked", counts(true, 0, 0, 0), nBlocked},
			{"user flood", counts(false, 30, 0, 0), nVoteRate},
			{"addr flood", counts(false, 0, 30, 0), nVoteRate},
			{"net flood", counts(false, 0, 0, 60), nVoteRate},
			{"below limits", counts(false, 29, 29, 59), ""},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				install(t, c.db)
				err := r.RunGates(core.GateVote, gateReq(core.GateVote, member(10), "net1", "addr1", ""))
				if c.want == "" {
					if err != nil {
						t.Fatalf("err = %v", err)
					}
				} else {
					wantRefusal(t, err, c.want)
				}
			})
		}
		t.Run("records rate rows", func(t *testing.T) {
			db := counts(false, 0, 0, 0)
			install(t, db)
			if err := r.RunGates(core.GateVote, gateReq(core.GateVote, member(10), "net1", "addr1", "")); err != nil {
				t.Fatal(err)
			}
			if len(db.Execs) != 1 || db.Execs[0].SQL != sqlRateRecord {
				t.Fatalf("execs = %+v", db.Execs)
			}
			rows, _ := db.Execs[0].Args[1].([]any)
			if len(rows) != 3 {
				t.Fatalf("rate rows = %v, want 3 (user, addr, net)", db.Execs[0].Args[1])
			}
		})
		t.Run("flood rows are not recorded", func(t *testing.T) {
			db := counts(false, 30, 30, 60)
			install(t, db)
			wantRefusal(t, r.RunGates(core.GateVote, gateReq(core.GateVote, member(10), "net1", "addr1", "")), nVoteRate)
			if len(db.Execs) != 0 {
				t.Fatalf("execs = %+v", db.Execs)
			}
			db = counts(false, 30, 0, 0)
			install(t, db)
			wantRefusal(t, r.RunGates(core.GateVote, gateReq(core.GateVote, member(10), "net1", "addr1", "")), nVoteRate)
			if rows, _ := db.Execs[0].Args[1].([]any); len(db.Execs) != 1 || len(rows) != 2 {
				t.Fatalf("execs = %+v", db.Execs)
			}
		})
		t.Run("db error passes", func(t *testing.T) {
			install(t, &fakeDB{Query: func(string, []any) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }})
			if err := r.RunGates(core.GateVote, gateReq(core.GateVote, member(10), "n", "a", "")); err != nil {
				t.Fatalf("err = %v", err)
			}
		})
	})

	t.Run("signup", func(t *testing.T) {
		answer := func(banned bool, netN, addrN int) *fakeDB {
			return &fakeDB{Query: func(sql string, _ []any) (any, error) {
				if sql != sqlSignupCheck {
					t.Errorf("unexpected query %q", sql)
				}
				return jsonRow(map[string]any{"net": banned, "net_n": netN, "addr_n": addrN}), nil
			}}
		}
		run := func(db *fakeDB) error {
			install(t, db)
			return r.RunGates(core.GateSignup, gateReq(core.GateSignup, nil, "net1", "addr1", ""))
		}
		wantRefusal(t, run(answer(true, 0, 0)), nNet)
		wantRefusal(t, run(answer(false, 5, 0)), nSignupRate)
		wantRefusal(t, run(answer(false, 0, 3)), nSignupRate)
		over := answer(false, 0, 3)
		_ = run(over)
		if len(over.Execs) != 0 {
			t.Fatalf("flood recorded: %+v", over.Execs)
		}
		db := answer(false, 4, 2)
		if err := run(db); err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(db.Execs) != 1 || db.Execs[0].SQL != sqlRateRecord {
			t.Fatalf("execs = %+v", db.Execs)
		}
		if rows, _ := db.Execs[0].Args[1].([]any); len(rows) != 2 {
			t.Fatalf("rate rows = %v, want signup_net and signup_addr", db.Execs[0].Args[1])
		}
		t.Run("no keys passes without a host call", func(t *testing.T) {
			h := install(t, &fakeDB{})
			if err := r.RunGates(core.GateSignup, gateReq(core.GateSignup, nil, "", "", "")); err != nil || len(h.Calls) != 0 {
				t.Fatalf("err = %v, calls = %v", err, h.Calls)
			}
		})
	})

	t.Run("login", func(t *testing.T) {
		counts := func(banned bool, userN, addrN, netN int) *fakeDB {
			return &fakeDB{Query: func(sql string, _ []any) (any, error) {
				if sql != sqlLoginCheck {
					t.Errorf("unexpected query %q", sql)
				}
				return jsonRow(map[string]any{"net": banned, "user_n": userN, "addr_n": addrN, "net_n": netN}), nil
			}}
		}
		run := func(db *fakeDB, target string) error {
			install(t, db)
			req := gateReq(core.GateLogin, nil, "net1", "addr1", "")
			req.Target = target
			return r.RunGates(core.GateLogin, req)
		}
		wantRefusal(t, run(counts(true, 0, 0, 0), "ann"), nNet)
		wantRefusal(t, run(counts(false, 10, 0, 0), "ann"), nLoginRate)
		wantRefusal(t, run(counts(false, 0, 20, 0), "ann"), nLoginRate)
		wantRefusal(t, run(counts(false, 0, 0, 60), "ann"), nLoginRate)
		db := counts(false, 9, 19, 59)
		if err := run(db, "ann"); err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(db.Execs) != 1 {
			t.Fatalf("execs = %+v", db.Execs)
		}
		kinds := ""
		rows, _ := db.Execs[0].Args[1].([]any)
		for _, row := range rows {
			m, _ := row.(map[string]any)
			kinds += m["kind"].(string) + " "
		}
		if kinds != "login_user login_addr login_net " {
			t.Fatalf("rate rows = %q", kinds)
		}
		t.Run("a key over its limit is not recorded", func(t *testing.T) {
			db := counts(false, 10, 0, 0)
			wantRefusal(t, run(db, "ann"), nLoginRate)
			rows, _ := db.Execs[0].Args[1].([]any)
			if len(db.Execs) != 1 || len(rows) != 2 {
				t.Fatalf("execs = %+v", db.Execs)
			}
		})
		t.Run("no username: no user row and no user refusal", func(t *testing.T) {
			db := counts(false, 99, 0, 0)
			if err := run(db, ""); err != nil {
				t.Fatalf("err = %v", err)
			}
			rows, _ := db.Execs[0].Args[1].([]any)
			if len(rows) != 2 {
				t.Fatalf("rate rows = %v, want addr and net only", db.Execs[0].Args[1])
			}
		})
		t.Run("db error passes", func(t *testing.T) {
			db := &fakeDB{Query: func(string, []any) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }}
			if err := run(db, "ann"); err != nil {
				t.Fatalf("err = %v", err)
			}
		})
		t.Run("no keys passes without a host call", func(t *testing.T) {
			h := install(t, &fakeDB{})
			if err := r.RunGates(core.GateLogin, gateReq(core.GateLogin, nil, "", "", "")); err != nil || len(h.Calls) != 0 {
				t.Fatalf("err = %v, calls = %v", err, h.Calls)
			}
		})
	})

	t.Run("flag", func(t *testing.T) {
		blocked := func(b bool) *fakeDB {
			return &fakeDB{Query: func(sql string, _ []any) (any, error) {
				if sql != sqlBlocked {
					t.Errorf("unexpected query %q", sql)
				}
				return jsonRow(map[string]any{"blocked": b}), nil
			}}
		}
		install(t, blocked(false))
		wantRefusal(t, r.RunGates(core.GateFlag, gateReq(core.GateFlag, member(29), "", "", "")), nFlagKarma)
		install(t, blocked(true))
		wantRefusal(t, r.RunGates(core.GateFlag, gateReq(core.GateFlag, member(30), "", "", "")), nBlocked)
		install(t, blocked(false))
		if err := r.RunGates(core.GateFlag, gateReq(core.GateFlag, member(30), "", "", "")); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDomainHost(t *testing.T) {
	for in, want := range map[string]string{"WWW.A.b.Example.com.": "a.b.example.com", "www.MÜNCHEN-qa.example.": "xn--mnchen-qa-q9a.example", " example.com ": "example.com", "": ""} {
		if got := domainHost(in); got != want {
			t.Errorf("domainHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func sameList(got any, want ...string) bool {
	l, ok := got.([]any)
	if !ok || len(l) != len(want) {
		return false
	}
	for i, w := range want {
		if l[i] != w {
			return false
		}
	}
	return true
}
