package main

// A buyer who picks seats holds them for seatHoldWindow, before any name or
// email is known. The window starts at the first seat of a hold code and is
// never extended by later picks. It is not the payment window (holdWindow):
// checkout turns the held seats into a pending order that has its own.
const seatHoldWindow = "10 minutes"

// seatHoldResult is the answer of holdSeat. Status is held, taken (another
// buyer has the seat, or it is sold), full (this code holds as many seats as it may, or
// the event started too many codes this hour) or gone (no such seat for sale).
type seatHoldResult struct {
	Status    string `json:"status"`
	SeatID    int64  `json:"seat_id"`
	Label     string `json:"label"`
	ExpiresIn int    `json:"expires_in"` // seconds left of the code's hold
}

// heldSeat is a seat a hold code holds, with the ticket type that sells it.
type heldSeat struct {
	SeatID int64  `json:"seat_id"`
	TypeID int64  `json:"type_id"`
	Label  string `json:"label"`
}

// holdSeat holds one seat of a grid section for a hold code, in one statement.
// The seat must belong to a section bound to an on-sale type of the event, be
// unblocked and have no live ticket. The unique (event, seat) row is the
// guard: the insert only takes over a row that has expired, so a live hold of
// another buyer is never overwritten, however many requests race. A code may
// hold maxTicketsPerOrder seats, and a new code is only started while the event
// has fewer than maxOrdersPerHour codes started in the last hour, the ceiling
// checkout has for orders. There is no section-wide cap: the seat row is the
// scarce unit, and a cap on it would let partial hoarding lock a section out.
// Holding a seat the code already holds answers held and changes nothing.
func holdSeat(eventID, code string, sectionID int64, rowIdx, num int) (seatHoldResult, error) {
	var res seatHoldResult
	err := dbJSON(`WITH s AS (SELECT st.id, st.row_label, st.num, ms.name AS section_name, t.id AS type_id, t.capacity
    FROM seats st JOIN map_sections ms ON ms.id = st.section_id AND ms.kind = 'grid'
    JOIN ticket_types t ON t.section_id = ms.id AND t.event_id = $1 AND t.on_sale
    WHERE st.section_id = $3 AND st.row_idx = $4 AND st.num = $5 AND NOT st.blocked),
 mine AS (SELECT h.expires_at FROM seat_holds h JOIN s ON h.seat_id = s.id
    WHERE h.event_id = $1 AND h.code = $2 AND h.expires_at > now()),
 sold AS (SELECT 1 FROM attendees a JOIN s ON a.seat_id = s.id
    WHERE a.event_id = $1 AND a.status IN ('pending', 'active')),
 caps AS (SELECT (SELECT count(*) FROM seat_holds h WHERE h.event_id = $1 AND h.code = $2 AND h.expires_at > now()) < $6
        AND (NOT EXISTS (SELECT 1 FROM seat_holds h WHERE h.event_id = $1 AND h.code = $2)
            OR (SELECT count(DISTINCT h.code) FROM seat_holds h WHERE h.event_id = $1 AND h.created_at > now() - interval '1 hour') < $7) AS ok
    FROM s),
 exp AS (SELECT coalesce((SELECT min(h.expires_at) FROM seat_holds h WHERE h.code = $2 AND h.event_id = $1 AND h.expires_at > now()),
        now() + interval '`+seatHoldWindow+`') AS at),
 ins AS (INSERT INTO seat_holds (event_id, seat_id, code, expires_at)
    SELECT $1, s.id, $2, exp.at FROM s, caps, exp
    WHERE caps.ok AND NOT EXISTS (SELECT 1 FROM sold) AND NOT EXISTS (SELECT 1 FROM mine)
    ON CONFLICT (event_id, seat_id) DO UPDATE SET code = EXCLUDED.code, expires_at = EXCLUDED.expires_at, created_at = now()
        WHERE seat_holds.expires_at <= now()
    RETURNING seat_id, expires_at)
SELECT jsonb_build_object(
    'status', CASE WHEN NOT EXISTS (SELECT 1 FROM s) THEN 'gone'
        WHEN EXISTS (SELECT 1 FROM mine) OR EXISTS (SELECT 1 FROM ins) THEN 'held'
        WHEN EXISTS (SELECT 1 FROM sold) THEN 'taken'
        WHEN NOT (SELECT ok FROM caps) THEN 'full'
        ELSE 'taken' END,
    'seat_id', coalesce((SELECT id FROM s), 0),
    'label', coalesce((SELECT section_name || ' ' || row_label || '-' || num FROM s), ''),
    'expires_in', coalesce((SELECT extract(epoch FROM i.expires_at - now())::int FROM ins i),
        (SELECT extract(epoch FROM m.expires_at - now())::int FROM mine m), 0))`,
		&res, eventID, code, sectionID, rowIdx, num, maxTicketsPerOrder, maxOrdersPerHour)
	return res, err
}

// releaseSeatHold lets go of one seat a hold code holds, or of all of them
// when seatID is 0, and returns how many seats it freed.
func releaseSeatHold(eventID, code string, seatID int64) (int, error) {
	n, err := dbExecN(`DELETE FROM seat_holds WHERE event_id = $1 AND code = $2 AND ($3::bigint = 0 OR seat_id = $3)`,
		eventID, code, seatID)
	return int(n), err
}

// heldSeats lists the live seats a hold code holds for the event, in seat order.
func heldSeats(eventID, code string) ([]heldSeat, error) {
	out := []heldSeat{}
	err := dbJSON(`SELECT coalesce(jsonb_agg(jsonb_build_object('seat_id', st.id, 'type_id', t.id,
    'label', ms.name || ' ' || st.row_label || '-' || st.num) ORDER BY st.section_id, st.row_idx, st.num), '[]'::jsonb)
FROM seat_holds h JOIN seats st ON st.id = h.seat_id JOIN map_sections ms ON ms.id = st.section_id
JOIN ticket_types t ON t.section_id = ms.id AND t.event_id = h.event_id
WHERE h.event_id = $1 AND h.code = $2 AND h.expires_at > now()`, &out, eventID, code)
	return out, err
}
