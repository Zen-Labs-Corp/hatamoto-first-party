package moderation

import (
	"strconv"
	"strings"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	sqlSubmitCheck = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM bans WHERE user_id = $1 AND mode = 'blocked') AS blocked, EXISTS(SELECT 1 FROM banned_domains d JOIN jsonb_array_elements_text($2::jsonb) h(host) ON h.host = d.domain OR h.host LIKE '%.' || d.domain) AS domain, EXISTS(SELECT 1 FROM net_bans WHERE net_key = $3 AND (expires_at IS NULL OR expires_at > $4::timestamptz)) AS net) x`
	sqlVoteCheck   = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM bans WHERE user_id = $1 AND mode = 'blocked') AS blocked, (SELECT count(*) FROM rate_events WHERE kind = 'vote_user' AND key = $2 AND created_at > $5::timestamptz - interval '1 minute') AS user_n, (SELECT count(*) FROM rate_events WHERE kind = 'vote_addr' AND key = $3 AND created_at > $5::timestamptz - interval '1 minute') AS addr_n, (SELECT count(*) FROM rate_events WHERE kind = 'vote_net' AND key = $4 AND created_at > $5::timestamptz - interval '1 minute') AS net_n, EXISTS(SELECT 1 FROM net_bans WHERE net_key = $4 AND (expires_at IS NULL OR expires_at > $5::timestamptz)) AS net, COALESCE((SELECT item_no FROM story_meta WHERE entity_id::text = $6), 0) AS story_no, (SELECT count(*) FROM rate_events WHERE kind = 'vote_ring' AND key = $7 AND created_at > $5::timestamptz - interval '24 hours') AS ring_n) x`
	sqlSignupCheck = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM net_bans WHERE net_key = $1 AND (expires_at IS NULL OR expires_at > $3::timestamptz)) AS net, (SELECT count(*) FROM rate_events WHERE kind = 'signup_net' AND key = $1 AND created_at > $3::timestamptz - interval '1 hour') AS net_n, (SELECT count(*) FROM rate_events WHERE kind = 'signup_addr' AND key = $2 AND created_at > $3::timestamptz - interval '1 hour') AS addr_n) x`
	sqlLoginCheck  = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM net_bans WHERE net_key = $3 AND (expires_at IS NULL OR expires_at > $4::timestamptz)) AS net, (SELECT count(*) FROM rate_events WHERE kind = 'login_user' AND key = $1 AND created_at > $4::timestamptz - interval '1 hour') AS user_n, (SELECT count(*) FROM rate_events WHERE kind = 'login_user_all' AND key = $5 AND created_at > $4::timestamptz - interval '1 hour') AS user_all_n, (SELECT count(*) FROM rate_events WHERE kind = 'login_addr' AND key = $2 AND created_at > $4::timestamptz - interval '1 hour') AS addr_n, (SELECT count(*) FROM rate_events WHERE kind = 'login_net' AND key = $3 AND created_at > $4::timestamptz - interval '1 hour') AS net_n) x`
	sqlMemberCheck = `SELECT to_jsonb(x) FROM (SELECT count(*) AS n FROM rate_events WHERE kind = 'pw_member' AND key = $1 AND created_at > $2::timestamptz - interval '1 hour') x`
	sqlBlocked     = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM bans WHERE user_id = $1 AND mode = 'blocked') AS blocked) x`
	sqlRateRecord  = `INSERT INTO rate_events (kind, key, created_at) SELECT e->>'kind', e->>'key', $1::timestamptz FROM jsonb_array_elements($2::jsonb) e`
)

func registerGates(r *core.Registry) {
	r.Gate(core.GateSubmit, gateSubmit)
	r.Gate(core.GateVote, gateVote)
	r.Gate(core.GateSignup, gateSignup)
	r.Gate(core.GateFlag, gateFlag)
	r.Gate(core.GateLogin, gateLogin)
	r.Gate(core.GateMember, gateMember)
}

func nowArg() string { return core.Now().UTC().Format(time.RFC3339Nano) }

// netKey is the network key of the request, "" when there is none.
func netKey(req *core.GateReq) string {
	if req.Req == nil {
		return ""
	}
	return req.Req.NetKey
}

func addrKey(req *core.GateReq) string {
	if req.Req == nil {
		return ""
	}
	return req.Req.AddrKey
}

func userID(req *core.GateReq) int64 {
	if req.User == nil {
		return 0
	}
	return req.User.ID
}

// domainHost is a host in the form banned_domains is matched in: lower-case punycode,
// without "www." and a trailing dot. The SQL matches the host and its parents,
// so the match does not depend on how many labels the host has.
func domainHost(d string) string {
	d = strings.TrimSpace(d)
	if a, ok := core.ASCIIHost(d); ok {
		d = a
	} else {
		d = strings.ToLower(d)
	}
	d = strings.TrimSuffix(d, ".")
	return strings.TrimPrefix(d, "www.")
}

// gateSubmit refuses blocked members, banned sites (and their subdomains) and
// banned networks. It cannot tell an edit from a submit and does not count
// submissions: stories owns submit_per_hour. Host errors fail open.
func gateSubmit(req *core.GateReq) error {
	var doms []string
	if h := domainHost(req.Domain); h != "" {
		doms = []string{h}
	}
	if req.User == nil && len(doms) == 0 && netKey(req) == "" {
		return nil
	}
	var res struct{ Blocked, Domain, Net bool }
	if err := core.QueryJSON(sqlSubmitCheck, &res, userID(req), doms, netKey(req), nowArg()); err != nil {
		return nil
	}
	switch {
	case res.Blocked:
		return refusal{nBlocked}
	case res.Domain:
		return refusal{nDomain}
	case res.Net:
		return refusal{nNet}
	}
	return nil
}

// gateVote refuses blocked members, banned networks and floods per member, address and network
// (attempts count, so every vote that gets this far is recorded).
func gateVote(req *core.GateReq) error {
	var res struct {
		Blocked bool
		UserN   int `json:"user_n"`
		AddrN   int `json:"addr_n"`
		NetN    int `json:"net_n"`
		Net     bool
		StoryNo int64 `json:"story_no"`
		RingN   int   `json:"ring_n"`
	}
	user, addr, net := "", addrKey(req), netKey(req)
	if req.User != nil {
		user = strconv.FormatInt(req.User.ID, 10)
	}
	if user == "" && addr == "" && net == "" {
		return nil
	}
	ring := ""
	if net != "" && req.Target != "" {
		ring = net + ":" + req.Target
	}
	now := nowArg()
	if err := core.QueryJSON(sqlVoteCheck, &res, userID(req), user, addr, net, now, req.Target, ring); err != nil {
		return nil
	}
	if res.Blocked {
		return refusal{nBlocked}
	}
	if res.Net {
		return refusal{nNet}
	}
	userOver, addrOver, netOver := res.UserN >= voteUserPerMin, res.AddrN >= voteAddrPerMin, res.NetN >= voteNetPerMin
	// A key already over its limit stays over without more rows, so a flood
	// does not grow rate_events.
	rateRefused := (user != "" && userOver) || (addr != "" && addrOver) || (net != "" && netOver)
	// The ring check only marks a vote the rate limits let through: the caller
	// stores a marked story vote without counting it, the voter is told nothing.
	ringOver := res.RingN >= ringVotesPerNet
	recordRates(now, "vote_user", keyBelow(user, userOver), "vote_addr", keyBelow(addr, addrOver), "vote_net", keyBelow(net, netOver))
	if rateRefused {
		return refusal{nVoteRate}
	}
	if res.StoryNo > 0 && ring != "" {
		// Only a vote that is really stored uses the network's budget: a double click, a
		// repeat vote or an unvote and revote must not (the caller reports it).
		req.Ring = ringOver
		req.Stored = func(counted bool) {
			if counted {
				recordRates(now, "vote_ring", ring)
				return
			}
			prefix := net
			if len(prefix) > 8 {
				prefix = prefix[:8]
			}
			writeLog("ring", "ring-vote", "story:"+strconv.FormatInt(res.StoryNo, 10), "net:"+prefix)
		}
	}
	return nil
}

// gateSignup runs for a visitor without a username: only the network and the address are known.
func gateSignup(req *core.GateReq) error {
	net, addr := netKey(req), addrKey(req)
	netLimit := signupNetPerHour
	if net == "" && addr == "" {
		// No client.key grant: one site-wide bucket, so a flood still has a ceiling.
		net, netLimit = siteKey, signupSitePerHour
	}
	var res struct {
		Net   bool
		NetN  int `json:"net_n"`
		AddrN int `json:"addr_n"`
	}
	now := nowArg()
	if err := core.QueryJSON(sqlSignupCheck, &res, net, addr, now); err != nil {
		return nil
	}
	if res.Net {
		return refusal{nNet}
	}
	if (net != "" && res.NetN >= netLimit) || (addr != "" && res.AddrN >= signupAddrPerHour) {
		return refusal{nSignupRate}
	}
	recordRates(now, "signup_net", net, "signup_addr", addr)
	return nil
}

// gateLogin runs before a password is checked (login and password change). Target is the
// lower-case username when it is a valid name, whether or not it exists, else "". Per-key limits
// count attempts; the all-networks and keyless caps count wrong passwords only (Failed). The
// refusal never depends on whether the username exists.
func gateLogin(req *core.GateReq) error {
	user, addr, net := req.Target, addrKey(req), netKey(req)
	netLimit := loginNetPerHour
	keyless := addr == "" && net == ""
	if keyless {
		// No client.key grant: one site-wide bucket, so a flood still has a ceiling.
		net, netLimit = siteKey, loginSitePerHour
	}
	var res struct {
		Net      bool
		UserN    int `json:"user_n"`
		UserAllN int `json:"user_all_n"`
		AddrN    int `json:"addr_n"`
		NetN     int `json:"net_n"`
	}
	// A name is limited per network, so a third party cannot lock a member out of
	// their own network; the all-networks cap is only a brute-force backstop.
	userKey := ""
	if user != "" {
		userKey = user + ":" + net
		if net == "" {
			userKey = user + ":" + addr
		}
	}
	now := nowArg()
	if err := core.QueryJSON(sqlLoginCheck, &res, userKey, addr, net, now, user); err != nil {
		if keyless {
			return refusal{nLoginRate} // no per-client limit to fall back on: fail closed
		}
		return nil
	}
	if res.Net {
		return refusal{nNet}
	}
	userOver, allOver := res.UserN >= loginUserPerHour, res.UserAllN >= loginUserAllPerHour
	addrOver, netOver := res.AddrN >= loginAddrPerHour, res.NetN >= netLimit
	refused := (user != "" && (userOver || allOver)) || (addr != "" && addrOver) || (net != "" && netOver)
	recordRates(now, "login_user", keyBelow(userKey, userOver), "login_addr", keyBelow(addr, addrOver), "login_net", keyBelow(net, netOver))
	if refused {
		// A refused request adds nothing to the all-networks cap: one address cannot fill it.
		return refusal{nLoginRate}
	}
	req.Failed = func() { recordRates(now, "login_user_all", user) }
	return nil
}

// gateMember limits the password routes of a signed-in member per member, in a bucket of its
// own: the login limits are not touched, so nobody else's traffic can block the member's
// profile forms. Target is the member's id. A host error fails open (a session is needed anyway).
func gateMember(req *core.GateReq) error {
	if req.Target == "" {
		return nil
	}
	var res struct{ N int }
	now := nowArg()
	if err := core.QueryJSON(sqlMemberCheck, &res, req.Target, now); err != nil {
		return nil
	}
	if res.N >= memberPwPerHour {
		return refusal{nLoginRate}
	}
	recordRates(now, "pw_member", req.Target)
	return nil
}

// gateFlag refuses members below flag_karma and blocked members. A signed-out
// visitor is the route's business.
func gateFlag(req *core.GateReq) error {
	if req.User == nil {
		return nil
	}
	if req.User.Karma < core.LoadSettings().FlagKarma {
		return refusal{nFlagKarma}
	}
	var res struct{ Blocked bool }
	if err := core.QueryJSON(sqlBlocked, &res, req.User.ID); err == nil && res.Blocked {
		return refusal{nBlocked}
	}
	return nil
}

// keyBelow is key, or "" when the key is over its limit and needs no new row.
func keyBelow(key string, over bool) string {
	if over {
		return ""
	}
	return key
}

// recordRates stores one rate_events row per non-empty key; pairs are kind, key.
// A failure is ignored (fail open).
func recordRates(now string, pairs ...string) {
	var rows []map[string]string
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			rows = append(rows, map[string]string{"kind": pairs[i], "key": pairs[i+1]})
		}
	}
	if len(rows) == 0 {
		return
	}
	_, _ = core.Exec(sqlRateRecord, now, rows)
}
