package moderation

import (
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func checkWith(doc map[string]any) *fakeDB {
	return &fakeDB{Query: func(string, []any) (any, error) { return jsonRow(doc), nil }}
}

func TestCheck(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	in := sdk.CommentCheck{Body: "hi", CommenterKey: "k1", AreaKey: "area1", LinkDomains: []string{"www.Blog.Spam.com"}, Score: 7}

	cases := []struct {
		name, reason string
		doc          map[string]any
	}{
		{"blocked member", "hn: blocked member", map[string]any{"blocked": true}},
		{"net", "hn: blocked network", map[string]any{"net": true}},
		{"domain", "hn: banned site", map[string]any{"domain": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			install(t, checkWith(c.doc))
			out, err := checkComment(in)
			if err != nil || out.Score != sdk.CommentCheckMaxScore {
				t.Fatalf("score = %d, err = %v", out.Score, err)
			}
			if len(out.Reasons) != 1 || out.Reasons[0] != c.reason {
				t.Fatalf("reasons = %v", out.Reasons)
			}
		})
	}

	t.Run("clean input unchanged, score kept", func(t *testing.T) {
		install(t, checkWith(map[string]any{}))
		out, err := checkComment(in)
		if err != nil || out.Score != 7 || len(out.Reasons) != 0 {
			t.Fatalf("out = %+v, err = %v", out, err)
		}
	})

	t.Run("sends the www-stripped suffixes", func(t *testing.T) {
		db := checkWith(map[string]any{})
		install(t, db)
		_, _ = checkComment(in)
		if len(db.Queries) != 1 || db.Queries[0].SQL != sqlCheckComment {
			t.Fatalf("queries = %+v", db.Queries)
		}
		if !sameList(db.Queries[0].Args[3], "blog.spam.com", "spam.com", "com") {
			t.Fatalf("domains = %v", db.Queries[0].Args[3])
		}
	})

	t.Run("no keys and no domains: no host call", func(t *testing.T) {
		h := install(t, &fakeDB{})
		out, err := checkComment(sdk.CommentCheck{Body: "hi", Score: 3})
		if err != nil || out.Score != 3 || len(h.Calls) != 0 {
			t.Fatalf("out = %+v, err = %v, calls = %v", out, err, h.Calls)
		}
	})

	t.Run("db error: input unchanged, no error", func(t *testing.T) {
		install(t, &fakeDB{Query: func(string, []any) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }})
		out, err := checkComment(in)
		if err != nil || out.Score != 7 || len(out.Reasons) != 0 {
			t.Fatalf("out = %+v, err = %v", out, err)
		}
	})

	t.Run("a higher incoming score is kept", func(t *testing.T) {
		install(t, checkWith(map[string]any{"blocked": true}))
		hi := in
		hi.Score = 100
		if out, _ := checkComment(hi); out.Score != 100 {
			t.Fatalf("score = %d", out.Score)
		}
	})
}
