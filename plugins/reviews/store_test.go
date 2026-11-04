package main

import (
	"testing"
	"time"
)

func rv(src, id string, rating int, day int) review {
	return review{Source: src, ExtID: id, Author: "A " + id, Rating: rating, Body: "text " + id,
		Published: time.Date(2026, 10, day, 9, 0, 0, 0, time.UTC)}
}

func TestReplaceSourceReplacesOnlyThatSource(t *testing.T) {
	f := newFake(t)
	f.seed(t, sourceGoogle, 4.8, 132, rv("google", "g1", 5, 1), rv("google", "g2", 4, 2))
	f.seed(t, sourceTrustpilot, 4.6, 210, rv("trustpilot", "t1", 5, 3))
	f.seed(t, sourceGoogle, 4.9, 140, rv("google", "g2", 5, 2), rv("google", "g3", 5, 4))

	rs, err := loadReviews()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range rs {
		ids = append(ids, r.ExtID)
	}
	if len(ids) != 3 || ids[0] != "g3" || ids[1] != "t1" || ids[2] != "g2" {
		t.Fatalf("ids newest first = %v", ids)
	}
	if rs[2].Rating != 5 {
		t.Fatalf("g2 not updated: %+v", rs[2])
	}
	src, err := loadSources()
	if err != nil {
		t.Fatal(err)
	}
	if src[sourceGoogle].Rating != 4.9 || src[sourceGoogle].Count != 140 || src[sourceTrustpilot].Count != 210 {
		t.Fatalf("sources = %+v", src)
	}
	if !src[sourceGoogle].OKAt.Equal(f.clock) {
		t.Fatalf("ok_at %v", src[sourceGoogle].OKAt)
	}
}

func TestRecordFailureKeepsRows(t *testing.T) {
	f := newFake(t)
	f.seed(t, sourceGoogle, 4.8, 132, rv("google", "g1", 5, 1))
	f.clock = f.clock.Add(time.Hour)
	if err := recordFailure(sourceGoogle, "401 invalid key"); err != nil {
		t.Fatal(err)
	}
	rs, _ := loadReviews()
	src, _ := loadSources()
	if len(rs) != 1 || src[sourceGoogle].LastError != "401 invalid key" || src[sourceGoogle].Count != 132 {
		t.Fatalf("rows %d, source %+v", len(rs), src[sourceGoogle])
	}
	if !src[sourceGoogle].AttemptedAt.Equal(f.clock) || src[sourceGoogle].OKAt.Equal(f.clock) {
		t.Fatalf("attempted %v ok %v", src[sourceGoogle].AttemptedAt, src[sourceGoogle].OKAt)
	}
}

func TestDeleteSourceAndDbDown(t *testing.T) {
	f := newFake(t)
	f.seed(t, sourceGoogle, 4.8, 1, rv("google", "g1", 5, 1))
	f.seed(t, sourceTrustpilot, 4.6, 1, rv("trustpilot", "t1", 5, 1))
	if err := deleteSource(sourceGoogle); err != nil {
		t.Fatal(err)
	}
	rs, _ := loadReviews()
	src, _ := loadSources()
	if len(rs) != 1 || rs[0].Source != sourceTrustpilot || len(src) != 1 {
		t.Fatalf("rows %+v sources %+v", rs, src)
	}
	f.dbDown = true
	if _, err := loadReviews(); err == nil {
		t.Fatal("db error must surface")
	}
}
