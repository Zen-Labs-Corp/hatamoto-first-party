package main

import (
	"errors"
	"slices"
)

// Prices are integers in the smallest unit of the currency, and no float is
// used anywhere: percentages are in whole percent or hundredths of a percent
// and round half up. priceCart is pure: it reads neither settings nor the
// database, so every rule can be table-tested.

const (
	maxTotalCents = 99999999 // the most a payment provider takes in one charge
	maxLineQty    = 1000
)

var (
	errTotalTooLarge = errors.New("total too large")
	errBadCart       = errors.New("bad cart")
)

// cartLine is one ticket type of a cart with the unit price read from the
// database, never from the request.
type cartLine struct {
	Type int64  `json:"type"`
	Name string `json:"name"`
	Qty  int    `json:"qty"`
	Unit int64  `json:"unit"`
}

// coupon is a discount code. EventID and TypeID are the scope; empty and 0
// mean any event and any type.
type coupon struct {
	ID      int64  `json:"id"`
	Code    string `json:"code"`
	Kind    string `json:"kind"` // percent or fixed
	Value   int64  `json:"value"`
	EventID string `json:"event_id"`
	TypeID  int64  `json:"type_id"`
}

// bundleRule is a discount on a combination of tickets of one event. No
// TypeIDs means every type counts; AllTypes asks for every listed type.
type bundleRule struct {
	ID       int64   `json:"id"`
	TypeIDs  []int64 `json:"type_ids"`
	AllTypes bool    `json:"all_types"`
	MinQty   int     `json:"min_qty"`
	Kind     string  `json:"kind"`
	Value    int64   `json:"value"`
}

// feeRule is the fee settings: a fixed part per order or per priced ticket
// plus a part in hundredths of a percent of the net amount.
type feeRule struct {
	Fixed     int64
	BP        int
	PerTicket bool
	Absorbed  bool // the organiser pays it: recorded, not added to the total
}

// quote is what a cart costs. Bundle and Coupon are the discounts as computed
// before the cap, Discount is what is taken off in the end.
type quote struct {
	Lines       []cartLine `json:"lines"`
	Subtotal    int64      `json:"subtotal_cents"`
	Bundle      int64      `json:"bundle_cents"`
	Coupon      int64      `json:"coupon_cents"`
	Discount    int64      `json:"discount_cents"`
	Fee         int64      `json:"fee_cents"`
	Total       int64      `json:"total_cents"`
	FeeAbsorbed bool       `json:"fee_absorbed"`
	CouponID    int64      `json:"coupon_id"` // the coupon that took something off, else 0
	Currency    string     `json:"currency"`
}

// discountOf is the discount a rule gives on gross: a percentage rounds half
// up, a fixed amount never exceeds what it covers.
func discountOf(kind string, value, gross int64) int64 {
	switch kind {
	case "percent":
		return (gross*value + 50) / 100
	case "fixed":
		return min(value, gross)
	}
	return 0
}

// bundleDiscount is the largest discount among the rules whose condition
// holds. Rules do not stack.
func bundleDiscount(lines []cartLine, rules []bundleRule) int64 {
	var best int64
	for _, r := range rules {
		var gross int64
		qty := 0
		for _, l := range lines {
			if len(r.TypeIDs) == 0 || slices.Contains(r.TypeIDs, l.Type) {
				gross += l.Unit * int64(l.Qty)
				qty += l.Qty
			}
		}
		if qty < r.MinQty {
			continue
		}
		if r.AllTypes && !coversAll(lines, r.TypeIDs) {
			continue
		}
		best = max(best, discountOf(r.Kind, r.Value, gross))
	}
	return best
}

// coversAll reports whether every listed type is in the cart.
func coversAll(lines []cartLine, types []int64) bool {
	for _, id := range types {
		if !slices.ContainsFunc(lines, func(l cartLine) bool { return l.Type == id && l.Qty > 0 }) {
			return false
		}
	}
	return true
}

// priceCart computes what a cart costs: subtotal, the best bundle, the coupon,
// the fee and the total, all in minor units.
func priceCart(lines []cartLine, c *coupon, rules []bundleRule, f feeRule, cur string) (quote, error) {
	q := quote{Lines: lines, Currency: cur, FeeAbsorbed: f.Absorbed}
	var priced int64
	for _, l := range lines {
		if l.Qty < 1 || l.Qty > maxLineQty || l.Unit < 0 || l.Unit > maxTotalCents {
			return quote{}, errBadCart
		}
		q.Subtotal += l.Unit * int64(l.Qty)
		if l.Unit > 0 {
			priced += int64(l.Qty)
		}
	}
	if q.Subtotal > maxTotalCents*maxLineQty { // nothing below can overflow int64
		return quote{}, errTotalTooLarge
	}
	q.Bundle = bundleDiscount(lines, rules)
	if c != nil {
		var gross int64
		for _, l := range lines {
			if c.TypeID == 0 || c.TypeID == l.Type {
				gross += l.Unit * int64(l.Qty)
			}
		}
		q.Coupon = discountOf(c.Kind, c.Value, gross)
		if q.Coupon > 0 {
			q.CouponID = c.ID
		}
	}
	q.Discount = min(q.Bundle+q.Coupon, q.Subtotal)
	net := q.Subtotal - q.Discount
	if net > 0 {
		fixed := f.Fixed
		if f.PerTicket {
			fixed *= priced
		}
		q.Fee = fixed + (net*int64(f.BP)+5000)/10000
	}
	q.Total = net
	if !f.Absorbed {
		q.Total += q.Fee
	}
	if q.Total > maxTotalCents {
		return quote{}, errTotalTooLarge
	}
	return q, nil
}
