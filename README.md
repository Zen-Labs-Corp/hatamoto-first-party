# hatamoto-first-party

The first-party plugins and kits for [Hatamoto](https://hatamoto.dev), one folder each.
They are built from source, signed with the Hatamoto first-party key and
listed in the plugin directory at hatamoto.dev/plugins.

| Plugin | Folder | What it does |
|---|---|---|
| `gtm` | [plugins/gtm](plugins/gtm) | Google Tag Manager: loads your GTM container on every public page |
| `bing-webmaster` | [plugins/bing-webmaster](plugins/bing-webmaster) | Bing Webmaster Tools: imports Bing clicks, impressions, queries and crawl issues, submits your sitemap |
| `google-analytics` | [plugins/google-analytics](plugins/google-analytics) | Google Analytics: loads the Google tag (GA4) on every public page, with a way to leave your own visits out |
| `payment-links` | [plugins/payment-links](plugins/payment-links) | Payments with Stripe Payment Links: buttons for your pages, an owner email per payment and a payments log |
| `searchconsole` | [plugins/searchconsole](plugins/searchconsole) | Google Search Console: OAuth, sitemap submit, URL inspection, daily search performance |
| `stats` | [plugins/stats](plugins/stats) | Receives the opt-in install stats ping and shows the counts (runs on hatamoto.dev only) |

Each plugin is its own Go module, built for `wasip1` against the public
[SDK](https://github.com/Zen-Labs-Corp/hatamoto-sdk) (`hatamoto.dev/sdk`). The
plugin contract is
[PLUGIN_API.md](https://github.com/Zen-Labs-Corp/hatamoto/blob/main/docs/PLUGIN_API.md)
in the core repository.

## Kits

A kit sets a site up in one step: plugins, a theme, content types, pages, menus and
comment settings, reviewed and ticked by the owner.

| Kit | Folder | What it sets up |
|---|---|---|
| `hn` | [kits/hn](kits/hn) | Hacker News style community: the hn plugin, the hn-classic theme, pages, menu and comment settings |

## Layout

```
plugins/<name>/        one plugin; <name> = plugin.json "name" = route prefix /p/<name>/
template/              what scripts/new-plugin.sh copies
scripts/build-plugin.sh   vet, test and build one plugin exactly as CI and the registry do
scripts/new-plugin.sh     create plugins/<name> from the template
scripts/changed-plugins.sh  which plugins a change touches (CI)
kits/<name>/           one kit: kit.json (+ README.md)
scripts/check-kit.sh      check a kit (core's checker + parity with the plugins)
scripts/build-kit.sh      check a kit and pack it for local tests
scripts/test-check-kit.sh prove check-kit.sh refuses broken kits
scripts/changed-kits.sh   which kits a change touches (CI)
scripts/hatamoto-bin.sh   the hatamoto CLI built from the core commit in .hatamoto-core
.hatamoto-core         the core commit whose CLI checks kits
```

## Build

```sh
scripts/build-plugin.sh plugins/gtm      # -> plugins/gtm/build/
```

Needs Go (the `toolchain` line of each `go.mod` pins it) and `jq`; `node` runs
any `*_test.js`. No `go.work`: every plugin builds alone with `GOWORK=off`, as
the registry build does.

```sh
scripts/check-kit.sh kits/hn             # core's checker, plugin parity, pack round trip
scripts/build-kit.sh kits/hn             # -> kits/hn/build/hn-<version>.kit.hmp
```

The first kit run builds the pinned core CLI with Go and caches it; `HATAMOTO_BIN`
overrides it.

## Releases

A release of one plugin is the tag `<name>/v<version>` on the commit that sets
that version in `plugins/<name>/plugin.json`. The registry lists it once
`manifests/<name>.json` in
[hatamoto-plugins](https://github.com/Zen-Labs-Corp/hatamoto-plugins) pins that
commit with `"subdir": "plugins/<name>"`.

A kit is tagged `kit-<name>/v<version>` and listed in the registry under
`manifests/kits/<name>.json` with `"subdir": "kits/<name>"`. It is never mirrored to
a per-name repository.

## History

`gtm`, `searchconsole` and `stats` were imported with their full history from
their former repositories (`hatamoto-plugin-<name>`, now archived).

## License

Apache-2.0, see [LICENSE](LICENSE). Contributions: see
[CONTRIBUTING.md](CONTRIBUTING.md).
