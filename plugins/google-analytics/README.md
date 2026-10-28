# google-analytics

Adds the Google tag (GA4) for your measurement ID to every public page, with a way to leave your own visits out. No theme edits.

## Use

1. Install the plugin (see [Install](#install)).
2. Grant `settings` and `site.scripts` under Plugins > Permissions.
3. Open its **Settings** tab and enter the measurement ID from
   Admin > Data streams in Google Analytics, for example `G-ABC123XYZ9`. A Google
   tag ID (`GT-...`) also works. Lowercase and surrounding spaces are fine. Leave
   it empty to turn the tag off.
4. Save. Cached pages are refreshed. The plugin page shows whether the tag is on.

**Leave your own visits out.** Add `#ga-exclude` to any public page address of your
site (for example `https://your-site/#ga-exclude`) and open it once in each browser
you use. That browser stops sending visits to Google Analytics, and a small bar on
that page says so. **Count a browser again** with `#ga-include`, or with the
"Count it again" button on that bar. The plugin page shows both addresses with
Copy buttons.

## Permissions

- `settings`: read the measurement ID.
- `site.scripts`: run `assets/plugin.js` on public pages to load gtag.js. Without
  the grant the tag is never loaded.

Nothing else: no network access from the plugin itself, no database, no routes, no cron.

## Data kept

Nothing on the server. In the visitor's browser, `localStorage` key
`hatamoto-ga-exclude` = `1` is set only when that browser was excluded. It stays
until you use `#ga-include` or the browser's site data is cleared. The fragment
(`#ga-exclude`) never reaches the server.

## How it works

- A `render.head` filter reads the setting, trims and uppercases it, checks it
  against `^(G|GT)-[A-Z0-9]{4,16}$` and adds
  `<meta name="google-analytics-id" content="G-ABC123XYZ9">` to the head. An empty,
  invalid or unreadable setting adds nothing; the filter never fails a render.
- `assets/plugin.js`, which the core links on every public page, reads that meta
  tag, validates the ID again, sets up `window.dataLayer` (kept if it exists, so it
  coexists with the `gtm` plugin), calls `gtag('js', ...)` and `gtag('config', ID)`
  and injects `https://www.googletagmanager.com/gtag/js?id=<ID>` once.
  An existing `window.gtag` is not replaced.
- For an excluded browser it sets `window['ga-disable-<ID>']` and does not load gtag.js.
- `assets/plugin.css` styles the notice bar; there is no inline script or style.

The theme's base layout must include the plugin-body slot (the default theme does).

## Limits

- **Content-Security-Policy.** On strict-script themes (`script-src 'self'`) the
  default Report-Only policy logs a violation for gtag.js. With `CSP_ENFORCE=true`
  the browser blocks `googletagmanager.com` and Google's beacons
  (`connect-src 'self'`), so the tag does not work until the policy allows them.
- No consent mode and no cookie banner; use a separate consent solution where the
  law requires it.
- No custom events, ecommerce or user-ID; configure those in Google Analytics.
- Only `G-` and `GT-` IDs. Universal Analytics (`UA-`) is shut down; `GTM-` belongs
  to the `gtm` plugin.
- A crafted `#ga-exclude` link only stops the visitor who opens it from being counted.
- Exclusion is handled on the first load of a page, not on later in-page hash changes.
- A mistyped but well-formed ID is accepted; the plugin cannot check that it exists.

## Develop

Needs the Go toolchain pinned in `go.mod` and, for the loader test, Node.

```
go vet . && go test .
node loader_test.js
rm -rf build && mkdir build && CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
cp plugin.json build/ && cp -r assets ui build/
```

## Install

Pack the build output into a `.hmp` and upload it in Admin > Plugins:

```
hatamoto plugin pack build --out google-analytics.hmp
```

The core refuses unsigned packages unless the owner turns on **Allow unsigned
packages** (Admin > Settings > Plugins). Either enable that for code you have
read, or sign with your own key (`hatamoto keys generate --name mykey`, then add
`--key mykey.key` to `pack`; the core must trust that key). Once the plugin is
listed in the registry you can install it from Admin > Plugins directly. See
the docs at [hatamoto.dev](https://hatamoto.dev) and the
[SDK](https://github.com/Zen-Labs-Corp/hatamoto-sdk).

## License

Apache-2.0, see `LICENSE`.
