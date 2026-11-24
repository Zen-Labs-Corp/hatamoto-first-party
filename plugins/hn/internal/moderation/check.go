package moderation

import (
	"strings"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const sqlCheckComment = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM bans b JOIN users u ON u.id = b.user_id WHERE u.commenter_key = $1 AND b.mode = 'blocked') AS blocked, EXISTS(SELECT 1 FROM net_bans WHERE net_key = $2 AND (expires_at IS NULL OR expires_at > $3::timestamptz)) AS net, EXISTS(SELECT 1 FROM banned_domains WHERE domain IN (SELECT jsonb_array_elements_text($4::jsonb))) AS domain) x`

const maxLinkSuffixes = 50

// storySchema is the entity schema this plugin owns; comment.check runs for
// every schema of the site and must leave the others alone.
const storySchema = "hn-story"

// linkSuffixes lists the banned-domain candidates of the linked domains: each
// domain and its parents, without duplicates, at most maxLinkSuffixes.
func linkSuffixes(domains []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range domains {
		for _, s := range domainSuffixes(d) {
			if !seen[s] && len(out) < maxLinkSuffixes {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// checkComment holds a comment of a blocked member, from a banned network or
// linking a banned site, by raising the spam score to the maximum. It never
// lowers the score, never returns an error and fails open.
func checkComment(in sdk.CommentCheck) (sdk.CommentCheck, error) {
	if in.Schema != "" && in.Schema != storySchema {
		return in, nil
	}
	doms := linkSuffixes(in.LinkDomains)
	if in.CommenterKey == "" && in.AreaKey == "" && len(doms) == 0 {
		return in, nil
	}
	var res struct{ Blocked, Net, Domain bool }
	if err := core.QueryJSON(sqlCheckComment, &res, in.CommenterKey, in.AreaKey, nowArg(), doms); err != nil {
		return in, nil
	}
	var reasons []string
	if res.Blocked {
		reasons = append(reasons, "hn: blocked member")
	}
	if res.Net {
		reasons = append(reasons, "hn: blocked network")
	}
	if res.Domain {
		reasons = append(reasons, "hn: banned site")
	}
	if len(reasons) == 0 {
		return in, nil
	}
	if in.Score < sdk.CommentCheckMaxScore {
		in.Score = sdk.CommentCheckMaxScore
	}
	in.Reasons = append(in.Reasons[:len(in.Reasons):len(in.Reasons)], strings.Join(reasons, ", "))
	return in, nil
}
