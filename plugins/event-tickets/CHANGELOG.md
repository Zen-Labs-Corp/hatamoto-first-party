# Changelog

## 0.6.0
- Seating: maps with grid sections (rows, seats per row, price tier, blocked seats) and GA zones in a new Seating tab, bound to ticket types per event. A Seat picker widget lets buyers choose seats, held for 10 minutes with a countdown, and checkout turns the hold into the order; without picking, buyers get the best available seats in their tier. Tickets, mails, check-in, attendees, orders and the CSV show the seat (`seat` column in the CSV).

## 0.5.0
- Emails tab (owner only): automatic event-changed, reminder (24 hours before by default) and thank-you mails, and campaigns sent now or later to a segment (event, ticket type, checked in, bought before, RSVP only, not bought). Campaigns show sent, open, click and 7-day buy counts. Every mail except the ticket confirmation has an unsubscribe link with a confirm page. Bulk mail is paced to about three quarters of the site's mail quota. Event-changed and reminder mails are on by default; the reminder only goes to events that have not started yet.

## 0.4.0
- Payouts: per-event split rules (percent or fixed per ticket), a monthly payouts report per payee and currency, CSV export, mark as paid with date and reference. A report only; no money moves.

## 0.3.0
- Check-in tab with camera QR scan, search, undo and offline queue.

## 0.2.0
- Paid tickets (Stripe, PayPal), orders, refunds, coupons, bundle discounts and fees.

## 0.1.0
- RSVP and free tickets, attendees, waitlist, QR tickets.
