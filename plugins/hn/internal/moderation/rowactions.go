package moderation

import (
	"net/url"
	"strconv"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const rowSettingsTTL = 10 * time.Second

// rowCache holds the settings of the page being rendered: row actions run once
// per row and must not call the host each time.
var rowCache struct {
	v  *core.User
	at time.Time
	s  core.Settings
}

func rowSettings(v *core.User) core.Settings {
	now := core.Now()
	if rowCache.v != v || now.Sub(rowCache.at) > rowSettingsTTL {
		rowCache.v, rowCache.at, rowCache.s = v, now, core.LoadSettings()
	}
	return rowCache.s
}

// rowActions offers vouch on a dead item and flag on a live one, decided from
// the viewer's karma alone (no query per row). Whether a dead item may be
// vouched for is the route's decision.
func rowActions(v *core.User, it core.Item) []core.Action {
	if v == nil || it.AuthorID == v.ID {
		return nil
	}
	id := it.ID
	if it.Kind == kindStory {
		id = strconv.FormatInt(it.ItemNo, 10)
	}
	if id == "" || id == "0" {
		return nil
	}
	s := rowSettings(v)
	verb, class := "flag", "hn-flag"
	if it.Dead {
		if v.Karma < s.VouchKarma {
			return nil
		}
		verb, class = "vouch", "hn-vouch"
	} else if v.Karma < s.FlagKarma {
		return nil
	}
	href := "/p/hn/" + verb + "/" + it.Kind + "/" + id
	if it.ItemNo > 0 {
		href += "?next=" + url.QueryEscape("/item/"+strconv.FormatInt(it.ItemNo, 10))
	}
	return []core.Action{{Label: verb, Href: href, Class: class, Post: true}}
}
