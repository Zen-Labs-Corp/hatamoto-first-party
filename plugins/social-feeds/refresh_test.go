package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	igSecretToken = "IGQVsecretTOKEN"
	ytSecretKey   = "AIzaSECRETKEY"
	igMedia       = "https://graph.instagram.com/me/media"
	ytChannels    = "https://www.googleapis.com/youtube/v3/channels"
	ytPlaylist    = "https://www.googleapis.com/youtube/v3/playlistItems"
)

func connect(t *testing.T, f *fake) {
	t.Helper()
	if err := saveToken(igToken{AccessToken: igSecretToken, UserID: "7", ObtainedAt: f.clock, ExpiresAt: f.clock.Add(1000 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func igBody(n int, prefix string) string {
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf(`{"id":"%s%d","caption":"Post %d","media_type":"IMAGE","media_url":"https://cdn.example/%s%d.jpg","permalink":"https://www.instagram.com/p/%s%d/","timestamp":"2025-03-01T10:%02d:00+0000"}`, prefix, i, i, prefix, i, prefix, i, i%60))
	}
	return `{"data":[` + strings.Join(parts, ",") + `]}`
}

func setupYouTube(f *fake, videos string) {
	f.settings = `{"youtube_api_key":"` + ytSecretKey + `","youtube_channel_id":"UC123"}`
	f.on(ytChannels, 200, `{"items":[{"contentDetails":{"relatedPlaylists":{"uploads":"UU123"}}}]}`)
	f.on(ytPlaylist, 200, videos)
}

const ytVideos = `{"items":[
 {"snippet":{"title":"Launch day","description":"All about it","publishedAt":"2025-05-02T08:00:00Z","resourceId":{"videoId":"vid1"},"thumbnails":{"default":{"url":"https://i.example/d.jpg"},"high":{"url":"https://i.example/h.jpg"}}}},
 {"snippet":{"title":"Deleted video","description":"","publishedAt":"2025-05-01T08:00:00Z","resourceId":{"videoId":"gone"}}},
 {"snippet":{"title":"Private video","description":"","publishedAt":"2025-04-01T08:00:00Z","resourceId":{"videoId":"priv"}}},
 {"snippet":{"title":"Second","description":"","publishedAt":"2025-04-02T08:00:00Z","resourceId":{"videoId":"vid2"},"thumbnails":{"maxres":{"url":"https://i.example/m.jpg"}}}}
]}`

func TestInstagramImageAndVideoThumbnails(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	f.on(igMedia, 200, `{"data":[
	 {"id":"1","caption":"A photo","media_type":"IMAGE","media_url":"https://cdn.example/photo.jpg","thumbnail_url":"https://cdn.example/ignored.jpg","permalink":"https://www.instagram.com/p/1/","timestamp":"2025-03-01T10:00:00+0000"},
	 {"id":"2","caption":"A reel","media_type":"VIDEO","media_url":"https://cdn.example/reel.mp4","thumbnail_url":"https://cdn.example/reel.jpg","permalink":"https://www.instagram.com/reel/2/","timestamp":"2025-03-02T10:00:00+0000"}]}`)
	if got := runRefresh(time.Second)["instagram"]; got != "ok" {
		t.Fatalf("result = %q", got)
	}
	rows := f.rows("instagram")
	if len(rows) != 2 || rows[0].Image != "https://cdn.example/photo.jpg" || rows[1].Image != "https://cdn.example/reel.jpg" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[1].Type != "video" || rows[0].PublishedAt != "2025-03-01T10:00:00Z" || rows[0].Title != "A photo" {
		t.Fatalf("row fields = %+v", rows)
	}
	if !strings.Contains(f.fetches[0].URL, "access_token="+igSecretToken) {
		t.Fatalf("token missing from the request: %s", f.fetches[0].URL)
	}
}

func TestInstagramKeepsAtMostFifty(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	f.on(igMedia, 200, igBody(60, "a"))
	runRefresh(time.Second)
	if got := len(f.rows("instagram")); got != 50 {
		t.Fatalf("rows = %d", got)
	}
}

func TestSecondRunReplacesRows(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	f.on(igMedia, 200, igBody(3, "old"))
	runRefresh(time.Second)
	f.on(igMedia, 200, igBody(2, "new"))
	runRefresh(time.Second)
	rows := f.rows("instagram")
	if len(rows) != 2 || !strings.HasPrefix(rows[0].ID, "new") || !strings.HasPrefix(rows[1].ID, "new") {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestInstagramErrorKeepsYouTubeAndOldRowsWithoutLeaks(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	setupYouTube(f, ytVideos)
	f.on(igMedia, 200, igBody(2, "old"))
	runRefresh(time.Second)

	f.fail(igMedia, errors.New(`Get "`+igMedia+`?access_token=`+igSecretToken+`&limit=50": timeout`))
	res := runRefresh(time.Second)
	if res["youtube"] != "ok" || res["instagram"] == "ok" {
		t.Fatalf("result = %v", res)
	}
	if len(f.rows("youtube")) != 2 || len(f.rows("instagram")) != 2 {
		t.Fatalf("rows ig=%d yt=%d", len(f.rows("instagram")), len(f.rows("youtube")))
	}
	st, _ := getStatus("instagram")
	if st.Error == "" || st.RefreshedAt == "" {
		t.Fatalf("status = %+v", st)
	}
	assertNoSecrets(t, f, st.Error, res["instagram"])

	f.on(igMedia, 401, `{"error":{"message":"Invalid token `+igSecretToken+`"}}`)
	res = runRefresh(time.Second)
	st, _ = getStatus("instagram")
	if !strings.Contains(st.Error, "401") {
		t.Fatalf("status = %+v", st)
	}
	assertNoSecrets(t, f, st.Error, res["instagram"])
}

func assertNoSecrets(t *testing.T, f *fake, texts ...string) {
	t.Helper()
	all := strings.Join(append(texts, f.logs...), "\n")
	for _, secret := range []string{igSecretToken, ytSecretKey} {
		if strings.Contains(all, secret) {
			t.Fatalf("secret %q leaked in %q", secret, all)
		}
	}
}

func TestYouTubeHappyPathSkipsDeletedAndPrivate(t *testing.T) {
	f := useFake(t)
	setupYouTube(f, ytVideos)
	res := runRefresh(time.Second)
	if res["youtube"] != "ok" || res["instagram"] != "skipped" {
		t.Fatalf("result = %v", res)
	}
	rows := f.rows("youtube")
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].ID != "vid1" || rows[0].URL != "https://www.youtube.com/watch?v=vid1" || rows[0].Image != "https://i.example/h.jpg" ||
		rows[0].PublishedAt != "2025-05-02T08:00:00Z" || rows[1].Image != "https://i.example/m.jpg" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestYouTubeErrorsNeverLeakTheKey(t *testing.T) {
	f := useFake(t)
	setupYouTube(f, ytVideos)
	f.fail(ytChannels, errors.New("Get "+ytChannels+"?id=UC123&key="+ytSecretKey+": dial failed"))
	res := runRefresh(time.Second)
	st, _ := getStatus("youtube")
	if res["youtube"] == "ok" || st.Error == "" {
		t.Fatalf("result = %v status = %+v", res, st)
	}
	assertNoSecrets(t, f, st.Error, res["youtube"])

	f.settings = `{"youtube_api_key":"` + ytSecretKey + `","youtube_channel_id":"UC123"}`
	f.on(ytChannels, 200, `{"items":[]}`)
	runRefresh(time.Second)
	if st, _ := getStatus("youtube"); !strings.Contains(st.Error, "channel") {
		t.Fatalf("status = %+v", st)
	}
}

func TestNoSettingsMakesNoCalls(t *testing.T) {
	f := useFake(t)
	res := runRefresh(time.Second)
	if res["instagram"] != "skipped" || res["youtube"] != "skipped" || f.fetchCount() != 0 || len(f.items) != 0 {
		t.Fatalf("result = %v fetches = %d", res, f.fetchCount())
	}
}

func TestDBFailureOnReplaceKeepsErrorAndNoLeak(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	f.on(igMedia, 200, igBody(2, "a"))
	f.failDB = "INSERT INTO item"
	res := runRefresh(time.Second)
	if res["instagram"] == "ok" {
		t.Fatalf("result = %v", res)
	}
}

func TestFailedReplaceKeepsTheOldRows(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	f.on(igMedia, 200, igBody(2, "a"))
	if got := runRefresh(time.Second)["instagram"]; got != "ok" {
		t.Fatalf("first run = %q", got)
	}
	f.on(igMedia, 200, igBody(3, "b"))
	f.failDB = "INSERT INTO item"
	if got := runRefresh(time.Second)["instagram"]; got == "ok" {
		t.Fatal("second run should fail")
	}
	if rows := f.rows("instagram"); len(rows) != 2 || rows[0].ID != "a0" {
		t.Fatalf("old rows lost: %+v", rows)
	}
}

func TestReplaceStripsNULAndInvalidUTF8(t *testing.T) {
	f := useFake(t)
	err := replaceItems("youtube", []item{{ID: "v1", Title: "a\x00b", Excerpt: "bad\xffbyte", Type: "video"}})
	rows := f.rows("youtube")
	if err != nil || len(rows) != 1 || rows[0].Title != "ab" || rows[0].Excerpt != "badbyte" {
		t.Fatalf("err = %v rows = %+v", err, rows)
	}
}

func TestCronRefreshNeverReturnsAnError(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	f.on(igMedia, 500, `{}`)
	in, _ := json.Marshal(sdk.CronEvent{Name: "refresh"})
	if _, err := guest.Handle(guest.CronExport("refresh"), in); err != nil {
		t.Fatalf("cron failed: %v", err)
	}
	if st, _ := getStatus("instagram"); st.Error == "" {
		t.Fatal("failure not recorded")
	}
}

func TestRefreshRenewsADayOldToken(t *testing.T) {
	f := useFake(t)
	saveTokenAged(t, f, 25*time.Hour)
	f.on(graphURL+"refresh_access_token", 200, `{"access_token":"NEWTOKEN","expires_in":5184000}`)
	f.on(igMedia, 200, igBody(1, "a"))
	if got := runRefresh(time.Second)["instagram"]; got != "ok" {
		t.Fatalf("result = %q", got)
	}
	if tok, _ := loadToken(); tok == nil || tok.AccessToken != "NEWTOKEN" {
		t.Fatalf("token = %+v", tok)
	}
	if last := f.fetches[len(f.fetches)-1].URL; !strings.Contains(last, "access_token=NEWTOKEN") {
		t.Fatalf("media fetched with the old token: %s", last)
	}
}

func TestInstagramNeverUsesAVideoFileAsImage(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	f.on(igMedia, 200, `{"data":[
	 {"id":"1","media_type":"VIDEO","media_url":"https://cdn.example/reel.mp4","permalink":"https://www.instagram.com/reel/1/"},
	 {"id":"2","media_type":"CAROUSEL_ALBUM","media_url":"https://cdn.example/cover.mp4?sig=1","permalink":"https://www.instagram.com/p/2/"},
	 {"id":"3","media_type":"CAROUSEL_ALBUM","media_url":"https://cdn.example/cover.jpg?sig=1","permalink":"https://www.instagram.com/p/3/"}]}`)
	runRefresh(time.Second)
	rows := f.rows("instagram")
	if len(rows) != 3 || rows[0].Image != "" || rows[1].Image != "" || rows[2].Image != "https://cdn.example/cover.jpg?sig=1" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestExcerptsAreCutWhenStored(t *testing.T) {
	f := useFake(t)
	connect(t, f)
	long := strings.Repeat("é", 3000)
	f.on(igMedia, 200, `{"data":[{"id":"1","caption":"`+long+`","media_type":"IMAGE","media_url":"https://cdn.example/a.jpg"}]}`)
	setupYouTube(f, `{"items":[{"snippet":{"title":"T","description":"`+long+`","resourceId":{"videoId":"v"}}}]}`)
	runRefresh(time.Second)
	for _, src := range []string{"instagram", "youtube"} {
		rows := f.rows(src)
		if len(rows) != 1 || len(rows[0].Excerpt) > maxFieldBytes || len(rows[0].Excerpt) < maxFieldBytes-1 {
			t.Fatalf("%s rows = %d, excerpt %d bytes", src, len(rows), len(rows[0].Excerpt))
		}
	}
}
