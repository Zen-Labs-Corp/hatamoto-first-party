package main

import (
	"encoding/json"
	"errors"
	"net/url"
)

const youtubeAPI = "https://www.googleapis.com/youtube/v3/"

func youtubeGet(path string, q url.Values, out any) error {
	res, err := fetch("GET", youtubeAPI+path+"?"+q.Encode(), nil, "")
	if err != nil {
		return err
	}
	if !ok2xx(res.Status) {
		return apiError(res.Status, res.Body)
	}
	if err := json.Unmarshal([]byte(res.Body), out); err != nil {
		return errors.New("YouTube answered with something unreadable")
	}
	return nil
}

// fetchYouTube returns the latest uploads of the configured channel.
func fetchYouTube(s settings) ([]item, error) {
	var ch struct {
		Items []struct {
			ContentDetails struct {
				RelatedPlaylists struct {
					Uploads string `json:"uploads"`
				} `json:"relatedPlaylists"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	q := url.Values{"part": {"contentDetails"}, "id": {s.ChannelID}, "key": {s.YouTubeKey}}
	if err := youtubeGet("channels", q, &ch); err != nil {
		return nil, err
	}
	if len(ch.Items) == 0 || ch.Items[0].ContentDetails.RelatedPlaylists.Uploads == "" {
		return nil, errors.New("no such channel; check the channel ID")
	}
	var pl struct {
		Items []struct {
			Snippet struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				PublishedAt string `json:"publishedAt"`
				ResourceID  struct {
					VideoID string `json:"videoId"`
				} `json:"resourceId"`
				Thumbnails map[string]struct {
					URL string `json:"url"`
				} `json:"thumbnails"`
			} `json:"snippet"`
		} `json:"items"`
	}
	q = url.Values{"part": {"snippet"}, "playlistId": {ch.Items[0].ContentDetails.RelatedPlaylists.Uploads}, "maxResults": {"50"}, "key": {s.YouTubeKey}}
	if err := youtubeGet("playlistItems", q, &pl); err != nil {
		return nil, err
	}
	out := make([]item, 0, len(pl.Items))
	for _, v := range pl.Items {
		sn := v.Snippet
		if sn.ResourceID.VideoID == "" || sn.Title == "Private video" || sn.Title == "Deleted video" {
			continue
		}
		image := ""
		for _, size := range []string{"maxres", "standard", "high", "medium", "default"} {
			if t := sn.Thumbnails[size].URL; t != "" {
				image = t
				break
			}
		}
		out = append(out, item{
			ID:          sn.ResourceID.VideoID,
			Position:    len(out),
			Title:       sn.Title,
			Excerpt:     truncate(sn.Description, maxFieldBytes),
			URL:         "https://www.youtube.com/watch?v=" + url.QueryEscape(sn.ResourceID.VideoID),
			Image:       image,
			Type:        "video",
			PublishedAt: normalizeTime(sn.PublishedAt),
		})
	}
	return out, nil
}
