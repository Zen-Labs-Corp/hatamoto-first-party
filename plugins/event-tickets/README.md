# event-tickets

Free and paid tickets (Stripe, PayPal), RSVP, coupons, attendees and event emails for the event-calendar plugin.

Tickets are free or paid. Paid tickets are taken with Stripe and PayPal, with coupons, bundle discounts, fees and refunds. Each event has ticket types with a capacity, a booking form on the event page, a confirmation email with one QR code per ticket, a waitlist for sold-out free types and an admin page with the attendee list. Door check-in works from a phone by scanning the ticket QR code. Seating is not part of it yet.

## Use

1. Install the **event-calendar** plugin first: events are its calendar events. Then install this plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Open the plugin's admin page and its **Events** tab. Press **Tickets** on an event, then **Add ticket type**: give it a name, a number of tickets, a price (0 is free) and say whether it has a waitlist and whether it is on sale. A type that already has bookings is closed for sale instead of deleted. Under **Questions for the buyer** you can add up to 10 extra fields (text, choice or checkbox) that are asked once per booking.
3. In the page builder, drop the **Tickets** widget under **Event details** on the event page (or the layout events use). It lists the ticket types of the page's own event with how many are left, and shows the booking form. Without JavaScript a visitor sees the list; the booking form needs JavaScript.
4. A visitor picks how many tickets (at most 10 per booking), gives a name and an email address, answers the extra questions and presses reserve. The confirmation shows at once and an email follows with one QR code per ticket and a link to the ticket page. If a type is sold out and has a waitlist, the visitor can join the waitlist instead.
5. When a seat frees up (a cancelled ticket, or an offer that ran out), the person who has waited longest gets an email with an offer and 48 hours to claim the seat. An offer that is not claimed passes to the next person. Anyone can leave the waitlist from the link in their email.
6. The **Attendees** tab lists the tickets of an event (name, email, ticket type, status, booking date) and its waitlist. **Resend** mails the confirmation again (at most 5 times per order and not twice within 10 minutes). **Cancel** frees the seat after you confirm, and offers it to the waitlist; the attendee is not emailed.
7. **Paid tickets**: set a price on a ticket type, in the smallest unit of the site currency (1250 is 12.50 USD). The buyer sees the total with any coupon, bundle discount and fee, then pays on the provider's page. The tickets are held for 15 minutes while the buyer pays and are confirmed by email once the payment arrives. A priced type has no waitlist. In the **Orders** tab you can refund or cancel an order and manage coupons. Bundle discounts are set per event.
8. **Stripe setup**: put a secret or restricted key in the settings. In Stripe add a webhook endpoint `<your site>/p/event-tickets/stripe/webhook` for the events `checkout.session.completed`, `checkout.session.async_payment_succeeded`, `checkout.session.async_payment_failed` and `checkout.session.expired`, and put its signing secret in the settings.
9. **PayPal setup**: create a REST app, put its client ID and secret in the settings, and add a webhook `<your site>/p/event-tickets/paypal/webhook` for `CHECKOUT.ORDER.APPROVED` and `PAYMENT.CAPTURE.COMPLETED`. Put the webhook ID in the settings. Turn on **PayPal test mode** while you try it with sandbox accounts.
10. **Export CSV**: the admin page cannot start a download, so it shows the path `/api/v1/p/event-tickets/admin/events/<id>/attendees.csv`. Copy it, put your site address in front and open it in the browser while you are signed in to the admin.
11. **Check-in**: open the **Check-in** tab at the door and pick the event. The line at the top shows how many tickets are checked in. **Scan QR (camera)** opens the camera dialog: take one photo of the ticket's QR code (on a phone screen or on paper) and the ticket is checked in; one photo per tap, and if no code is found you tap again. The result is listed as **OK**, **ALREADY CHECKED IN** (with the time of the first check-in) or **REFUSED** (not a valid ticket, a ticket for another event, or a ticket that is cancelled, refunded, expired or not paid). **Undo** next to an OK result takes the check-in back after you confirm. To check someone in by hand, search by name or by the start of the ticket code and press **Check in**. The owner must grant the camera on the plugin's Permissions tab first; without it, search still works.
12. **Weak signal**: a scan that cannot reach the server is kept in an offline queue shown under the list and sent in order when the connection is back (the page retries every 15 seconds). A queued scan that turns out to be already checked in or refused is reported in the list like any other.
13. **Payouts** (owner only): under **Tickets** on an event, **Payout splits** lists who is owed a share of its ticket money: a name, a payout email and either a percent (for example 80 or 12.5) or a fixed amount per ticket. The **Payouts** tab shows, for a calendar month, what each payee is owed, with gross, refunds, fees, share and the amount owed. After you have paid someone by hand, **Mark paid** records the date and a reference; **Undo** removes the record. For a spreadsheet, the tab shows the path `/api/v1/p/event-tickets/admin/payouts.csv?period=YYYY-MM`: copy it, put your site address in front and open it in the browser while you are signed in to the admin. It is a report only: the plugin sends no money.

## Settings

- **Currency**: three-letter code such as `USD` or `EUR`, default `USD`. One currency per site. Currencies with three decimals (BHD, JOD, KWD, OMR, TND) cannot be used for payments.
- **Sender name**: signs the emails sent to attendees. Empty uses the site name.
- **Confirmation message**: extra text added to every confirmation email, for example how to find the entrance. Empty adds nothing.
- **Default number of tickets**: pre-filled when you add a ticket type, default 50.
- **Keep attendee details for (months)**: names and email addresses are erased this many months after the event ended, default 24 (1 to 120). Ticket counts stay.
- **Site address**: address of the site (`https://example.com`), used for QR images in emails when the event's own address is a path only. Empty is fine when the event address is absolute.
- **Event content type**: empty uses **Calendar event** and any older `event` type. Or name one content type.
- **Stripe secret key**: a secret or restricted key. Stored and never shown.
- **Stripe webhook signing secret**: the `whsec_` secret of your webhook endpoint. Stored and never shown.
- **PayPal client ID**: of your REST app. Empty means PayPal is not offered.
- **PayPal secret**: stored and never shown.
- **PayPal webhook ID**: the ID of your PayPal webhook. Without it a payment is still completed when the buyer returns to your site.
- **PayPal test mode**: use the PayPal sandbox instead of the live service.
- **Fixed fee**: in the smallest currency unit, default 0.
- **Percentage fee**: in hundredths of a percent (250 is 2.5 %), default 0.
- **Fixed fee applies per**: once per order or once per paid ticket.
- **Who pays the fee**: the buyer (added on top) or you (the buyer pays the ticket prices only).

The **Settings** tab of the admin page shows the current values; change them in the plugin settings form.

## Permissions

- `entities.read`: reads the event's name, dates, status, address and join link.
- `db`: keeps ticket types, fields, orders, attendees, the waitlist, the mail queue and the signing key in the plugin's own database.
- `settings`: reads the settings above (defaults, sender name, retention).
- `mail.send`: sends the confirmation with one QR code per ticket and the waitlist mails. It respects the site's recipients-per-hour quota: mail over the quota is queued and retried.
- `http.fetch`: talks to the payment providers you configure, and only to `api.stripe.com`, `api-m.paypal.com` and `api-m.sandbox.paypal.com`: it opens checkout sessions and PayPal orders, captures PayPal payments, checks that a PayPal webhook is genuine and makes refunds. A site that installed an earlier version has to grant it again.
- `builder.data`: feeds the **Tickets** list on event pages (ticket types, with how many are left).
- `builder.island`: runs the booking form on the event page; a widget cannot hold a form on its own.
- `ui.allow camera`: lets the admin page take a photo to read ticket QR codes. The owner grants it on the plugin's Permissions tab. The photo is decoded in the page and is never stored or sent.

Payouts need only `db`, and move no money.

Nothing else: no other hosts, no scripts added to your pages.

## Join links

An event can carry a join link for online events. If the event's `join_link_public` is absent or true, the link is public as it is today. If it is false, it is shown only on the ticket page and in the confirmation email. This plugin never shows the join link on the public page.

## Data kept

All of it lives in the plugin's own database:

- **Ticket types and fields** of each event, with how many tickets are taken.
- **Orders**: the buyer's name, email address and answers, when the confirmation was sent, and for paid orders the amounts, currency, provider ids and status. Card data never reaches this plugin.
- **Coupons and bundle rules** you define.
- **Split rules**: for each event, the payee's name, payout email and share. Removed some months after the event ends, like orders (see below).
- **Payout records**: the month, payee name and email, currency, the amount owed when you marked it paid, the date, your reference and the id of the admin user who marked it. Removed after the same number of months, counted from the paid date.
- **Payment events**: the ids of processed provider events, kept 90 days so a repeated event is ignored.
- **PayPal access token**: cached in the plugin database for a short time.
- **Attendees**: one row per ticket, with its status and a random ticket code. The code is part of the ticket link. A checked-in ticket also keeps the check-in time and the id of the admin user who checked it in.
- **Waitlist**: name, email address and status of everyone who joined.
- **Mail queue**: ids of the mails still to be sent, never the text or the address.
- **Event dates**, copied from the event so the retention job knows when it ended.
- **Signing key**: the key that signs the booking form's token is generated on first use and kept in the plugin database. It is never a setting and is never shown.

Orders are anonymised the number of months set in **Keep attendee details for** after the event ends: name, email and answers are cleared. The waitlist rows of that event are removed then, and queued mails that point at nothing are dropped. The ticket rows stay, since counts and codes are not personal. Split rules are deleted at the same time, and payout records the same number of months after their paid date, but never while a split rule for that payee still exists, so a paid month does not come back as owed. Checkouts that were started but never paid are deleted after 7 days. Everything is removed when you remove the plugin's data.

## Limits

- The counts on a cached page can be up to 5 minutes old. They are checked again when someone books, so a page is never oversold.
- There is one ticket pool per event: the dates of a recurring event share it.
- Tickets are held for 15 minutes while a buyer pays. A Stripe session can stay open up to 30 minutes; a payment that arrives after the hold ended re-takes the tickets if they are still free, and is refunded otherwise.
- One currency per site. Taxes are not calculated.
- At most 2 unpaid holds per email address at a time; a buyer over the limit is asked to try again later. A cart that would push a type over its hold share (below) is answered as sold out for that type. A hold that lapsed or was cancelled does not count towards the 200 orders per hour an event takes.
- Holds are unauthenticated: anyone can start a checkout. At most max(10, half of a type's total capacity) seats of a type can sit in unpaid 15-minute holds at a time. That is a share of the capacity, not of the seats left: once half of a type is sold, the remaining seats can all be held, and a type of 20 seats or fewer can be held in full.
- At most 10 tickets per booking, 10 extra questions per event.
- QR images in emails need an absolute site address: the event's address, or the **Site address** setting.
- The offline queue is kept in this page only: the admin page runs in a sandbox that has no browser storage, so scans waiting to be sent are lost if you reload or close the page. The tab warns you while the queue has items.
- The check-in time is the time the server received the scan, so a scan that waited in the queue is stamped when it syncs.
- One photo per scan: the camera dialog takes a still picture, not a video stream, and a QR code that is blurred or cut off needs a new photo.
- Check-in is for the owner and editor accounts of the admin; there is no separate door-staff role yet.
- Attendees cannot cancel their own tickets yet; you cancel them from the Attendees tab.
- Payouts are a report. The plugin sends no money and does not connect to Stripe Connect or PayPal payouts.
- Owed is the share of (paid total - refunds - this plugin's fee) per order, never below 0. Stripe or PayPal fees are not known to the plugin and are not deducted.
- A period is a calendar month in UTC by payment date. A refund counts in the month the order was paid.
- The report uses today's split rules. **Mark paid** keeps the amount owed at that time, and the report flags it when the amount later changes. Mark a month paid before you edit the rules.
- One row per payee and currency. A payee is identified by payout email, whatever the name.
- A fixed per-ticket amount counts tickets that were not refunded and whose type has a price today. Each rule is capped at the event's base on its own, so several rules together can go above it. Percent rules of one event cannot add up to more than 100.
- Events without split rules do not appear. Orders with a payment problem are left out and counted above the table.
- Payouts and split rules are for owners only: editors do not see them.

## Build

From the repository root: `scripts/build-plugin.sh plugins/event-tickets`.

## License

Apache-2.0. QR codes are encoded with [github.com/boombuler/barcode](https://github.com/boombuler/barcode) (MIT licence, copyright Florian Sundermann). QR codes are decoded in the page with jsQR 1.4.0 (Apache-2.0, `ui/jsqr.LICENSE`).
