package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"hatamoto.dev/sdk"
)

// Coupons and bundle rules, listed for the owner and editor, changed by the owner only: a discount is money.

const maxBundleRules = 10

// couponBody is the body of POST /admin/coupons: a new coupon, or the one named
// by id. event_id and type_id narrow the scope; empty and 0 mean any. A missing
// active means true. expires_at is a date or an RFC 3339 time; a date lasts to
// the end of that day (UTC).
type couponBody struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Kind      string `json:"kind"`
	Value     int64  `json:"value"`
	EventID   string `json:"event_id"`
	TypeID    int64  `json:"type_id"`
	MaxUses   *int   `json:"max_uses"`
	ExpiresAt string `json:"expires_at"`
	Active    *bool  `json:"active"`
}

// validDiscount checks a discount: percent 1..100, fixed 1..the price cap.
func validDiscount(kind string, value int64) bool {
	switch kind {
	case "percent":
		return value >= 1 && value <= 100
	case "fixed":
		return value >= 1 && value <= maxPriceCents
	}
	return false
}

// parseExpiry reads expires_at; nil means no expiry.
func parseExpiry(s string) (*string, bool) {
	if s == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		d, derr := time.Parse("2006-01-02", s)
		if derr != nil {
			return nil, false
		}
		t = d.Add(24*time.Hour - time.Second)
	}
	out := t.UTC().Format(time.RFC3339)
	return &out, true
}

// serveCoupons answers GET /admin/coupons: the coupons and, read-only, every bundle rule.
func serveCoupons(u *sdk.HTTPUser) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	var rows []json.RawMessage
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id DESC), '[]'::jsonb) FROM (
SELECT id, code, kind, value, coalesce(event_id, '') AS event_id, coalesce(type_id, 0) AS type_id,
  max_uses, used, to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS expires_at, active FROM coupons ORDER BY id DESC LIMIT 500) c`, &rows)
	if err != nil {
		return unavailable()
	}
	// Every event's bundle rules, read-only, for the Orders tab summary line.
	var bundles []json.RawMessage
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(b) ORDER BY b.event_id, b.position, b.id), '[]'::jsonb) FROM (
SELECT r.id, r.event_id, r.min_qty, r.kind, r.value, r.all_types,
  coalesce((SELECT jsonb_agg(t.name ORDER BY t.position, t.id) FROM ticket_types t
    WHERE r.type_ids @> to_jsonb(t.id)), '[]'::jsonb) AS type_names
  FROM bundle_rules r ORDER BY r.event_id, r.position, r.id LIMIT 100) b`, &bundles)
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"coupons": rows, "bundles": bundles})
}

// serveSaveCoupon answers POST /admin/coupons. A code that is taken is a 409.
func serveSaveCoupon(r sdk.HTTPRequest) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	var b couponBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	code, ok := cleanCouponCode(b.Code)
	expires, expOK := parseExpiry(b.ExpiresAt)
	switch {
	case !ok || !expOK || b.ID < 0 || b.TypeID < 0 || !validDiscount(b.Kind, b.Value):
		return failReply(http.StatusBadRequest, "bad request")
	case b.EventID != "" && !idOK(b.EventID):
		return failReply(http.StatusBadRequest, "bad request")
	case b.MaxUses != nil && (*b.MaxUses < 1 || *b.MaxUses > 1000000):
		return failReply(http.StatusBadRequest, "bad request")
	}
	active := b.Active == nil || *b.Active
	var event, typeID, maxUses, exp any // NULL when not set
	if b.EventID != "" {
		event = b.EventID
	}
	if b.TypeID != 0 {
		typeID = b.TypeID
	}
	if b.MaxUses != nil {
		maxUses = *b.MaxUses
	}
	if expires != nil {
		exp = *expires
	}
	// The type, when given, must belong to the event, when given.
	scopeOK := `($5::bigint IS NULL OR EXISTS (SELECT 1 FROM ticket_types WHERE id = $5::bigint AND ($4::text IS NULL OR event_id = $4::text)))`
	var res struct {
		ID     *int64 `json:"id"`
		Exists bool   `json:"exists"`
	}
	var err error
	if b.ID == 0 {
		err = dbJSON(`WITH i AS (INSERT INTO coupons (code, kind, value, event_id, type_id, max_uses, expires_at, active)
    SELECT $1, $2, $3, $4::text, $5::bigint, $6::int, $7::timestamptz, $8 WHERE `+scopeOK+`
    ON CONFLICT (code) DO NOTHING RETURNING id)
SELECT jsonb_build_object('id', (SELECT id FROM i), 'exists', EXISTS (SELECT 1 FROM coupons WHERE code = $1) AND NOT EXISTS (SELECT 1 FROM i))`,
			&res, code, b.Kind, b.Value, event, typeID, maxUses, exp, active)
		if err == nil && res.ID == nil {
			if res.Exists {
				return failReply(http.StatusConflict, "this code is already used")
			}
			return failReply(http.StatusBadRequest, "bad request")
		}
	} else {
		err = dbJSON(`WITH u AS (UPDATE coupons SET code = $1, kind = $2, value = $3, event_id = $4::text, type_id = $5::bigint,
    max_uses = $6::int, expires_at = $7::timestamptz, active = $8
    WHERE id = $9 AND `+scopeOK+` AND NOT EXISTS (SELECT 1 FROM coupons c WHERE c.code = $1 AND c.id <> $9) RETURNING id)
SELECT jsonb_build_object('id', (SELECT id FROM u), 'exists', EXISTS (SELECT 1 FROM coupons WHERE id = $9))`,
			&res, code, b.Kind, b.Value, event, typeID, maxUses, exp, active, b.ID)
		if err == nil && res.ID == nil {
			if !res.Exists {
				return failReply(http.StatusNotFound, "not found")
			}
			return failReply(http.StatusConflict, "this code is already used")
		}
	}
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "id": *res.ID})
}

// serveDeleteCoupon answers POST /admin/coupons/{id}/delete. A coupon that was
// used stays in the orders' history: it is switched off instead of deleted.
func serveDeleteCoupon(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil || u.Role != "owner" {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var res struct {
		Deleted     bool `json:"deleted"`
		Deactivated bool `json:"deactivated"`
	}
	err := dbJSON(`WITH d AS (DELETE FROM coupons WHERE id = $1 AND used = 0 RETURNING id),
 u AS (UPDATE coupons SET active = false WHERE id = $1 AND used > 0 RETURNING id)
SELECT jsonb_build_object('deleted', EXISTS (SELECT 1 FROM d), 'deactivated', EXISTS (SELECT 1 FROM u))`, &res, id)
	switch {
	case err != nil:
		return unavailable()
	case !res.Deleted && !res.Deactivated:
		return failReply(http.StatusNotFound, "not found")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "deleted": res.Deleted})
}

// bundleBody is one rule of POST /admin/events/{id}/bundles.
type bundleBody struct {
	TypeIDs  []int64 `json:"type_ids"`
	AllTypes bool    `json:"all_types"`
	MinQty   int     `json:"min_qty"`
	Kind     string  `json:"kind"`
	Value    int64   `json:"value"`
}

// serveSaveBundles answers POST /admin/events/{id}/bundles with the event's
// whole rule list, and answers the saved rules.
func serveSaveBundles(r sdk.HTTPRequest, eventID string) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	if !idOK(eventID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var b struct {
		Rules []bundleBody `json:"rules"`
	}
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	if len(b.Rules) > maxBundleRules {
		return failReply(http.StatusBadRequest, "at most 10 bundle rules")
	}
	type row struct {
		bundleBody
		Pos int `json:"pos"`
	}
	rows := make([]row, len(b.Rules))
	for i, x := range b.Rules {
		if x.TypeIDs == nil {
			x.TypeIDs = []int64{}
		}
		if len(x.TypeIDs) > 20 || slices.ContainsFunc(x.TypeIDs, func(id int64) bool { return id < 1 }) ||
			(x.AllTypes && len(x.TypeIDs) == 0) || x.MinQty < 1 || x.MinQty > 10 || !validDiscount(x.Kind, x.Value) {
			return failReply(http.StatusBadRequest, "bad request")
		}
		rows[i] = row{x, i}
	}
	if _, found, err := readEvent(eventID, loadSettings()); err != nil {
		return unavailable()
	} else if !found {
		return failReply(http.StatusNotFound, "not found")
	}
	doc, _ := json.Marshal(rows)
	err := dbExec(`WITH d AS (DELETE FROM bundle_rules WHERE event_id = $1)
INSERT INTO bundle_rules (event_id, type_ids, all_types, min_qty, kind, value, position)
SELECT $1, x.type_ids, x.all_types, x.min_qty, x.kind, x.value, x.pos
FROM jsonb_to_recordset($2::jsonb) AS x(type_ids jsonb, all_types boolean, min_qty int, kind text, value bigint, pos int)`, eventID, string(doc))
	if err != nil {
		return unavailable()
	}
	rules, err := listBundles(eventID)
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "bundles": rules})
}
