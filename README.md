# gtm

Loads your Google Tag Manager container on every public page. No theme edits.

## Use

1. Install the plugin (see [Install](#install)).
2. Open its **Settings** tab and enter the container ID from
   [tagmanager.google.com](https://tagmanager.google.com), for example
   `GTM-ABC1234`. Lowercase and surrounding spaces are fine. Leave it empty to
   turn the tag off.
3. Save. Cached pages are refreshed.

The plugin asks for the `settings` permission only.

## How it works

- A `render.head` filter reads the setting, trims and uppercases it, checks it
  against `^GTM-[A-Z0-9]{4,12}$` and adds `<meta name="gtm-container" content="GTM-ABC1234">`
  to the head. An empty or invalid ID adds nothing; the filter never fails a render.
- `assets/plugin.js`, which the core links on every public page, reads that
  meta tag, validates the ID again, sets up `window.dataLayer`, pushes
  `{'gtm.start': <ms>, event: 'gtm.js'}` and injects
  `https://www.googletagmanager.com/gtm.js?id=<ID>`. It does nothing without a
  valid ID and never loads the same container twice.

The theme's base layout must include the plugin-body slot (the default theme
does) so the core can link `plugin.js`.

## Limits

- **No `<noscript>` fallback.** It would need a slot right after `<body>`,
  which the core does not have. Visitors without JavaScript are not tagged.
- No consent mode, no per-page dataLayer values, no server-side GTM domain:
  only `www.googletagmanager.com`.
- **Content-Security-Policy.** The default policy is Report-Only
  (`script-src 'self' 'unsafe-inline'` on themes without `csp.strictScripts`,
  `script-src 'self'` on the default theme): GTM works but each load logs a
  violation report. With `CSP_ENFORCE=true` the browser blocks
  `googletagmanager.com`, so GTM will not load until the policy allows it.
  On a strict-script theme GTM Custom HTML tags (inline script) never run.
- A mistyped but well-formed ID is accepted; the plugin cannot check that the
  container exists.

## Develop

Needs the Go toolchain pinned in `go.mod` and, for the loader test, Node.

```
go vet ./... && go test ./...
node loader_test.js
rm -rf build && mkdir build && CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
cp plugin.json build/ && cp -r assets build/assets
```

## Install

Pack the build output into a `.hmp` and upload it in Admin > Plugins:

```
hatamoto plugin pack build --out gtm.hmp
```

The core refuses unsigned packages unless the owner turns on **Allow unsigned
packages** (Admin > Settings > Plugins). Either enable that for code you have
read, or sign with your own key (`hatamoto keys generate --name mykey`, then add
`--key mykey.key` to `pack`; the core must trust that key). Once the plugin is
listed in the registry you can install it from Admin > Plugins directly. See
the docs at [hatamoto.dev](https://hatamoto.dev) and the
[SDK](https://github.com/zenlabscorp/hatamoto-sdk).

## License

Apache-2.0, see `LICENSE`.
