package main

// completeFree marks a pending order whose total is zero (a coupon covered
// everything) as paid and activates its tickets, in one statement. It reports
// whether this call made the change, so the confirmation is sent once.
func completeFree(orderID int64) (bool, error) {
	var done bool
	err := dbJSON(`WITH o AS (UPDATE orders SET status = 'paid', paid_at = now()
    WHERE id = $1 AND status = 'pending' AND total_cents = 0 RETURNING id),
 a AS (UPDATE attendees SET status = 'active' WHERE order_id IN (SELECT id FROM o) AND status = 'pending' RETURNING id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM o))`, &done, orderID)
	return done, err
}
