# payment-links

Take payments and donations through Stripe Payment Links: payment buttons for your
pages, an owner email for every payment and a payments log. It replaces the payment
forms of WordPress plugins such as GiveWP, WPForms and Forminator. The buttons link
straight to Stripe, so card data never touches your site.

## Use

1. Install the plugin and grant its permissions under Plugins > Permissions.
2. In Stripe, create your Payment Links.
3. In Stripe, create a restricted key with **read** access to Events and Checkout
   Sessions. Also give it Payment Links read (recommended): without it, payments are
   labelled with the Stripe link id instead of your label.
4. In the plugin **Settings** tab enter the key, your links (label, Payment Link URL,
   kind payment or donation), whether to email you (`notify`), the address for those
   emails (`notify_email`) and how many months to keep payments (default 24).
5. In Stripe, add a webhook endpoint with the address
   `https://<your site>/p/payment-links/webhook` (for example
   `https://example.com/p/payment-links/webhook`) and select the events
   `checkout.session.completed` and `checkout.session.async_payment_succeeded`.
6. Add buttons. In a template use the partial, with the label and URL of a link:

   ```
   {{ template "plugin/payment_links/button" (dict "label" "Donate" "url" "https://buy.stripe.com/your-link") }}
   ```

   The partial is the supported way to show a button. The plugin also registers a
   **Payment button** section, but the page editor has no fields for it, so you
   cannot set its `label` and `url` there. It renders from section content that has
   `label` and `url` keys, for example content you pass through `render.page` or the
   API; with no content it shows an inert, empty element. The button is a plain link
   with the class `pl-button`; a URL that does not start with `https://` renders as
   plain text. No script is loaded.

The plugin page shows whether the key is set, the webhook path, the button code for
each link, totals per month and currency, and the payments. The CSV export is at
`/api/v1/p/payment-links/payments.csv`: open it in a new tab while signed in to the
admin (the plugin page cannot start a download itself).

## Permissions

- `settings`: read the Stripe key, the links, the email options and the retention.
- `http.fetch`: confirm each webhook event, and look up its payment link, at
  `api.stripe.com` (the only allowed host).
- `db`: the payments log and the ids of processed events.
- `mail.send`: the email to the owner for each payment.
- `log`: operational errors, never the key or customer data.

Not requested: `site.scripts` (no script runs on your public pages) and `kv`.

## Data kept

One row per confirmed payment: Stripe event id, checkout session id, amount in minor
units, currency, customer email, link label and kind, payment link id, live or test
mode, the time Stripe created it and the time it was received. Rows older than the
retention setting (24 months by default, 1 to 120) are deleted by a job that runs
every six hours. The Stripe key lives in the plugin settings (write-only) and is
never shown, logged, mailed or returned by a route.

## Build

From the repository root: `scripts/build-plugin.sh plugins/payment-links`.

For a development build by hand, also copy the folders the script packs into
`build/`: `cp -r migrations ui partials sections build/`.

## Limits

- **No signature check yet.** Core does not pass the `Stripe-Signature` header to
  plugin routes. The plugin therefore reads only the event id from the posted body
  and fetches the event from Stripe with your key; only what Stripe returns is
  recorded. Anyone can post the id of a real, old event, which is harmless because
  recording is idempotent. The signature will be verified once core provides it.
- Only one-time payments and donations: no subscriptions, carts, tax or refunds.
- Only checkouts started from a payment link are recorded. Delayed payment methods
  are recorded when Stripe sends `checkout.session.async_payment_succeeded`.
- The owner email follows the site mail limit (6 recipients a minute); extra emails
  in a burst are dropped and logged, the payment is still recorded.
- A plugin call has 10 seconds and a 1 MiB answer. The CSV holds the latest 10 000
  payments.
- If Stripe or the database is unreachable the webhook answers 503 so Stripe retries.

## License

Apache-2.0, see `LICENSE`.
