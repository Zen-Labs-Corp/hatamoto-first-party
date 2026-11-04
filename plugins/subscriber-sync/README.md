# subscriber-sync

Adds people who submit your forms to Mailchimp, Brevo, MailerLite or Kit, with field mapping, an optional consent field and retries.

It replaces the WordPress plugins MC4WP, Mailchimp for WooCommerce, Brevo, MailerLite and Kit for the one job they share: a form submission becomes a subscriber in your email list.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Open its **Settings** tab and choose the provider, paste the API key and set the list:
   - Mailchimp: the audience id. The key ends in `-usNN`.
   - Brevo: the list id (a number).
   - MailerLite: the group id.
   - Kit: the form id.
3. Under **Forms to sync** enter the form slugs, one per line. Empty means nothing is sent.
4. Set the email field (default `email`) and, if you want them, the first name, last name and consent fields.
5. Open the plugin page and press **Test connection**. It shows the list name it found.

When a watched form is submitted, the person is added to the list, or updated if already there. If a consent field is set, only submissions where it is ticked (or `yes`, `true`, `1`, `on`, `checked`, `y`) are sent; the others are logged as skipped. Failures that may pass are retried in the background.

The plugin page shows the provider, list and forms, and the last 100 deliveries.

## Permissions

- `forms.read`: receive form submissions to read the email, names and consent field.
- `settings`: read the provider, API key, list and field mapping you saved.
- `http.fetch`: call the provider's API. Only these hosts are allowed: `*.api.mailchimp.com`, `api.brevo.com`, `connect.mailerlite.com`, `api.kit.com`.
- `db`: keep the delivery queue and the delivery log in the plugin's own tables.
- `log`: write host log lines about failures. They hold counts and fixed words, never email addresses, names or the key.

## Data kept

- Queue: while a delivery is pending, the email address and first and last name, until it succeeds, fails for good, or is 24 hours old.
- Log: time, form slug, status, short reason and the provider's HTTP code. No email addresses and no names. Rows older than 30 days are deleted.
- The API key is stored in the settings, is never shown again, and never appears in the page, the log or any response.

## Build

From the repository root: `scripts/build-plugin.sh plugins/subscriber-sync`.

## Limits

- One provider and one list at a time. Nothing is synced back (unsubscribes, edits).
- Double opt-in: for Mailchimp the setting sets `status_if_new` to pending (confirmation email) or subscribed. For Kit it creates new subscribers as inactive, so the form's confirmation email applies, or as active. Brevo and MailerLite follow their own account setting.
- Tags: only Mailchimp (added by name) and Kit (existing tag names; unknown names are skipped). Brevo and MailerLite get none.
- At most 10 tags can be set.
- A delivery that does not finish within one run's time budget is retried later with a growing wait, like any other failure (log reason `too slow`).
- Kit gets email and first name only, no last name.
- Mailchimp reports success the same way for new and existing members, so the log shows `sent` for both.
- Mailchimp keys must end in `-us` and one or two digits.
- HTTP 429, 408 and 5xx answers, timeouts and network errors are retried with a growing wait (5 minutes up to 2 hours) for up to 24 hours. Any other 4xx answer is not retried and is logged as failed.
- The queue holds a submission for 24 hours at most. The log is kept 30 days, without email addresses.
- A submission with no valid email address (`name@example.com`, at most 254 characters) is skipped.

## License

Apache-2.0.
