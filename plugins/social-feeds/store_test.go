package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReplaceItemsSwapsOnlyThatSource(t *testing.T) {
	f := useFake(t)
	if err := replaceItems("instagram", []item{{ID: "a", Position: 0}, {ID: "b", Position: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := replaceItems("youtube", []item{{ID: "y", Position: 0}}); err != nil {
		t.Fatal(err)
	}
	if err := replaceItems("instagram", []item{{ID: "c", Position: 0}}); err != nil {
		t.Fatal(err)
	}
	ig := f.rows("instagram")
	if len(ig) != 1 || ig[0].ID != "c" || ig[0].Source != "instagram" {
		t.Fatalf("instagram rows = %+v", ig)
	}
	if yt := f.rows("youtube"); len(yt) != 1 || yt[0].ID != "y" {
		t.Fatalf("youtube rows = %+v", yt)
	}
}

func TestReplaceItemsChunksLargeBatches(t *testing.T) {
	f := useFake(t)
	var in []item
	for i := 0; i < 1200; i++ {
		in = append(in, item{ID: fmt.Sprintf("id-%d", i), Position: i})
	}
	if err := replaceItems("instagram", in); err != nil {
		t.Fatal(err)
	}
	if got := len(f.rows("instagram")); got != 1200 {
		t.Fatalf("rows = %d", got)
	}
}

func TestListItemsEmptyAndOrdered(t *testing.T) {
	useFake(t)
	got, err := listItems("instagram", 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty db: %v %v", got, err)
	}
	_ = replaceItems("instagram", []item{
		{ID: "old", Position: 0, PublishedAt: "2025-01-01T00:00:00Z"},
		{ID: "new", Position: 1, PublishedAt: "2025-06-01T00:00:00Z"},
		{ID: "undated", Position: 2},
	})
	got, err = listItems("instagram", 2)
	if err != nil || len(got) != 2 || got[0].ID != "new" || got[1].ID != "old" {
		t.Fatalf("list = %+v %v", got, err)
	}
}

func TestStatusSuccessThenFailureKeepsRefreshedAt(t *testing.T) {
	f := useFake(t)
	if err := setStatus("instagram", "", true); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Hour)
	if err := setStatus("instagram", strings.Repeat("e", 500), false); err != nil {
		t.Fatal(err)
	}
	st, err := getStatus("instagram")
	if err != nil {
		t.Fatal(err)
	}
	if st.RefreshedAt != "2026-01-02T12:00:00Z" || len(st.Error) != maxErrorLen {
		t.Fatalf("status = %+v", st)
	}
	if st, _ := getStatus("youtube"); st != (sourceStatus{}) {
		t.Fatalf("unknown source status = %+v", st)
	}
}

func TestTokenRoundTripAndDelete(t *testing.T) {
	f := useFake(t)
	if tok, err := loadToken(); tok != nil || err != nil {
		t.Fatalf("empty kv: %v %v", tok, err)
	}
	want := igToken{AccessToken: "tok", UserID: "7", Username: "shop", ObtainedAt: f.clock, ExpiresAt: f.clock.Add(60 * 24 * time.Hour)}
	if err := saveToken(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadToken()
	if err != nil || got == nil || got.AccessToken != "tok" || got.Username != "shop" || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("token = %+v %v", got, err)
	}
	if f.kv[tokenKey].TTL != 0 {
		t.Fatal("token must not expire on its own")
	}
	if err := deleteToken(); err != nil {
		t.Fatal(err)
	}
	if tok, _ := loadToken(); tok != nil {
		t.Fatal("token survived delete")
	}
}

func TestClearItems(t *testing.T) {
	f := useFake(t)
	_ = replaceItems("instagram", []item{{ID: "a"}})
	_ = replaceItems("youtube", []item{{ID: "b"}})
	if err := clearItems("instagram"); err != nil {
		t.Fatal(err)
	}
	if len(f.rows("instagram")) != 0 || len(f.rows("youtube")) != 1 {
		t.Fatalf("rows = %+v", f.items)
	}
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	got := truncate(strings.Repeat("é", 2000), 2048)
	if len(got) > 2048 || len(got)%2 != 0 || strings.ToValidUTF8(got, "?") != got {
		t.Fatalf("len %d", len(got))
	}
}
