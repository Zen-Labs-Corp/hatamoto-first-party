package parity

import (
	"errors"
	"net/url"
	"strconv"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	sqlVisit      = "SELECT to_jsonb(x) FROM (SELECT v.window_start, v.last_seen FROM visits v WHERE v.user_id = $1) x"
	sqlVisitStart = "INSERT INTO visits (user_id, window_start, last_seen) VALUES ($1, $2::timestamptz, $2::timestamptz) ON CONFLICT (user_id) DO UPDATE SET window_start = EXCLUDED.window_start, last_seen = EXCLUDED.last_seen"
	sqlVisitSeen  = "UPDATE visits SET last_seen = $2::timestamptz WHERE user_id = $1"
)

// visit is a member's row of the visits table.
type visit struct {
	WindowStart *time.Time `json:"window_start"`
	LastSeen    *time.Time `json:"last_seen"`
}

// decide applies the noprocrast rule at time now. A member with no window, or
// who was away for minaway minutes or more, starts a new window. Inside a
// window of maxvisit minutes the page is allowed. Past it the page is blocked
// and comeBack is the minutes left until the away time is over.
func decide(now time.Time, v *visit, maxvisit, minaway int) (allow, newWindow bool, comeBack int) {
	maxv, mina := time.Duration(max(maxvisit, 1))*time.Minute, time.Duration(max(minaway, 1))*time.Minute
	if v == nil || v.WindowStart == nil || v.LastSeen == nil || now.Sub(*v.LastSeen) >= mina {
		return true, true, 0
	}
	if now.Sub(*v.WindowStart) < maxv {
		return true, false, 0
	}
	left := mina - now.Sub(*v.LastSeen)
	return false, false, max(int((left+time.Minute-1)/time.Minute), 1)
}

// noprocrast keeps a member with the flag off the site past their visit
// window: the page shrinks to the account bar and a notice. Blocked views do
// not write, so they do not reset the away clock. Anonymous pages make no
// host call, and a host failure leaves the page as it was.
func noprocrast(p *core.Page) error {
	if p.Ctx.CommenterKey == "" || p.Ctx.Path == "/user" || p.Ctx.Path == "/login" {
		return nil
	}
	v := p.Viewer()
	if v == nil || !v.Noprocrast {
		return nil
	}
	var row visit
	var vis *visit
	switch err := core.QueryJSON(sqlVisit, &row, v.ID); {
	case err == nil:
		vis = &row
	case errors.Is(err, core.ErrNoRows):
	default:
		return nil
	}
	now := core.Now()
	allow, newWindow, comeBack := decide(now, vis, v.Maxvisit, v.Minaway)
	switch {
	case allow && newWindow:
		_, _ = core.Exec(sqlVisitStart, v.ID, nowArg())
	case allow:
		_, _ = core.Exec(sqlVisitSeen, v.ID, nowArg())
	default:
		kept := []sdk.Block{}
		for _, b := range p.Ctx.Blocks {
			if b.Type == "hn_userbar" {
				kept = append(kept, b)
			}
		}
		p.Ctx.Blocks = append(kept, sdk.Block{ID: "hn_noprocrast", Type: "hn_saved", Content: map[string]any{
			"procrast": true, "maxvisit": strconv.Itoa(v.Maxvisit), "minaway": strconv.Itoa(comeBack), "profile_href": "/user?id=" + url.QueryEscape(v.Username),
		}})
	}
	return nil
}
