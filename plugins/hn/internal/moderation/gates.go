package moderation

import (
	"strconv"
	"strings"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	sqlSubmitCheck = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM bans WHERE user_id = $1 AND mode = 'blocked') AS blocked, EXISTS(SELECT 1 FROM banned_domains d JOIN jsonb_array_elements_text($2::jsonb) h(host) ON h.host = d.domain OR h.host LIKE '%.' || d.domain) AS domain, EXISTS(SELECT 1 FROM net_bans WHERE net_key = $3 AND (expires_at IS NULL OR expires_at > $4::timestamptz)) AS net) x`
	sqlVoteCheck   = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM bans WHERE user_id = $1 AND mode = 'blocked') AS blocked, (SELECT count(*) FROM rate_events WHERE kind = 'vote_user' AND key = $2 AND created_at > $5::timestamptz - interval '1 minute') AS user_n, (SELECT count(*) FROM rate_events WHERE kind = 'vote_addr' AND key = $3 AND created_at > $5::timestamptz - interval '1 minute') AS addr_n, (SELECT count(*) FROM rate_events WHERE kind = 'vote_net' AND key = $4 AND created_at > $5::timestamptz - interval '1 minute') AS net_n) x`
	sqlSignupCheck = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM net_bans WHERE net_key = $1 AND (expires_at IS NULL OR expires_at > $2::timestamptz)) AS net, (SELECT count(*) FROM rate_events WHERE kind = 'signup_net' AND key = $1 AND created_at > $2::timestamptz - interval '1 hour') AS net_n) x`
	sqlBlocked     = `SELECT to_jsonb(x) FROM (SELECT EXISTS(SELECT 1 FROM bans WHERE user_id = $1 AND mode = 'blocked') AS blocked) x`
	sqlRateRecord  = `INSERT INTO rate_events (kind, key, created_at) SELECT e->>'kind', e->>'key', $1::timestamptz FROM jsonb_array_elements($2::jsonb) e`
)

func registerGates(r *core.Registry) {
	r.Gate(core.GateSubmit, gateSubmit)
	r.Gate(core.GateVote, gateVote)
	r.Gate(core.GateSignup, gateSignup)
	r.Gate(core.GateFlag, gateFlag)
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

// domainHost is a host in the form banned_domains is matched in: lower-case,
// without "www." and a trailing dot. The SQL matches the host and its parents,
// so the match does not depend on how many labels the host has.
func domainHost(d string) string {
	d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
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

// gateVote refuses blocked members and floods per member, address and network
// (attempts count, so every vote that gets this far is recorded).
func gateVote(req *core.GateReq) error {
	var res struct {
		Blocked bool
		UserN   int `json:"user_n"`
		AddrN   int `json:"addr_n"`
		NetN    int `json:"net_n"`
	}
	user, addr, net := "", addrKey(req), netKey(req)
	if req.User != nil {
		user = strconv.FormatInt(req.User.ID, 10)
	}
	if user == "" && addr == "" && net == "" {
		return nil
	}
	now := nowArg()
	if err := core.QueryJSON(sqlVoteCheck, &res, userID(req), user, addr, net, now); err != nil {
		return nil
	}
	if res.Blocked {
		return refusal{nBlocked}
	}
	userOver, addrOver, netOver := res.UserN >= voteUserPerMin, res.AddrN >= voteAddrPerMin, res.NetN >= voteNetPerMin
	// A key already over its limit stays over without more rows, so a flood
	// does not grow rate_events.
	recordRates(now, "vote_user", keyBelow(user, userOver), "vote_addr", keyBelow(addr, addrOver), "vote_net", keyBelow(net, netOver))
	if (user != "" && userOver) || (addr != "" && addrOver) || (net != "" && netOver) {
		return refusal{nVoteRate}
	}
	return nil
}

// gateSignup runs for a visitor without a username: only the network is known.
func gateSignup(req *core.GateReq) error {
	net := netKey(req)
	if net == "" {
		return nil
	}
	var res struct {
		Net  bool
		NetN int `json:"net_n"`
	}
	now := nowArg()
	if err := core.QueryJSON(sqlSignupCheck, &res, net, now); err != nil {
		return nil
	}
	if res.Net {
		return refusal{nNet}
	}
	if res.NetN >= signupNetPerHour {
		return refusal{nSignupRate}
	}
	recordRates(now, "signup_net", net)
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
