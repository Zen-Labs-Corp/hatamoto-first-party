package parity

import (
	"encoding/xml"
	"strconv"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	Comments    string  `xml:"comments"`
	GUID        rssGUID `xml:"guid"`
	PubDate     string  `xml:"pubDate"`
	Description string  `xml:"description,omitempty"`
}

type rssGUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

// feedLink is the page of a feed: best lives at /news?view=best, the rest at /<feed>.
func feedLink(feed string) string {
	if feed == "best" {
		return "/news?view=best"
	}
	return "/" + feed
}

// handleRSS is GET /rss?feed=...: the first page of a feed as RSS 2.0. The
// plugin does not know the site address, so links are root-relative.
func handleRSS(q *core.Request) (sdk.HTTPResponse, error) {
	feed := firstQuery(q, "feed")
	if feed == "" {
		feed = "news"
	}
	rows, _, ok, err := loadPub(feed, 1)
	if !ok {
		return core.JSONError(404, "not found"), nil
	}
	if err != nil {
		return core.JSONError(503, "unavailable"), nil
	}
	doc := rssDoc{Version: "2.0", Channel: rssChannel{
		Title: "Hatamoto News: " + feed, Link: feedLink(feed), Description: "Stories from the " + feed + " feed", Items: []rssItem{},
	}}
	for _, s := range rows {
		no := strconv.FormatInt(s.ItemNo, 10)
		link := "/item/" + no
		if s.URL != "" {
			link = s.URL
		}
		doc.Channel.Items = append(doc.Channel.Items, rssItem{
			Title: s.Title, Link: link, Comments: "/item/" + no,
			GUID: rssGUID{IsPermaLink: "false", Value: "item-" + no}, PubDate: s.CreatedAt.UTC().Format(time.RFC1123Z), Description: s.Body,
		})
	}
	body, err := xml.Marshal(doc)
	if err != nil {
		return core.JSONError(500, "internal"), nil
	}
	return sdk.HTTPResponse{Status: 200, Body: xml.Header + string(body), Headers: map[string]string{
		"Content-Type": "application/rss+xml; charset=utf-8", "Cache-Control": "public, max-age=60",
	}}, nil
}
