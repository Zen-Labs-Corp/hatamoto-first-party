package main

import "testing"

func TestEmbedFor(t *testing.T) {
	const yt = "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"
	const vm = "https://player.vimeo.com/video/123456"
	for _, c := range []struct{ in, video, zoom string }{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", yt, ""},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ&t=5", yt, ""},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", yt, ""},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", yt, ""},
		{"https://youtube.com/live/dQw4w9WgXcQ?feature=share", yt, ""},
		{"https://youtube-nocookie.com/embed/dQw4w9WgXcQ", yt, ""},
		{"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ", yt, ""},
		{"  https://WWW.YOUTUBE.COM/watch?v=dQw4w9WgXcQ ", yt, ""},
		{"https://player.vimeo.com/video/123456?h=abc", vm, ""},
		{"https://us02web.zoom.us/j/123", "", "https://us02web.zoom.us/j/123"},
		{"https://zoom.us/j/1", "", "https://zoom.us/j/1"},
		{"http://www.youtube.com/watch?v=dQw4w9WgXcQ", "", ""},
		{"https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ", "", ""},
		{"https://evilyoutube.com/watch?v=dQw4w9WgXcQ", "", ""},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "", ""},
		{"https://notzoom.us/j/1", "", ""},
		{"https://zoom.us.evil.example/j/1", "", ""},
		{"http://zoom.us/j/1", "", ""},
		{"https://vimeo.com/123456", "", ""},
		{"https://player.vimeo.com/video/abc", "", ""},
		{"https://youtu.be/dQw4w9WgXcQ", "", ""},
		{"https://user@www.youtube.com/watch?v=dQw4w9WgXcQ", "", ""},
		{"https://www.youtube.com:8443/watch?v=dQw4w9WgXcQ", "", ""},
		{"https://www.youtube.com/watch?v=short", "", ""},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQx", "", ""},
		{"https://www.youtube.com/watch?v=dQw4w9WgXc%22", "", ""},
		{"https://www.youtube.com/channel/x", "", ""},
		{"javascript:alert(1)", "", ""},
		{"", "", ""},
	} {
		if v, z := embedFor(c.in); v != c.video || z != c.zoom {
			t.Errorf("embedFor(%q) = %q, %q; want %q, %q", c.in, v, z, c.video, c.zoom)
		}
	}
}
