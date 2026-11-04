package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const allSettings = `{"instagram_app_id":"123","instagram_app_secret":"APPSECRET","youtube_api_key":"YTKEYSECRET","youtube_channel_id":"UC1","public_url":"https://example.com","items":2}`

func decodeStatus(t *testing.T, body string) statusDoc {
	t.Helper()
	var d statusDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("status is not json: %v\n%s", err, body)
	}
	return d
}

func TestStatusEmptyStateIsOK(t *testing.T) {
	f := useFake(t)
	res := send(t, "GET", "/status", nil, nil)
	if res.Status != 200 {
		t.Fatalf("status = %d %s", res.Status, res.Body)
	}
	d := decodeStatus(t, res.Body)
	if d.Instagram.Configured || d.Instagram.Connected || d.YouTube.Configured {
		t.Errorf("empty setup reported as set up: %+v", d)
	}
	if len(d.Missing["instagram"]) != 3 || len(d.Missing["youtube"]) != 2 {
		t.Errorf("missing = %v", d.Missing)
	}
	if len(d.Items["instagram"]) != 0 || len(d.Items["youtube"]) != 0 || d.Error != "" {
		t.Errorf("items/error = %v %q", d.Items, d.Error)
	}
	if f.fetchCount() != 0 {
		t.Errorf("status made %d http calls", f.fetchCount())
	}
	if !strings.Contains(res.Body, `"instagram":[]`) {
		t.Errorf("items must be empty arrays, not null: %s", res.Body)
	}
}

func TestStatusReportsStateWithoutSecrets(t *testing.T) {
	f := useFake(t)
	f.settings = allSettings
	exp := f.clock.Add(30 * 24 * time.Hour)
	if err := saveToken(igToken{AccessToken: "TOKENSECRET", UserID: "9", Username: "shop", ObtainedAt: f.clock, ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	f.items = []item{
		{Source: "instagram", ID: "a", Title: "A", PublishedAt: "2026-01-01T00:00:00Z"},
		{Source: "instagram", ID: "b", Title: "B", PublishedAt: "2026-01-02T00:00:00Z"},
		{Source: "instagram", ID: "c", Title: "C", PublishedAt: "2026-01-03T00:00:00Z"},
	}
	f.status["instagram"] = sourceStatus{Source: "instagram", RefreshedAt: "2026-01-02T10:00:00Z", Error: "boom"}

	res := send(t, "GET", "/status", nil, nil)
	if res.Status != 200 {
		t.Fatalf("status = %d", res.Status)
	}
	for _, secret := range []string{"TOKENSECRET", "APPSECRET", "YTKEYSECRET"} {
		if strings.Contains(res.Body, secret) {
			t.Errorf("response contains %s", secret)
		}
	}
	d := decodeStatus(t, res.Body)
	ig := d.Instagram
	if !ig.Configured || !ig.Connected || ig.Username != "shop" || ig.Count != 3 || ig.Error != "boom" || ig.RefreshedAt != "2026-01-02T10:00:00Z" {
		t.Errorf("instagram = %+v", ig)
	}
	if ig.Expired {
		t.Error("a token with time left must not be expired")
	}
	if ig.TokenExpiresAt != exp.Format(time.RFC3339) {
		t.Errorf("expiry = %q", ig.TokenExpiresAt)
	}
	if got := d.Items["instagram"]; len(got) != 2 || got[0].ID != "c" {
		t.Errorf("items capped by the setting, newest first: %+v", got)
	}
	if !d.YouTube.Configured || d.YouTube.ChannelID != "UC1" {
		t.Errorf("youtube = %+v", d.YouTube)
	}
	if d.RedirectURI != "https://example.com/p/social-feeds/oauth/callback" {
		t.Errorf("redirect = %q", d.RedirectURI)
	}
}

func TestStatusSurvivesDatabaseFailure(t *testing.T) {
	f := useFake(t)
	f.settings = allSettings
	f.failDB = "source_status"
	f.status["instagram"] = sourceStatus{Source: "instagram"}
	res := send(t, "GET", "/status", nil, nil)
	if res.Status != 200 {
		t.Fatalf("status = %d", res.Status)
	}
	d := decodeStatus(t, res.Body)
	if d.Error == "" {
		t.Error("a database failure should be reported in error")
	}
}

func TestDisconnectForgetsTokenAndInstagramRows(t *testing.T) {
	f := useFake(t)
	f.settings = allSettings
	_ = saveToken(igToken{AccessToken: "TOKENSECRET"})
	f.items = []item{{Source: "instagram", ID: "a"}, {Source: "youtube", ID: "v"}}
	f.status["instagram"] = sourceStatus{Source: "instagram", Error: "x"}
	f.status["youtube"] = sourceStatus{Source: "youtube"}

	res := send(t, "POST", "/disconnect", nil, nil)
	if res.Status != 200 || res.Body != `{"ok":true}` {
		t.Fatalf("disconnect = %d %s", res.Status, res.Body)
	}
	if _, ok := f.kv[tokenKey]; ok {
		t.Error("token still stored")
	}
	if len(f.rows("instagram")) != 0 || len(f.rows("youtube")) != 1 {
		t.Errorf("rows = %+v", f.items)
	}
	if _, ok := f.status["instagram"]; ok {
		t.Error("instagram status kept")
	}
	if _, ok := f.status["youtube"]; !ok {
		t.Error("youtube status lost")
	}
	d := decodeStatus(t, send(t, "GET", "/status", nil, nil).Body)
	if d.Instagram.Connected {
		t.Error("still connected after disconnect")
	}
}

func TestRefreshNowRunsTheRefresh(t *testing.T) {
	f := useFake(t)
	f.settings = allSettings
	f.on("https://www.googleapis.com/youtube/v3/channels", 200, `{"items":[{"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}}}]}`)
	f.on("https://www.googleapis.com/youtube/v3/playlistItems", 200,
		`{"items":[{"snippet":{"title":"Video","publishedAt":"2026-01-01T00:00:00Z","resourceId":{"videoId":"v1"},"thumbnails":{"high":{"url":"https://i.ytimg.com/v1.jpg"}}}}]}`)

	res := send(t, "POST", "/refresh-now", nil, nil)
	if res.Status != 200 {
		t.Fatalf("refresh-now = %d %s", res.Status, res.Body)
	}
	if f.fetchCount() != 2 || len(f.rows("youtube")) != 1 {
		t.Errorf("fetches=%d rows=%+v", f.fetchCount(), f.items)
	}
	d := decodeStatus(t, res.Body)
	if d.YouTube.Count != 1 || d.Refresh["youtube"] != "ok" || d.Refresh["instagram"] != "skipped" {
		t.Errorf("doc = %+v", d)
	}
	if strings.Contains(res.Body, "YTKEYSECRET") {
		t.Error("response contains the API key")
	}
}

func TestRefreshNowWithNothingConfigured(t *testing.T) {
	f := useFake(t)
	res := send(t, "POST", "/refresh-now", nil, nil)
	if res.Status != 200 || f.fetchCount() != 0 {
		t.Fatalf("status=%d fetches=%d", res.Status, f.fetchCount())
	}
}

func TestStatusFlagsAnExpiredToken(t *testing.T) {
	f := useFake(t)
	f.settings = igSettings
	if err := saveToken(igToken{AccessToken: "OLD", ObtainedAt: f.clock.Add(-61 * 24 * time.Hour), ExpiresAt: f.clock.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if ig := decodeStatus(t, send(t, "GET", "/status", nil, nil).Body).Instagram; !ig.Connected || !ig.Expired {
		t.Fatalf("instagram = %+v", ig)
	}
}
