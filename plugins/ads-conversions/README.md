# ads-conversions

Replaces the server-side part of Pixel Manager, Conversios, the official TikTok and LinkedIn plugins and Google Ads conversion tracking add-ons for WordPress.

Sends a server-side conversion to Google Ads, TikTok and LinkedIn for each form submission, with hashed contact details and the ad click ID.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for, including `forms.read`: without it the host delivers no form submissions to the plugin.
2. Open its **Settings** tab. In **General** set the **Site address** (for example `https://example.com`), pick the **Forms** to send (one slug per line; empty means nothing is sent), and optionally an **Alert email**, the **Email field** and **Phone field** names of your forms, a **Consent field**, a **Default country code**, and a **Conversion value** with its **Currency**.
3. Fill in the sections of the platforms you use and switch them on:
   - **Google Ads**: the OAuth client ID and secret, the Google Ads customer ID, the conversion action ID and, when you reach the account through a manager account, the manager ID. Register the redirect address `<site address>/p/ads-conversions/oauth/google/callback` with the OAuth client, then press **Connect** on the plugin's page.
   - **TikTok**: the pixel code and the Events API access token from Events Manager. The event name defaults to `SubmitForm`; a test event code is optional and is used only by **Test**, never by live conversions.
   - **LinkedIn**: the client ID and secret of your LinkedIn app, the conversion rule ID and the ad account ID. Register `<site address>/p/ads-conversions/oauth/linkedin/callback` as the app's redirect URL, then press **Connect**.
4. The plugin's page has one card per platform and a delivery log. **Connect** gives you an address to open in a new browser tab (a plugin page cannot open other sites itself); approve access there and reload the page. **Test** checks a platform without sending a real conversion: Google validates a request only, TikTok sends a test event, LinkedIn reads the conversion rule.
5. Each result appears in the delivery log (newest 200, kept 30 days): sent, retrying, failed, skipped or test, with the platform's code and message.

Every stored submission of a selected form becomes one conversion per enabled platform. The deduplication key is the submission id (`transaction ID` for Google, `event_id` for TikTok, `eventId` for LinkedIn): give the browser tags the same value and the platform counts the two as one. A second delivery of the same submission sends nothing.

The conversion carries the hashed email (SHA-256, trimmed and lower-cased; for Google the dots before the `@` of a gmail.com or googlemail.com address are removed first) and, for Google and TikTok, the hashed phone number in E.164 form (`+` and the country code, 8 to 15 digits; a number without a `+` or `00` prefix gets the default country code). LinkedIn receives no phone number. Invalid input is dropped, never an error. A conversion that has no identifier left for a platform is logged as skipped.

Ad click IDs (`gclid`, `gbraid`, `wbraid`, `ttclid`, `li_fat_id`) come from two places. The plugin's page script (loaded with `site.scripts`) keeps them in a first-party cookie `hm_adc` for 90 days and posts them with the submission id when the form is stored. When a **Consent field** is set, the submission's page address is the fallback: only those five parameters are read from it and the address itself is never stored. When a **Consent field** is set, only submissions where it is ticked are sent. The script itself does nothing until the visitor has agreed to marketing cookies; with no consent banner it starts at once.

**Consent.** Without a **Consent field** the server-side conversion (the hashed contact details and any click ID the plugin has) is sent for every stored submission of a selected form, whatever the visitor chose in the cookie banner; the banner only gates the page script. Without a consent field the page address is not read for click IDs, so a click ID in it is not sent; only the IDs the script posted after consent are used. If you need consent for this processing, set the **Consent field** to a checkbox of your forms (recommended).

A conversion is sent one to two minutes after the submission, so that the page script's click IDs can arrive first.

## Permissions

- `forms.read`: the host delivers `form.submitted` only to plugins that hold it.
- `settings`: reads the configuration and the write-only secrets (client secrets, TikTok token).
- `http.fetch`: calls the platforms and the OAuth token endpoints, only the hosts in `allowed_hosts`: `datamanager.googleapis.com` (Google conversions), `oauth2.googleapis.com` (Google tokens), `business-api.tiktok.com` (TikTok events), `api.linkedin.com` (LinkedIn conversions) and `www.linkedin.com` (LinkedIn token endpoint).
- `db`: the queue, the posted click IDs and the delivery log.
- `kv`: the one-time OAuth state, the sealed access tokens and the flags for connection status and alerts.
- `mail.send`: tells the alert email once when a connection needs reconnecting or a LinkedIn token is about to expire.
- `site.scripts`: serves `assets/plugin.js`, which keeps the ad click ID after marketing consent and posts it with the submission id.
- `log`: one line for each failure or state change. Never a field value, a hash, a click ID or a token.

## Data kept

- Table `queue`: one row per submission and platform, holding only hashes (email, phone), click IDs, the form, the page path without query and the retry state. A row is deleted when the conversion is sent, when the platform refuses it for good, or after 24 hours.
- Table `clicks`: the click IDs a visitor's browser posted, for one hour.
- Table `log`: submission id, form, platform, result, a short reason, the platform's code and its message (cut to 300 characters), for 30 days. Never a field value, a hash, a click ID or a token.
- Key-value store: the OAuth state for 10 minutes; the access and refresh tokens, sealed with a key derived from the platform's client secret (a changed secret means connecting again); a "needs reconnecting" flag and an "alert sent" flag per platform.
- Never kept anywhere: a raw email address, a phone number or a name. The `source_path` of a submission is never stored. Names are not sent at all.

## Build

From the repository root: `scripts/build-plugin.sh plugins/ads-conversions`.

## Limits

- **Google**: you need a Google Cloud OAuth client with the Data Manager API enabled and the redirect address registered. Google processes uploads asynchronously: a sent conversion can still be rejected later, so check the diagnostics in Google Ads.
- **LinkedIn**: the app needs Conversions API access. Access tokens last 60 days and need reconnecting unless the app has programmatic refresh tokens; the plugin mails the alert email 7 days before a token runs out.
- **TikTok**: needs an Events API access token from Events Manager.
- The host never passes the visitor's IP address or user agent to plugins, so match quality comes from the hashed contact details and the click IDs.
- Click IDs need the plugin script (`site.scripts`) and marketing consent. Without them a conversion is still sent with the hashes only.
- A conversion may arrive one to two minutes after the submission.
- Names are not sent: LinkedIn's `userInfo` and Google's address matching need plain names, which this plugin never keeps.
- A platform that needs reconnecting is paused; its queued conversions are kept for up to 24 hours and then logged as expired.
- No browser tags, no Meta, no offline or CRM-stage conversions, no reporting, and no backfill of submissions from before the plugin was set up.

## License

Apache-2.0.
