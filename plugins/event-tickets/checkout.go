package main

import (
	"net/http"
	"slices"
	"time"

	"hatamoto.dev/sdk"
)

// The paid-ticket routes. Prices come from the database only: neither body
// has a price key, and an unknown key is a 400. Every answer is generic.

const (
	maxCheckoutBody = maxBodyBytes // 16 KiB
	maxQuoteBody    = 4 << 10
	maxPendingHolds = 2 // open (unpaid) holds per address and event
	checkoutBudget  = 8 * time.Second
	holdSweepBatch  = 50
	titleRunes      = 100
)

// checkoutBody is the JSON the island posts to pay.
type checkoutBody struct {
	Event    string            `json:"event"`
	Items    []rsvpItem        `json:"items"`
	Name     string            `json:"name"`
	Email    string            `json:"email"`
	Answers  map[string]string `json:"answers"`
	Coupon   string            `json:"coupon"`
	Provider string            `json:"provider"`
	Token    string            `json:"token"`
	Website  string            `json:"website"`
	Hold     string            `json:"hold"` // token of the seats the buyer picked, if any
}

// quoteBody is the JSON the island posts to show a total.
type quoteBody struct {
	Event  string     `json:"event"`
	Items  []rsvpItem `json:"items"`
	Coupon string     `json:"coupon"`
}

// cartLines prices the requested seats from the ticket types read from the
// database. ok is false when a type is not on sale (or not the event's).
func cartLines(types []ticketType, items []rsvpItem) ([]cartLine, bool) {
	lines := make([]cartLine, 0, len(items))
	for _, it := range items {
		i := slices.IndexFunc(types, func(t ticketType) bool { return t.ID == it.Type && t.OnSale })
		if i < 0 {
			return nil, false
		}
		lines = append(lines, cartLine{Type: it.Type, Name: types[i].Name, Qty: it.Qty, Unit: types[i].Price})
	}
	return lines, true
}

// providers lists the payment providers that are set up and usable, for the
// island's pay buttons. A currency that cannot be charged lists none.
func (s settings) providers() []string {
	out := []string{}
	if !paymentsCurrencyOK(s.Currency) {
		return out
	}
	if s.stripeReady() {
		out = append(out, "stripe")
	}
	if s.paypalReady() {
		out = append(out, "paypal")
	}
	return out
}

func (s settings) feeRule() feeRule {
	return feeRule{Fixed: s.FeeFixed, BP: s.FeeBP, PerTicket: s.FeePerTicket, Absorbed: s.FeeAbsorbed}
}

// hasPriced reports whether any line costs money.
func hasPriced(lines []cartLine) bool {
	return slices.ContainsFunc(lines, func(l cartLine) bool { return l.Unit > 0 })
}

// serveQuote answers what a cart costs. An unknown coupon is not an error: the
// quote comes back without it and coupon_ok false.
func serveQuote(r sdk.HTTPRequest) sdk.HTTPResponse {
	var b quoteBody
	if bad := readPostJSON(r, maxQuoteBody, &b); bad != nil {
		return *bad
	}
	items, ok := cleanItems(b.Items)
	if !idOK(b.Event) || !ok {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	s := loadSettings()
	e, found, err := readEvent(b.Event, s)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if !found || !e.open(now()) {
		return jsonReply(http.StatusNotFound, map[string]any{"ok": false, "closed": true, "error": "closed"})
	}
	types, err := listTypes(b.Event)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	lines, ok := cartLines(types, items)
	if !ok {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	c, err := lookupCoupon(b.Coupon, b.Event)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	rules, err := listBundles(b.Event)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	q, err := priceCart(lines, c, rules, s.feeRule(), s.Currency)
	if err != nil {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	// The texts are what the island shows: it never formats or adds up a price.
	display := map[string]string{
		"subtotal": formatMoney(q.Subtotal, q.Currency), "discount": formatMoney(q.Discount, q.Currency),
		"fee": formatMoney(q.Fee, q.Currency), "total": formatMoney(q.Total, q.Currency),
	}
	return rsvpOK(map[string]any{"quote": q, "coupon_ok": c != nil, "display": display})
}

// serveCheckout holds the seats of a paid cart and sends the buyer to the
// payment provider. The hold is one statement (holdCart); when anything after
// it fails the hold is released again.
func serveCheckout(r sdk.HTTPRequest) sdk.HTTPResponse {
	var b checkoutBody
	if bad := readPostJSON(r, maxCheckoutBody, &b); bad != nil {
		return *bad
	}
	g, bad := guardPost(b.Website, b.Token, b.Name, b.Email)
	if bad != nil {
		return *bad
	}
	items, ok := cleanItems(b.Items)
	if !idOK(b.Event) || !ok || (b.Provider != "stripe" && b.Provider != "paypal") {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	env, bad := loadCartEnv(b.Event, g.Email)
	if bad != nil {
		return *bad
	}
	s := env.S
	origin := originOf(env.E.URL, s.SiteURL)
	ready := (b.Provider == "stripe" && s.stripeReady()) || (b.Provider == "paypal" && s.paypalReady())
	if !ready || origin == "" || !paymentsCurrencyOK(s.Currency) {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	// Picked seats are the truth for the seated lines: the buyer sends the
	// hold token, never a seat id.
	var held []heldSeat
	holdCodeStr := ""
	if b.Hold != "" {
		c, ok := verifyToken(g.Key, kindHold, b.Hold)
		if !ok {
			return holdEnded()
		}
		holdCodeStr = c
		var err error
		if held, err = heldSeats(b.Event, c); err != nil {
			return failReply(http.StatusServiceUnavailable, laterError)
		}
		if len(held) == 0 {
			return holdEnded()
		}
		if items, ok = withHeldSeats(items, held); !ok {
			return failReply(http.StatusUnprocessableEntity, fieldsError)
		}
	}
	lines, ok := cartLines(env.Types, items)
	if !ok || !hasPriced(lines) { // an all-free cart is an RSVP
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	if env.Counts.PendingByEmail >= maxPendingHolds {
		return failReply(http.StatusTooManyRequests, tooManyError)
	}
	answers, bad := buyerAnswers(b.Event, b.Answers)
	if bad != nil {
		return *bad
	}

	// Seats of lapsed holds of this event come back before this cart asks for them.
	if sessions, err := expireHolds(b.Event, holdSweepBatch); err == nil && len(sessions) > 0 {
		onHoldsExpired(sessions)
	}

	var cp *coupon
	if b.Coupon != "" {
		var err error
		if cp, err = lookupCoupon(b.Coupon, b.Event); err != nil {
			return failReply(http.StatusServiceUnavailable, laterError)
		}
		if cp == nil {
			return jsonReply(http.StatusConflict, map[string]any{"ok": false, "error": "coupon"})
		}
	}
	rules, err := listBundles(b.Event)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	q, err := priceCart(lines, cp, rules, s.feeRule(), s.Currency)
	if err != nil {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}

	// Seated lines without picked seats get the best seats available, held
	// under the buyer's hold code (a new one when there is none).
	plan, short, err := seatCart(b.Event, s.Currency, holdCodeStr, env.Types, q.Lines, held)
	if err != nil {
		plan.release(b.Event)
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if len(short) > 0 {
		plan.release(b.Event)
		return jsonReply(http.StatusConflict, map[string]any{"ok": false, "error": "sold out", "sold_out": short})
	}

	var codes []holdCode
	var tickets []string
	for _, l := range q.Lines {
		for i := range l.Qty {
			c, err := newCode()
			if err != nil {
				plan.release(b.Event)
				return failReply(http.StatusServiceUnavailable, laterError)
			}
			hc := holdCode{Type: l.Type, Code: c, Label: plan.Zones[l.Type]}
			if seats := plan.Seats[l.Type]; i < len(seats) {
				hc.Seat, hc.Label = seats[i].SeatID, seats[i].Label
			}
			codes = append(codes, hc)
			tickets = append(tickets, ticketPath(signCode(g.Key, kindTicket, c)))
		}
	}
	payCode, err := newCode()
	if err != nil {
		plan.release(b.Event)
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	provider := b.Provider
	if q.Total == 0 {
		provider = "none"
	}
	res, err := holdCart(holdIn{
		EventID: b.Event, Buyer: orderIn{Name: g.Name, Email: g.Email, Answers: answers},
		Q: q, Codes: codes, Provider: provider, PayCode: payCode, HoldCode: plan.Code,
	})
	if err != nil {
		plan.release(b.Event)
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if res.OrderID == nil {
		plan.release(b.Event)
		return heldNothing(res)
	}
	orderID := *res.OrderID

	if q.Total == 0 { // a coupon covered everything: no payment to wait for
		done, err := completeFree(orderID)
		if err != nil {
			_, _ = releaseHold(orderID, "cancelled")
			return failReply(http.StatusServiceUnavailable, laterError)
		}
		if done {
			sendNow(kindConfirm, orderID)
		}
		return rsvpOK(map[string]any{"tickets": tickets})
	}

	redirect, err := openPayment(s, g.Key, b.Provider, orderID, payCode, origin, env.E, g.Email, q)
	if err != nil {
		_, _ = releaseHold(orderID, "cancelled")
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	return rsvpOK(map[string]any{"redirect": redirect})
}

// heldNothing explains why a cart could not be held.
func heldNothing(res holdResult) sdk.HTTPResponse {
	switch {
	case res.HoldGone:
		return holdEnded()
	case len(res.Changed) > 0:
		return jsonReply(http.StatusConflict, map[string]any{"ok": false, "error": "changed", "changed": res.Changed})
	case res.CouponGone:
		return jsonReply(http.StatusConflict, map[string]any{"ok": false, "error": "coupon"})
	}
	return jsonReply(http.StatusConflict, map[string]any{"ok": false, "error": "sold out", "sold_out": res.Full})
}

// holdEnded is the answer when the picked seats are not held any more: the
// island tells the buyer to pick again.
func holdEnded() sdk.HTTPResponse {
	return jsonReply(http.StatusConflict, map[string]any{"ok": false, "error": "hold"})
}

// withHeldSeats sets the quantity of every seated type that has picked seats
// to the number of seats held, and adds the type when the cart lacks it. false
// when the cart would be larger than an order may be.
func withHeldSeats(items []rsvpItem, held []heldSeat) ([]rsvpItem, bool) {
	count := map[int64]int{}
	var order []int64
	for _, h := range held {
		if count[h.TypeID] == 0 {
			order = append(order, h.TypeID)
		}
		count[h.TypeID]++
	}
	out := make([]rsvpItem, 0, len(items)+len(order))
	total := 0
	for _, it := range items {
		if n := count[it.Type]; n > 0 {
			it.Qty = n
			delete(count, it.Type)
		}
		total += it.Qty
		out = append(out, it)
	}
	for _, id := range order {
		if n := count[id]; n > 0 {
			out = append(out, rsvpItem{Type: id, Qty: n})
			total += n
		}
	}
	return out, total <= maxTicketsPerOrder
}

// seatPlan is where the tickets of a cart sit: the seats per ticket type (held
// by the buyer or claimed for them now), the zone name of zone lines, and the
// seats this request claimed itself, which it gives back when the cart fails.
type seatPlan struct {
	Code    string
	Seats   map[int64][]heldSeat
	Zones   map[int64]string
	Claimed []int64
}

// release gives back the seats this request claimed. The seats the buyer
// picked before are left to their own hold.
func (p seatPlan) release(eventID string) {
	for _, id := range p.Claimed {
		_, _ = releaseSeatHold(eventID, p.Code, id)
	}
}

// seatCart finds the seats of the lines bound to a section. A zone line gets
// its zone name. A grid line that has picked seats keeps them; one that has
// none gets bestSeats of its section, claimed one seat at a time under the hold
// code (made here when there is none). A race on a seat is retried once from a
// fresh read; the types that still lack seats are returned in short, and the
// claims stay in the plan for the caller to release.
func seatCart(eventID, currency, code string, types []ticketType, lines []cartLine, held []heldSeat) (p seatPlan, short []int64, err error) {
	p = seatPlan{Code: code, Seats: map[int64][]heldSeat{}, Zones: map[int64]string{}}
	for _, h := range held {
		p.Seats[h.TypeID] = append(p.Seats[h.TypeID], h)
	}
	var avail *availMap
	section := func(typeID int64) *availSection {
		if avail == nil {
			return nil
		}
		i := slices.IndexFunc(avail.Sections, func(s availSection) bool { return s.TypeID == typeID })
		if i < 0 {
			return nil
		}
		return &avail.Sections[i]
	}
	for _, l := range lines {
		i := slices.IndexFunc(types, func(t ticketType) bool { return t.ID == l.Type })
		if i < 0 || types[i].SectionID == nil || (types[i].Seated && len(p.Seats[l.Type]) >= l.Qty) {
			continue
		}
		if avail == nil {
			if avail, err = seatAvailability(eventID, currency); err != nil {
				return p, nil, err
			}
		}
		sec := section(l.Type)
		if sec == nil {
			short = append(short, l.Type)
			continue
		}
		if !types[i].Seated {
			p.Zones[l.Type] = sec.Name
			continue
		}
		if p.Code == "" {
			if p.Code, err = newCode(); err != nil {
				return p, nil, err
			}
		}
		for attempt := 0; attempt < 2 && len(p.Seats[l.Type]) < l.Qty; attempt++ {
			if attempt > 0 {
				if avail, err = seatAvailability(eventID, currency); err != nil {
					return p, nil, err
				}
				if sec = section(l.Type); sec == nil {
					break
				}
			}
			picks := bestSeats(sec.Rows, l.Qty-len(p.Seats[l.Type]))
			if picks == nil {
				break
			}
			for _, pk := range picks {
				r, err := holdSeat(eventID, p.Code, sec.ID, pk[0], pk[1], 0) // no ceiling: bounded by the order ceilings
				if err != nil {
					return p, nil, err
				}
				if r.Status != "held" {
					break // taken meanwhile: read again
				}
				p.Seats[l.Type] = append(p.Seats[l.Type], heldSeat{SeatID: r.SeatID, TypeID: l.Type, Label: r.Label})
				p.Claimed = append(p.Claimed, r.SeatID)
			}
		}
		if len(p.Seats[l.Type]) < l.Qty {
			short = append(short, l.Type)
		}
	}
	return p, short, nil
}

// openPayment creates the payment at the provider for a held order, records
// its reference and returns the page to send the buyer to. The page is
// checked against the checkout hosts before it is returned. On an error the
// caller releases the hold.
func openPayment(s settings, key []byte, provider string, orderID int64, payCode, origin string, e event, email string, q quote) (string, error) {
	tok := signCode(key, kindOrder, payCode)
	in := payIn{
		OrderID: orderID, PayCode: payCode, Email: email, Title: "Tickets: " + capRunes(plainText(eventTitle(e), false), titleRunes),
		Currency: q.Currency, Total: q.Total,
		SuccessURL: origin + "/p/event-tickets/return/" + tok, CancelURL: origin + "/p/event-tickets/cancel/" + tok,
	}
	var ref, page string
	var err error
	if provider == "stripe" {
		in.SuccessURL += "?session_id={CHECKOUT_SESSION_ID}"
		ref, page, err = stripeCreateSession(s, in)
	} else {
		ref, page, err = paypalCreateOrder(s, in, newDeadline(checkoutBudget))
	}
	if err != nil {
		return "", err
	}
	if !checkoutURLOK(page) {
		if provider == "stripe" {
			_ = stripeExpireSession(s, ref)
		}
		return "", errProviderBadAnswer
	}
	n, err := dbExecN(`UPDATE orders SET provider_ref = $2 WHERE id = $1 AND status = 'pending'`, orderID, ref)
	if err != nil || n != 1 {
		if provider == "stripe" {
			_ = stripeExpireSession(s, ref)
		}
		return "", errProviderBadAnswer
	}
	return page, nil
}
