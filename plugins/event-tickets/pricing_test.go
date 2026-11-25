package main

import (
	"errors"
	"testing"
)

var (
	ga  = cartLine{Type: 1, Name: "GA", Qty: 2, Unit: 1000}
	vip = cartLine{Type: 2, Name: "VIP", Qty: 1, Unit: 5000}
	fre = cartLine{Type: 3, Name: "Free", Qty: 4, Unit: 0}
)

func TestPriceCart(t *testing.T) {
	pct := func(v int64) *coupon { return &coupon{ID: 9, Kind: "percent", Value: v} }
	fix := func(v int64) *coupon { return &coupon{ID: 9, Kind: "fixed", Value: v} }
	for _, c := range []struct {
		name  string
		lines []cartLine
		cp    *coupon
		rules []bundleRule
		fee   feeRule
		cur   string
		// subtotal, bundle, coupon, discount, fee, total
		want [6]int64
		id   int64
	}{
		{name: "plain", lines: []cartLine{ga, vip}, cur: "USD", want: [6]int64{7000, 0, 0, 0, 0, 7000}},
		{name: "free lines are free", lines: []cartLine{ga, fre}, cur: "USD", want: [6]int64{2000, 0, 0, 0, 0, 2000}},
		{name: "percent coupon on one type", lines: []cartLine{ga, vip}, cp: &coupon{ID: 9, Kind: "percent", Value: 10, TypeID: 2}, cur: "USD",
			want: [6]int64{7000, 0, 500, 500, 0, 6500}, id: 9},
		{name: "percent coupon any type", lines: []cartLine{ga, vip}, cp: pct(25), cur: "USD", want: [6]int64{7000, 0, 1750, 1750, 0, 5250}, id: 9},
		{name: "fixed coupon larger than its gross", lines: []cartLine{ga, vip}, cp: &coupon{ID: 9, Kind: "fixed", Value: 9000, TypeID: 1}, cur: "USD",
			want: [6]int64{7000, 0, 2000, 2000, 0, 5000}, id: 9},
		{name: "fixed coupon larger than the cart", lines: []cartLine{ga}, cp: fix(99999), cur: "USD", want: [6]int64{2000, 0, 2000, 2000, 0, 0}, id: 9},
		{name: "coupon for a type not in the cart", lines: []cartLine{ga}, cp: &coupon{ID: 9, Kind: "percent", Value: 50, TypeID: 7}, cur: "USD",
			want: [6]int64{2000, 0, 0, 0, 0, 2000}},
		{name: "best of two bundles", lines: []cartLine{ga, vip}, cur: "USD",
			rules: []bundleRule{
				{ID: 1, Kind: "percent", Value: 10, MinQty: 2},
				{ID: 2, Kind: "fixed", Value: 1000, MinQty: 3},
				{ID: 3, Kind: "percent", Value: 50, MinQty: 4},
			}, want: [6]int64{7000, 1000, 0, 1000, 0, 6000}},
		{name: "bundle on listed types only", lines: []cartLine{ga, vip}, cur: "USD",
			rules: []bundleRule{{ID: 1, TypeIDs: []int64{1}, Kind: "percent", Value: 10, MinQty: 2}}, want: [6]int64{7000, 200, 0, 200, 0, 6800}},
		{name: "bundle below its minimum", lines: []cartLine{vip}, cur: "USD",
			rules: []bundleRule{{ID: 1, Kind: "percent", Value: 10, MinQty: 2}}, want: [6]int64{5000, 0, 0, 0, 0, 5000}},
		{name: "combo needs every type", lines: []cartLine{ga, vip}, cur: "USD",
			rules: []bundleRule{{ID: 1, TypeIDs: []int64{1, 2}, AllTypes: true, MinQty: 1, Kind: "percent", Value: 20}}, want: [6]int64{7000, 1400, 0, 1400, 0, 5600}},
		{name: "combo with a type missing", lines: []cartLine{ga}, cur: "USD",
			rules: []bundleRule{{ID: 1, TypeIDs: []int64{1, 2}, AllTypes: true, MinQty: 1, Kind: "percent", Value: 20}}, want: [6]int64{2000, 0, 0, 0, 0, 2000}},
		{name: "bundle and coupon stack, capped at the subtotal", lines: []cartLine{ga}, cp: pct(80), cur: "USD",
			rules: []bundleRule{{ID: 1, Kind: "percent", Value: 50, MinQty: 1}}, want: [6]int64{2000, 1000, 1600, 2000, 0, 0}, id: 9},
		{name: "bundle and coupon stack", lines: []cartLine{ga}, cp: pct(10), cur: "USD",
			rules: []bundleRule{{ID: 1, Kind: "percent", Value: 10, MinQty: 1}}, want: [6]int64{2000, 200, 200, 400, 0, 1600}, id: 9},
		{name: "fixed fee per order", lines: []cartLine{ga, vip}, fee: feeRule{Fixed: 30}, cur: "USD", want: [6]int64{7000, 0, 0, 0, 30, 7030}},
		{name: "fixed fee per priced ticket", lines: []cartLine{ga, vip, fre}, fee: feeRule{Fixed: 30, PerTicket: true}, cur: "USD", want: [6]int64{7000, 0, 0, 0, 90, 7090}},
		{name: "percent fee rounds half up", lines: []cartLine{{Type: 1, Qty: 1, Unit: 1010}}, fee: feeRule{BP: 250}, cur: "USD", want: [6]int64{1010, 0, 0, 0, 25, 1035}}, // 25.25
		{name: "percent fee at exactly .5", lines: []cartLine{{Type: 1, Qty: 1, Unit: 1020}}, fee: feeRule{BP: 250}, cur: "USD", want: [6]int64{1020, 0, 0, 0, 26, 1046}},  // 25.5
		{name: "percent fee below .5", lines: []cartLine{{Type: 1, Qty: 1, Unit: 1000}}, fee: feeRule{BP: 249}, cur: "USD", want: [6]int64{1000, 0, 0, 0, 25, 1025}},       // 24.9
		{name: "fixed and percent fee", lines: []cartLine{ga}, fee: feeRule{Fixed: 30, BP: 290}, cur: "USD", want: [6]int64{2000, 0, 0, 0, 88, 2088}},
		{name: "fee on the net amount", lines: []cartLine{ga}, cp: pct(50), fee: feeRule{BP: 1000}, cur: "USD", want: [6]int64{2000, 0, 1000, 1000, 100, 1100}, id: 9},
		{name: "absorbed fee is recorded, not added", lines: []cartLine{ga}, fee: feeRule{Fixed: 30, Absorbed: true}, cur: "USD", want: [6]int64{2000, 0, 0, 0, 30, 2000}},
		{name: "zero decimal currency", lines: []cartLine{{Type: 1, Qty: 3, Unit: 1500}}, cp: pct(10), fee: feeRule{Fixed: 50}, cur: "JPY", want: [6]int64{4500, 0, 450, 450, 50, 4100}, id: 9},
		{name: "100 percent coupon: no fee, no total", lines: []cartLine{ga}, cp: pct(100), fee: feeRule{Fixed: 30, BP: 300}, cur: "USD", want: [6]int64{2000, 0, 2000, 2000, 0, 0}, id: 9},
		{name: "free cart has no fee", lines: []cartLine{fre}, fee: feeRule{Fixed: 30, BP: 300}, cur: "USD", want: [6]int64{0, 0, 0, 0, 0, 0}},
		{name: "total at the cap", lines: []cartLine{{Type: 1, Qty: 1, Unit: 99999999}}, cur: "USD", want: [6]int64{99999999, 0, 0, 0, 0, 99999999}},
	} {
		q, err := priceCart(c.lines, c.cp, c.rules, c.fee, c.cur)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got := [6]int64{q.Subtotal, q.Bundle, q.Coupon, q.Discount, q.Fee, q.Total}
		if got != c.want || q.CouponID != c.id || q.Currency != c.cur || q.FeeAbsorbed != c.fee.Absorbed {
			t.Errorf("%s: got %v coupon %d, want %v coupon %d", c.name, got, q.CouponID, c.want, c.id)
		}
	}
}

func TestPriceCartRefusals(t *testing.T) {
	over := []cartLine{{Type: 1, Qty: 1, Unit: 99999999}}
	if _, err := priceCart(over, nil, nil, feeRule{Fixed: 1}, "USD"); !errors.Is(err, errTotalTooLarge) {
		t.Errorf("total over the cap: %v", err)
	}
	if _, err := priceCart([]cartLine{{Type: 1, Qty: 2, Unit: 60000000}}, nil, nil, feeRule{}, "USD"); !errors.Is(err, errTotalTooLarge) {
		t.Errorf("subtotal over the cap: %v", err)
	}
	// an absorbed fee is not added, so the cap holds
	if _, err := priceCart(over, nil, nil, feeRule{Fixed: 1, Absorbed: true}, "USD"); err != nil {
		t.Errorf("absorbed fee at the cap: %v", err)
	}
	for name, l := range map[string]cartLine{
		"zero qty": {Type: 1, Qty: 0, Unit: 1}, "negative qty": {Type: 1, Qty: -1, Unit: 1}, "huge qty": {Type: 1, Qty: 1001, Unit: 1},
		"negative price": {Type: 1, Qty: 1, Unit: -1}, "huge price": {Type: 1, Qty: 1, Unit: 100000000},
	} {
		if _, err := priceCart([]cartLine{l}, nil, nil, feeRule{}, "USD"); !errors.Is(err, errBadCart) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// priceCart takes its inputs as arguments and answers from them alone.
func TestPriceCartIsPure(t *testing.T) {
	callHost = func(fn string, _, _ any) error {
		t.Errorf("priceCart called the host: %s", fn)
		return nil
	}
	if _, err := priceCart([]cartLine{ga}, nil, nil, feeRule{}, "USD"); err != nil {
		t.Fatal(err)
	}
}
