# meta-capi

Meta Conversions API: sends a server-side Lead event to Meta for each form submission, with hashed contact details, to back up the browser pixel.

Replaces the server-side part of PixelYourSite and Meta pixel for WordPress.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for, including `forms.read`: without it the host delivers no form submissions to the plugin.
2. Open its **Settings** tab and enter the **Pixel ID** and the **Access token** (write-only: stored, never shown again). Set the **Site address** (for example `https://example.com`) so each event carries the page it came from.
3. Optionally limit it to some **Forms** (empty means all), map the **Email**, **Phone** and **Name** field names of your forms, and name a **Consent field**: when set, only submissions where it is checked (`true`, `1`, `yes`, `on`, `y`, `checked`) or, for a checkbox with options, has at least one option chosen are sent.
4. Open the plugin's page and press **Send test event**. Put a test event code from Events Manager > Test events in the settings first; the event then shows up there.

Each stored submission produces one event (`Lead` by default) whose `event_id` is the submission id. Give the browser pixel the same value as its `eventID` and Meta counts the two as one event. A second delivery of the same submission sends nothing.

Matching data is hashed (SHA-256) after cleaning, as Meta asks: email trimmed and lower-cased; phone reduced to digits without leading zeros; first and last name lower-cased without punctuation (the last word of the name field is the last name). **Phone numbers must include the country code**; the plugin does not guess one, and numbers under 7 digits are ignored. A submission with neither a usable email nor phone is not sent.

## Permissions

- `forms.read`: the host delivers `form.submitted` only to plugins that hold it.
- `settings`: reads the pixel ID, token and field mapping.
- `http.fetch`: one POST per submission to `graph.facebook.com` (the only entry in `allowed_hosts`).
- `db`: the delivery log table.
- `log`: one line for each failed delivery, with a status or error code. Never a field value, a hash or the token.

## Data kept

The table `deliveries` holds, per submission: the submission id, the form, the result (sent, failed or skipped) with a short reason, Meta's answer code and error message, a test flag and the time. It never holds a field value, a hash or the token. Rows older than 30 days are deleted whenever a new row is added. The admin routes return the newest 200 rows and never the token.

## Build

From the repository root: `scripts/build-plugin.sh plugins/meta-capi`.

## Limits

- The host never passes the visitor's IP address or user agent (or the `fbp` and `fbc` cookies), so matching uses the hashed email and phone (and name) only, and Meta may flag or drop events for the missing user agent. The browser pixel stays the primary path; this adds the server side.
- No retries, queue or backfill: a failed delivery is logged once and not sent again; submissions from before the plugin was set up are not sent.
- The send is synchronous: up to 8 seconds inside the host's 10 second hook limit.
- Only the configured event name is sent; no purchase or other event types.

## License

Apache-2.0.
