package main

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	ytIDRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	vimeoIDRe = regexp.MustCompile(`^[0-9]{1,12}$`)
)

// embedFor reads the event's embed link: video is an iframe src rebuilt from the
// id of an allowed YouTube or Vimeo URL (hosts match exactly), zoom is a Zoom
// link; both are empty for anything else.
func embedFor(raw string) (video, zoom string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	var id string
	switch host {
	case "youtube.com", "www.youtube.com":
		switch {
		case len(parts) == 1 && parts[0] == "watch":
			id = u.Query().Get("v")
		case len(parts) == 2 && (parts[0] == "embed" || parts[0] == "shorts" || parts[0] == "live"):
			id = parts[1]
		}
	case "youtube-nocookie.com", "www.youtube-nocookie.com":
		if len(parts) == 2 && parts[0] == "embed" {
			id = parts[1]
		}
	case "player.vimeo.com":
		if len(parts) == 2 && parts[0] == "video" && vimeoIDRe.MatchString(parts[1]) {
			return "https://player.vimeo.com/video/" + parts[1], ""
		}
		return "", ""
	default:
		if host == "zoom.us" || strings.HasSuffix(host, ".zoom.us") {
			return "", u.String()
		}
		return "", ""
	}
	if !ytIDRe.MatchString(id) {
		return "", ""
	}
	return "https://www.youtube-nocookie.com/embed/" + id, ""
}
