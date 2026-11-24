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
		t.Run("db error passes", func(t *testing.T) {
			install(t, &fakeDB{Query: func(string, []any) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }})
			if err := r.RunGates(core.GateVote, gateReq(core.GateVote, member(10), "n", "a", "")); err != nil {
				t.Fatalf("err = %v", err)
			}
		})
	})

	t.Run("signup", func(t *testing.T) {
		answer := func(banned bool, n int) *fakeDB {
			return &fakeDB{Query: func(sql string, _ []any) (any, error) {
				if sql != sqlSignupCheck {
					t.Errorf("unexpected query %q", sql)
				}
				return jsonRow(map[string]any{"net": banned, "net_n": n}), nil
			}}
		}
		install(t, answer(true, 0))
		wantRefusal(t, r.RunGates(core.GateSignup, gateReq(core.GateSignup, nil, "net1", "", "")), nNet)
		install(t, answer(false, 5))
		wantRefusal(t, r.RunGates(core.GateSignup, gateReq(core.GateSignup, nil, "net1", "", "")), nSignupRate)
		db := answer(false, 4)
		install(t, db)
		if err := r.RunGates(core.GateSignup, gateReq(core.GateSignup, nil, "net1", "", "")); err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(db.Execs) != 1 || db.Execs[0].SQL != sqlRateRecord {
			t.Fatalf("execs = %+v", db.Execs)
		}
		t.Run("no net key passes without a host call", func(t *testing.T) {
			h := install(t, &fakeDB{})
			if err := r.RunGates(core.GateSignup, gateReq(core.GateSignup, nil, "", "", "")); err != nil || len(h.Calls) != 0 {
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
	for in, want := range map[string]string{"WWW.A.b.Example.com.": "a.b.example.com", " example.com ": "example.com", "": ""} {
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
