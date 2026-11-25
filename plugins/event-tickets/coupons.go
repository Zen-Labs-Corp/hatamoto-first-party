package main

import (
	"regexp"
	"strings"
)

var couponCodeRe = regexp.MustCompile(`^[A-Z0-9_-]{3,32}$`)

// cleanCouponCode upper-cases what the buyer typed, so a code is found
// whatever its case, and reports whether it can be a code at all.
func cleanCouponCode(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	return s, couponCodeRe.MatchString(s)
}

// lookupCoupon finds a code that can be used now for the event: active, not
// expired, not used up, and not scoped to another event. nil when there is
// none. The use is counted later, in the statement that holds the seats.
func lookupCoupon(code, eventID string) (*coupon, error) {
	code, ok := cleanCouponCode(code)
	if !ok {
		return nil, nil
	}
	var c *coupon
	err := dbJSON(`SELECT coalesce((SELECT to_jsonb(c) FROM (
SELECT id, code, kind, value, coalesce(event_id, '') AS event_id, coalesce(type_id, 0) AS type_id
FROM coupons
WHERE code = $1 AND active AND (expires_at IS NULL OR expires_at > now())
AND (max_uses IS NULL OR used < max_uses) AND (event_id IS NULL OR event_id = $2)) c), 'null'::jsonb)`, &c, code, eventID)
	return c, err
}

// listBundles reads the bundle rules of an event.
func listBundles(eventID string) ([]bundleRule, error) {
	out := []bundleRule{}
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.position, r.id), '[]'::jsonb) FROM (
SELECT id, type_ids, all_types, min_qty, kind, value, position FROM bundle_rules WHERE event_id = $1) r`, &out, eventID)
	return out, err
}
