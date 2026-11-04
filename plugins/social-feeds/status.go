package main

import (
	"net/http"
	"time"

	"hatamoto.dev/sdk"
)

// refreshNowBudget keeps a manual refresh inside the route time limit.
const refreshNowBudget = 8 * time.Second

type instagramStatus struct {
	Configured     bool   `json:"configured"`
	Connected      bool   `json:"connected"`
	Expired        bool   `json:"expired"`
	Username       string `json:"username"`
	TokenExpiresAt string `json:"token_expires_at"`
	RefreshedAt    string `json:"refreshed_at"`
	Error          string `json:"error"`
	Count          int    `json:"count"`
}

type youtubeStatus struct {
	Configured  bool   `json:"configured"`
	ChannelID   string `json:"channel_id"`
	RefreshedAt string `json:"refreshed_at"`
	Error       string `json:"error"`
	Count       int    `json:"count"`
}

type statusDoc struct {
	Instagram   instagramStatus     `json:"instagram"`
	YouTube     youtubeStatus       `json:"youtube"`
	RedirectURI string              `json:"redirect_uri"`
	Missing     map[string][]string `json:"missing"`
	Items       map[string][]item   `json:"items"`
	Error       string              `json:"error,omitempty"`
	Refresh     map[string]string   `json:"refresh,omitempty"`
}

func (s settings) missingForYouTube() []string {
	var m []string
	if s.YouTubeKey == "" {
		m = append(m, "youtube_api_key")
	}
	if s.ChannelID == "" {
		m = append(m, "youtube_channel_id")
	}
	return m
}

// buildStatus reads the cached state only (no call leaves the plugin). It
// never fails: a read error becomes an empty list plus a redacted error text.
// refresh is the outcome of a run that just happened, if any.
func buildStatus(s settings, refresh map[string]string) statusDoc {
	doc := statusDoc{
		RedirectURI: s.redirectURI(),
		Missing: map[string][]string{
			"instagram": s.missingForConnect(),
			"youtube":   s.missingForYouTube(),
		},
		Items:   map[string][]item{"instagram": {}, "youtube": {}},
		Refresh: refresh,
	}
	doc.Instagram.Configured = len(doc.Missing["instagram"]) == 0
	doc.YouTube.Configured = len(doc.Missing["youtube"]) == 0
	doc.YouTube.ChannelID = s.ChannelID

	var problems []string
	note := func(err error) {
		problems = append(problems, redact(err.Error(), s.AppSecret, s.YouTubeKey))
	}

	tok, err := loadToken()
	if err != nil {
		note(err)
	}
	if tok != nil {
		doc.Instagram.Connected = true
		doc.Instagram.Username = tok.Username
		if !tok.ExpiresAt.IsZero() {
			doc.Instagram.TokenExpiresAt = tok.ExpiresAt.UTC().Format(time.RFC3339)
			doc.Instagram.Expired = !tok.ExpiresAt.After(now())
		}
	}

	for _, src := range []string{"instagram", "youtube"} {
		rows, err := listItems(src, maxStored)
		if err != nil {
			note(err)
		}
		st, err := getStatus(src)
		if err != nil {
			note(err)
		}
		count := len(rows)
		rows = rows[:min(len(rows), s.Items)]
		if rows == nil {
			rows = []item{}
		}
		doc.Items[src] = rows
		if src == "instagram" {
			doc.Instagram.Count, doc.Instagram.RefreshedAt, doc.Instagram.Error = count, st.RefreshedAt, st.Error
		} else {
			doc.YouTube.Count, doc.YouTube.RefreshedAt, doc.YouTube.Error = count, st.RefreshedAt, st.Error
		}
	}
	if len(problems) > 0 {
		doc.Error = problems[0]
	}
	return doc
}

// disconnect forgets the Instagram connection and its cached posts.
func disconnect(s settings) (sdk.HTTPResponse, error) {
	if err := deleteToken(); err != nil {
		return fail(err)
	}
	if err := clearItems("instagram"); err != nil {
		return fail(err)
	}
	if err := clearStatus("instagram"); err != nil {
		return fail(err)
	}
	logf("info", "disconnected from Instagram")
	return reply(http.StatusOK, map[string]bool{"ok": true})
}
